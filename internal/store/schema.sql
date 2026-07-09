-- Zyme schema. Idempotent (IF NOT EXISTS). Applied by `zyme migrate`.

CREATE EXTENSION IF NOT EXISTS vector;

-- Flat pool of content nodes. No hierarchy, no parent_id.
-- id: content-hash for frozen nodes, canonical source_uri for live nodes.
CREATE TABLE IF NOT EXISTS node (
    id          text PRIMARY KEY,
    kind        text NOT NULL,                       -- idea | page | link | image | star | feed | app
    role        text NOT NULL DEFAULT 'source',      -- source | derived | pending | archived
    source_uri  text,                                -- set for live nodes (star/feed); NULL for frozen
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Per-version payload. Frozen node = 1 row; live node = appended over time.
-- Track A = snapshot_path (blob on disk); Track B = markdown + embedding.
CREATE TABLE IF NOT EXISTS node_version (
    node_id       text NOT NULL REFERENCES node(id) ON DELETE CASCADE,
    version       int  NOT NULL,
    ingested_at   timestamptz NOT NULL DEFAULT now(),
    snapshot_path text,                  -- Track A: content-addressed blob filename
    markdown      text,                  -- Track B: cleaned text
    embedding     vector(1024),          -- Track B: must match ZYME_EMBED_MODEL dim (bge-m3 = 1024)
    embed_model   text,
    PRIMARY KEY (node_id, version)
);

-- Directed provenance: from_node (precursor) -> to_node (product).
-- "begat". A product can have many precursors (multi-substrate reaction).
CREATE TABLE IF NOT EXISTS edge (
    from_node   text NOT NULL REFERENCES node(id) ON DELETE CASCADE,
    to_node     text NOT NULL REFERENCES node(id) ON DELETE CASCADE,
    kind        text NOT NULL,           -- substrate | bridge | seed
    derive_run  text,                    -- which derive pass produced this edge
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (from_node, to_node, kind)
);

-- Review state for derived candidates awaiting user approval.
-- role=pending on node keeps them out of the pool; this table tracks the decision.
CREATE TABLE IF NOT EXISTS review (
    node_id     text PRIMARY KEY REFERENCES node(id) ON DELETE CASCADE,
    proposed_at timestamptz NOT NULL DEFAULT now(),
    decided_at  timestamptz,
    decision    text                     -- NULL = pending, 'approved', 'rejected' (soft-delete -> archived)
);

-- Stateless projection coords. Computed periodically by re-cluster.
-- Only role IN (source, derived) get coords (pending/archived excluded).
CREATE TABLE IF NOT EXISTS node_coord (
    node_id    text PRIMARY KEY REFERENCES node(id) ON DELETE CASCADE,
    x          double precision,
    y          double precision,
    cluster_id int,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_node_role      ON node(role);
CREATE INDEX IF NOT EXISTS idx_node_kind      ON node(kind);
CREATE INDEX IF NOT EXISTS idx_edge_from      ON edge(from_node);
CREATE INDEX IF NOT EXISTS idx_edge_to        ON edge(to_node);

-- Add HNSW index here later when node count grows past ~100k:
-- CREATE INDEX IF NOT EXISTS idx_nv_embedding ON node_version USING hnsw (embedding vector_cosine_ops);
