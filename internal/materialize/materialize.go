// Package materialize writes nodes out as markdown into the user's vault
// (<vault>/_zyme/), so Zyme output shows up inside Obsidian.
// One-way, generate-only, namespaced: never edits user files, only writes _zyme/.
package materialize

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteNode writes a node's markdown to <vault>/_zyme/<short-id>.md with zyme frontmatter.
// Takes primitives so it does not depend on the model package. Returns the path written.
func WriteNode(vaultPath, id, kind, role, sourceURI, markdown string) (string, error) {
	dir := filepath.Join(vaultPath, "_zyme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create _zyme dir: %w", err)
	}
	path := filepath.Join(dir, shortID(id)+".md")

	front := fmt.Sprintf("---\nzyme_id: %s\nkind: %s\nrole: %s\nsource_uri: %q\n---\n\n",
		id, kind, role, sourceURI)

	if err := os.WriteFile(path, []byte(front+markdown), 0o644); err != nil {
		return "", fmt.Errorf("write materialized file: %w", err)
	}
	return path, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
