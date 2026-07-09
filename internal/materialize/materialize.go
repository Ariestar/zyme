// Package materialize writes nodes out as markdown into the user's vault
// (<vault>/_zyme/), so Zyme output shows up inside Obsidian.
// One-way, generate-only, namespaced: never edits user files, only writes _zyme/.
package materialize

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteNode writes a node's markdown to <vault>/_zyme/<title>.md with zyme frontmatter.
//
// The filename is derived from the markdown's H1 title (sanitized) so Obsidian
// shows a human-readable note title instead of a hash. On a title collision
// (same title, different node) a short-id suffix is appended. The full node id
// stays in frontmatter as the stable identity.
func WriteNode(vaultPath, id, kind, role, sourceURI, markdown string) (string, error) {
	dir := filepath.Join(vaultPath, "_zyme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create _zyme dir: %w", err)
	}

	base := sanitizeFilename(titleFromMarkdown(markdown))
	if base == "" {
		base = shortID(id) // no H1 in markdown — fall back to id
	}
	name := base + ".md"
	path := filepath.Join(dir, name)

	// If a same-named file exists for a DIFFERENT node, disambiguate with the id.
	if existing := readZymeID(path); existing != "" && existing != id {
		name = base + "-" + shortID(id) + ".md"
		path = filepath.Join(dir, name)
	}

	front := fmt.Sprintf("---\nzyme_id: %s\nkind: %s\nrole: %s\nsource_uri: %q\n---\n\n",
		id, kind, role, sourceURI)

	if err := os.WriteFile(path, []byte(front+markdown), 0o644); err != nil {
		return "", fmt.Errorf("write materialized file: %w", err)
	}
	return path, nil
}

// titleFromMarkdown returns the first "# heading" line, or "" if there is none.
func titleFromMarkdown(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
		if line != "" {
			break
		}
	}
	return ""
}

// sanitizeFilename makes a title safe and sane as a filename (Windows-safe).
func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	for _, c := range `\/:*?"<>|` {
		s = strings.ReplaceAll(s, string(c), "-")
	}
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, " .-")
	if len(s) > 80 {
		s = s[:80]
	}
	return strings.Trim(s, " .-")
}

// readZymeID reads zyme_id from a file's frontmatter; "" if missing/unreadable.
func readZymeID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, "zyme_id:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "zyme_id:"))
		}
	}
	return ""
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
