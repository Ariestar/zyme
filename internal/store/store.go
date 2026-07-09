// Package store is the Postgres access layer (nodes, versions, edges, review, coords).
package store

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"zyme/internal/model"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

// Migrate applies the embedded schema (idempotent).
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.Pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// InsertNode upserts a node by id (idempotent re-ingest of the same source).
func (s *Store) InsertNode(ctx context.Context, n model.Node) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO node (id, kind, role, source_uri)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (id) DO UPDATE SET updated_at = now(), source_uri = EXCLUDED.source_uri`,
		n.ID, n.Kind, n.Role, nullable(n.SourceURI))
	return err
}

// CurrentVersion returns the highest version number for a node, or 0 if none.
func (s *Store) CurrentVersion(ctx context.Context, nodeID string) (int, error) {
	var v int
	err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM node_version WHERE node_id = $1`, nodeID).Scan(&v)
	return v, err
}

// InsertVersion appends a version row. Embedding is passed as a pgvector text literal.
func (s *Store) InsertVersion(ctx context.Context, nodeID string, version int, markdown, snapshotPath, embedModel string, embedding []float32) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO node_version (node_id, version, markdown, snapshot_path, embed_model, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6::vector)`,
		nodeID, version, markdown, nullable(snapshotPath), embedModel, vectorLiteral(embedding))
	return err
}

// GetNode fetches a node by exact id or by prefix (hashes are long; prefix is convenient).
func (s *Store) GetNode(ctx context.Context, id string) (model.Node, error) {
	var n model.Node
	err := s.Pool.QueryRow(ctx,
		`SELECT id, kind, role, COALESCE(source_uri,''), created_at, updated_at
		 FROM node WHERE id = $1 OR id LIKE $2 LIMIT 1`,
		id, id+"%").
		Scan(&n.ID, &n.Kind, &n.Role, &n.SourceURI, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

// LatestVersion returns the most recent version row for a node (without the embedding bytes).
func (s *Store) LatestVersion(ctx context.Context, nodeID string) (model.NodeVersion, error) {
	var v model.NodeVersion
	err := s.Pool.QueryRow(ctx,
		`SELECT node_id, version, ingested_at,
		        COALESCE(snapshot_path,''), COALESCE(markdown,''), COALESCE(embed_model,'')
		 FROM node_version WHERE node_id = $1 ORDER BY version DESC LIMIT 1`, nodeID).
		Scan(&v.NodeID, &v.Version, &v.IngestedAt, &v.SnapshotPath, &v.Markdown, &v.EmbedModel)
	return v, err
}

// ListNodes returns recent nodes (for verification/UI).
func (s *Store) ListNodes(ctx context.Context, limit int) ([]model.Node, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, kind, role, COALESCE(source_uri,''), created_at, updated_at
		 FROM node ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var n model.Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Role, &n.SourceURI, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ContentRow is a node plus its latest version's markdown — what materialize needs.
type ContentRow struct {
	ID, Kind, Role, SourceURI, Markdown string
}

// AllContents returns every source/derived node with its latest-version markdown,
// for full-mirror materialization (zyme sync). pending/archived are excluded.
func (s *Store) AllContents(ctx context.Context) ([]ContentRow, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT DISTINCT ON (n.id) n.id, n.kind, n.role, COALESCE(n.source_uri,''), COALESCE(v.markdown,'')
		 FROM node n JOIN node_version v ON v.node_id = n.id
		 WHERE n.role IN ('source','derived')
		 ORDER BY n.id, v.version DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContentRow
	for rows.Next() {
		var r ContentRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.Role, &r.SourceURI, &r.Markdown); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// vectorLiteral formats a slice as a pgvector text literal, e.g. [0.1,0.2,0.3].
// Avoids needing the pgvector-go type registration; Postgres casts text -> vector.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 8)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", f)
	}
	b.WriteByte(']')
	return b.String()
}

// nullable returns nil for empty strings so Postgres stores NULL, not ''.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
