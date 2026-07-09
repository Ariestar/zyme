package ingest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-shiori/go-readability"
)

// Web adapter: fetch a single URL and extract its main text via readability.
// Track A = raw HTML bytes (written by Pipeline); Track B = extracted markdown.
// Note: plain HTTP only — JS-rendered pages need a headless variant (TODO).
type Web struct{}

func (Web) ID() string { return "web" }

func (Web) Fetch(ctx context.Context, ref SourceRef) ([]IngestPayload, error) {
	p, err := fetchWeb(ctx, ref.URI)
	if err != nil {
		return nil, err
	}
	return []IngestPayload{*p}, nil
}

func fetchWeb(ctx context.Context, rawURL string) (*IngestPayload, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ZymeBot/0.1 (+https://github.com/zyme)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}
	html, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	article, err := readability.FromReader(bytes.NewReader(html), parsedURL)
	if err != nil {
		return nil, fmt.Errorf("extract text: %w", err)
	}

	md := article.TextContent
	if article.Title != "" {
		md = "# " + article.Title + "\n\n" + md
	}
	// Identity = canonical URL, NOT hash(html): raw HTML drifts between fetches
	// (timestamps, "last edited", ads), which would otherwise spawn duplicate nodes.
	// Dedup still happens via markdown comparison in Pipeline.Save.
	return &IngestPayload{
		Identity:      contentHash(canonicalURL(rawURL)),
		IdentityBasis: "uri",
		Kind:          "page",
		SourceURI:     rawURL,
		Title:         article.Title,
		Markdown:      md,
		Snapshot:      html,
		SnapshotMIME:  "text/html",
		FetchedAt:     time.Now(),
		AdapterID:     "web",
	}, nil
}

// canonicalURL strips fragments and common tracking params so the same article
// reached via different links still maps to one node.
func canonicalURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Fragment = ""
	u.RawFragment = ""
	q := u.Query()
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content"} {
		q.Del(k)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
