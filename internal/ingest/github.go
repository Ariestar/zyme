package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub adapter: import the authenticated user's starred repos.
// One repo -> one node (Kind "star"), identity = repo URL (live: README updates over time).
// Track B = README text (fallback: description); Track A = repo metadata JSON.
//
// Requires ZYME_GITHUB_TOKEN (a PAT). Optional: ref.Options["limit"] caps the count.
// READMEs are fetched concurrently (GitHub API latency dominates) with a per-request
// timeout so one slow repo can't stall the batch.
type GitHub struct{}

func (GitHub) ID() string { return "github" }

// ghHTTP has a timeout so a single stalled request can't hang the whole batch.
var ghHTTP = &http.Client{Timeout: 30 * time.Second}

func (GitHub) Fetch(ctx context.Context, ref SourceRef) ([]IngestPayload, error) {
	token := firstNonEmpty(ref.Options["token"], os.Getenv("ZYME_GITHUB_TOKEN"))
	if token == "" {
		return nil, fmt.Errorf("github adapter requires ZYME_GITHUB_TOKEN (PAT)")
	}
	limit := 0
	if l := firstNonEmpty(ref.Options["limit"], os.Getenv("ZYME_GITHUB_LIMIT")); l != "" {
		limit, _ = strconv.Atoi(l)
	}

	target := firstNonEmpty(ref.URI, "starred")
	if target != "starred" {
		return nil, fmt.Errorf("github adapter: unknown target %q (only 'starred' supported)", target)
	}

	repos, err := ghList(ctx, token, "https://api.github.com/user/starred?per_page=100")
	if err != nil {
		return nil, err
	}

	n := len(repos)
	if limit > 0 && limit < n {
		n = limit
	}

	// Fetch READMEs concurrently. ghRepoPayload never errors (falls back to
	// description), so each goroutine just writes its own index — no mutex needed.
	out := make([]IngestPayload, n)
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = ghRepoPayload(ctx, token, repos[i], time.Now())
		}(i)
	}
	wg.Wait()
	return out, nil
}

type ghRepo struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	HTMLURL     string `json:"html_url"`
}

func ghRepoPayload(ctx context.Context, token string, repo ghRepo, now time.Time) IngestPayload {
	readme := ghReadme(ctx, token, repo.FullName)
	body := firstNonEmpty(readme, repo.Description, repo.FullName)
	md := "# " + repo.FullName + "\n\n" + body + "\n\n_Source: " + repo.HTMLURL + "_"
	snap, _ := json.Marshal(repo)
	return IngestPayload{
		Identity:      contentHash(repo.HTMLURL),
		IdentityBasis: "uri",
		Kind:          "star",
		SourceURI:     repo.HTMLURL,
		Title:         repo.FullName,
		Markdown:      md,
		Snapshot:      snap,
		SnapshotMIME:  "application/json",
		FetchedAt:     now,
		AdapterID:     "github",
	}
}

// ghList follows GitHub's Link-header pagination.
func ghList(ctx context.Context, token, url string) ([]ghRepo, error) {
	var all []ghRepo
	for url != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := ghHTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github api: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("github api HTTP %d: %s", resp.StatusCode, b)
		}
		var page []ghRepo
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode github response: %w", err)
		}
		resp.Body.Close()
		all = append(all, page...)
		url = ghNextLink(resp.Header.Get("Link"))
	}
	return all, nil
}

// ghReadme fetches the raw README for a repo (empty string on any failure).
func ghReadme(ctx context.Context, token, repo string) string {
	url := "https://api.github.com/repos/" + repo + "/readme"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.raw")
	resp, err := ghHTTP.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(b)
}

// ghNextLink extracts the rel="next" URL from a GitHub Link header.
func ghNextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, `rel="next"`) {
			i := strings.Index(part, "<")
			j := strings.Index(part, ">")
			if i >= 0 && j > i {
				return part[i+1 : j]
			}
		}
	}
	return ""
}
