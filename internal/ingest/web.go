// Package ingest contains source adapters that turn external data into a
// standard payload (Track A snapshot + Track B cleaned text).
package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/go-shiori/go-readability"
)

// WebResult is the standard payload from fetching a web URL.
type WebResult struct {
	URL         string
	NodeID      string // sha256(html): content-addressed identity for frozen nodes
	Title       string
	Markdown    string // Track B cleaned text (starts with "# <title>" when available)
	HTML        []byte // Track A raw (MHTML packaging is a TODO; raw HTML stored for now)
	SnapshotKey string // NodeID + ".html"
}

// FetchWeb downloads rawURL, extracts main text (readability), and writes a
// content-addressed snapshot of the raw HTML to snapshotDir (Track A).
func FetchWeb(ctx context.Context, rawURL, snapshotDir string) (*WebResult, error) {
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

	sum := sha256.Sum256(html)
	nodeID := hex.EncodeToString(sum[:])
	key := nodeID + ".html"
	if err := saveSnapshot(snapshotDir, key, html); err != nil {
		return nil, err
	}

	md := article.TextContent
	if article.Title != "" {
		md = "# " + article.Title + "\n\n" + md
	}
	return &WebResult{
		URL:         rawURL,
		NodeID:      nodeID,
		Title:       article.Title,
		Markdown:    md,
		HTML:        html,
		SnapshotKey: key,
	}, nil
}

func saveSnapshot(dir, key string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, key), data, 0o644); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	return nil
}
