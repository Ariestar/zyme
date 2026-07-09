// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	PostgresDSN string
	OllamaURL   string
	EmbedModel  string
	EmbedDim    int
	VaultPath   string
	SnapshotDir string
}

func Load() (Config, error) {
	c := Config{
		PostgresDSN: os.Getenv("ZYME_PG_DSN"),
		OllamaURL:   envOr("ZYME_OLLAMA_URL", "http://localhost:11434"),
		EmbedModel:  envOr("ZYME_EMBED_MODEL", "bge-m3"),
		EmbedDim:    1024,
		VaultPath:   os.Getenv("ZYME_VAULT"),
		SnapshotDir: envOr("ZYME_SNAPSHOT_DIR", "./data/snapshots"),
	}
	if c.PostgresDSN == "" {
		return c, fmt.Errorf("ZYME_PG_DSN is not set")
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
