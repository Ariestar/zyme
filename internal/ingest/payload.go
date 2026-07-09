package ingest

import (
	"context"
	"time"
)

// IngestPayload is the standard contract every adapter emits. It is source-agnostic:
// downstream code (Pipeline.Save) never inspects which source produced it.
type IngestPayload struct {
	Identity      string    // resolved node ID (already normalized/hashed by adapter)
	IdentityBasis string    // "content-hash" | "guid" | "uri" — how Identity was derived
	Kind          string    // page | feed_item | star | image | ...
	SourceURI     string    // canonical source URI
	Title         string
	Markdown      string    // Track B: cleaned text fed to the embedder
	Snapshot      []byte    // Track A: raw payload (html/xml/json/pdf/image bytes)
	SnapshotMIME  string    // mime type of Snapshot
	FetchedAt     time.Time
	AdapterID     string
}

// SourceRef describes what an adapter should fetch.
type SourceRef struct {
	Adapter string
	URI     string
	Options map[string]string // adapter-specific (auth tokens, limits, ...)
}

// Adapter turns one source into one or more standard payloads.
type Adapter interface {
	ID() string
	Fetch(ctx context.Context, ref SourceRef) ([]IngestPayload, error)
}

// Registry maps adapter IDs to adapters.
type Registry struct {
	items map[string]Adapter
}

func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{items: map[string]Adapter{}}
	for _, a := range adapters {
		r.items[a.ID()] = a
	}
	return r
}

func (r *Registry) Get(id string) (Adapter, bool) {
	a, ok := r.items[id]
	return a, ok
}
