// Package model holds the shared domain types for the node pool.
// Plain names only — see the naming convention.
package model

import "time"

// Role controls a node's participation in the system.
// pending and archived nodes are excluded from clustering and from derive input.
type Role string

const (
	RoleSource   Role = "source"   // ingested by a human or adapter (ground truth)
	RoleDerived  Role = "derived"  // fermentation product, approved, in pool
	RolePending  Role = "pending"  // fermentation candidate awaiting approval
	RoleArchived Role = "archived" // rejected (soft-deleted), kept for record, out of pool
)

type Kind string

const (
	KindIdea Kind = "idea"
	KindPage Kind = "page"
	KindLink Kind = "link"
	KindStar Kind = "star"
	KindFeed Kind = "feed"
)

// Node is one entry in the flat content pool. Identity is stable across versions.
type Node struct {
	ID        string
	Kind      Kind
	Role      Role
	SourceURI string // set for live nodes (star/feed); empty for frozen
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NodeVersion is one snapshot of a node's payload (Track A + Track B).
type NodeVersion struct {
	NodeID       string
	Version      int
	IngestedAt   time.Time
	SnapshotPath string // Track A
	Markdown     string // Track B cleaned text
	EmbedModel   string
	// Embedding is handled at the store layer (pgvector).
}

// Edge is a directed provenance link: FromNode begat ToNode.
type Edge struct {
	FromNode  string
	ToNode    string
	Kind      string // substrate | bridge | seed
	DeriveRun string
	CreatedAt time.Time
}
