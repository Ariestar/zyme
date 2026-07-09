package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"zyme/internal/embed"
	"zyme/internal/model"
	"zyme/internal/store"
)

// Pipeline is the source-agnostic boundary: every IngestPayload flows through Save,
// regardless of which adapter produced it.
type Pipeline struct {
	Store       *store.Store
	Embed       *embed.Client
	SnapshotDir string
}

// Save embeds the payload's markdown and stores node + version. Returns the node ID,
// the version number written, and skipped=true if the content was unchanged since the
// last version (so no new version or embedding was produced).
func (pl *Pipeline) Save(ctx context.Context, p IngestPayload) (nodeID string, version int, skipped bool, err error) {
	if p.FetchedAt.IsZero() {
		p.FetchedAt = time.Now()
	}
	id := p.Identity
	if id == "" {
		id = contentHash(p.Snapshot, p.Markdown)
	}

	node := model.Node{ID: id, Kind: model.Kind(p.Kind), Role: model.RoleSource, SourceURI: p.SourceURI}
	if err := pl.Store.InsertNode(ctx, node); err != nil {
		return "", 0, false, fmt.Errorf("insert node: %w", err)
	}

	// Dedup: if the latest version's markdown matches, skip embedding + version append.
	latest, lerr := pl.Store.LatestVersion(ctx, id)
	if lerr == nil && latest.Markdown == p.Markdown {
		return id, latest.Version, true, nil
	}
	if lerr != nil && !errors.Is(lerr, pgx.ErrNoRows) {
		return "", 0, false, fmt.Errorf("latest version: %w", lerr)
	}

	nextVer := 1
	if lerr == nil {
		nextVer = latest.Version + 1
	}

	vec, err := pl.Embed.Embed(ctx, p.Markdown)
	if err != nil {
		return "", 0, false, fmt.Errorf("embed: %w", err)
	}

	snapPath, err := pl.writeSnapshot(id, nextVer, p.Snapshot, p.SnapshotMIME)
	if err != nil {
		return "", 0, false, err
	}
	if err := pl.Store.InsertVersion(ctx, id, nextVer, p.Markdown, snapPath, pl.Embed.Model, vec); err != nil {
		return "", 0, false, fmt.Errorf("insert version: %w", err)
	}
	return id, nextVer, false, nil
}

func (pl *Pipeline) writeSnapshot(nodeID string, version int, data []byte, mime string) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(pl.SnapshotDir, 0o755); err != nil {
		return "", fmt.Errorf("create snapshot dir: %w", err)
	}
	name := fmt.Sprintf("%s-v%d%s", nodeID, version, snapshotExt(mime))
	if err := os.WriteFile(filepath.Join(pl.SnapshotDir, name), data, 0o644); err != nil {
		return "", fmt.Errorf("write snapshot: %w", err)
	}
	return name, nil
}

// contentHash sha256-hashes a mix of []byte and string parts into a hex id.
func contentHash(parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		switch v := p.(type) {
		case []byte:
			h.Write(v)
		case string:
			h.Write([]byte(v))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func snapshotExt(mime string) string {
	switch strings.ToLower(mime) {
	case "text/html", "application/xhtml+xml":
		return ".html"
	case "application/pdf":
		return ".pdf"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "application/json":
		return ".json"
	case "text/plain":
		return ".txt"
	default:
		return ".bin"
	}
}
