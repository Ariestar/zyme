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
	"time"
)

// GitHub adapter: import the authenticated user's starred repos.
// One repo -> one node (Kind "star"), identity = repo URL (live: README updates over time).
// Track B = README text (fallback: description); Track A = repo metadata JSON.
//
// Requires ZYME_GITHUB_TOKEN (a PAT with read:user / public_repo).
// Optional: ref.Options["limit"] caps the count (useful for a first test).
type GitHub struct{}

func (GitHub) ID() string { return "github" }

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

	out := make([]IngestPayload, 0, len(repos))
	for i, repo := range repos {
		if limit > 0 && i >= limit {
			break
		}
		out = append(out, ghRepoPayload(ctx, token, repo, time.Now()))
	}
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

		resp, err := http.DefaultClient.Do(req)
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
	resp, err := http.DefaultClient.Do(req)
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
