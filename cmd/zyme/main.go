// Command zyme is the Zyme CLI. Subcommands: migrate, ingest, nodes, sync, materialize.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"zyme/internal/config"
	"zyme/internal/embed"
	"zyme/internal/ingest"
	"zyme/internal/materialize"
	"zyme/internal/store"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "zyme",
		Short: "Zyme — parasitic data middleware",
	}
	reg := ingest.NewRegistry(ingest.Web{}, ingest.RSS{}, ingest.GitHub{})
	root.AddCommand(migrateCmd(), ingestCmd(reg), nodesCmd(), syncCmd(), materializeCmd())
	return root
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply the Postgres schema",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := context.Background()
			s, err := store.Open(ctx, cfg.PostgresDSN)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Migrate(ctx); err != nil {
				return err
			}
			fmt.Println("schema applied")
			return nil
		},
	}
}

func ingestCmd(reg *ingest.Registry) *cobra.Command {
	var adapter, url string
	var limit int
	c := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest a source via an adapter (web, rss, github): fetch -> extract -> embed -> store",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			a, ok := reg.Get(adapter)
			if !ok {
				return fmt.Errorf("unknown adapter %q", adapter)
			}
			ctx := context.Background()
			s, err := store.Open(ctx, cfg.PostgresDSN)
			if err != nil {
				return err
			}
			defer s.Close()
			pl := &ingest.Pipeline{
				Store:       s,
				Embed:       embed.New(cfg.OllamaURL, cfg.EmbedModel),
				SnapshotDir: cfg.SnapshotDir,
			}

			ref := ingest.SourceRef{Adapter: adapter, URI: url}
			if limit > 0 {
				ref.Options = map[string]string{"limit": strconv.Itoa(limit)}
			}
			payloads, err := a.Fetch(ctx, ref)
			if err != nil {
				return err
			}
			log.Printf("adapter %q returned %d payload(s)", adapter, len(payloads))

			var stored, unchanged, failed int
			for i, p := range payloads {
				id, ver, skipped, err := pl.Save(ctx, p)
				if err != nil {
					failed++
					log.Printf("[%d/%d] save error: %v", i+1, len(payloads), err)
					continue
				}
				if skipped {
					unchanged++
					log.Printf("[%d/%d] unchanged: %s (v%d)", i+1, len(payloads), short(id), ver)
				} else {
					stored++
					// Auto-materialize: every stored node lands in the vault too.
					// pool and vault are always in sync — the vault is the user-visible face.
					if cfg.VaultPath != "" {
						if _, merr := materialize.WriteNode(cfg.VaultPath, id, p.Kind, "source", p.SourceURI, p.Markdown); merr != nil {
							log.Printf("[%d/%d] materialize error: %v", i+1, len(payloads), merr)
						}
					}
					log.Printf("[%d/%d] stored: %s v%d — %s", i+1, len(payloads), short(id), ver, p.Title)
				}
			}
			log.Printf("done: %d stored, %d unchanged, %d failed", stored, unchanged, failed)
			return nil
		},
	}
	c.Flags().StringVar(&adapter, "adapter", "web", "source adapter: web, rss, github")
	c.Flags().StringVar(&url, "url", "", "source URL or target (e.g. 'starred' for github)")
	c.Flags().IntVar(&limit, "limit", 0, "max items to ingest (0 = all)")
	_ = c.MarkFlagRequired("url")
	return c
}

func nodesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "nodes",
		Short: "List nodes in the pool (recent first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := context.Background()
			s, err := store.Open(ctx, cfg.PostgresDSN)
			if err != nil {
				return err
			}
			defer s.Close()
			ns, err := s.ListNodes(ctx, 100)
			if err != nil {
				return err
			}
			if len(ns) == 0 {
				fmt.Println("(no nodes)")
				return nil
			}
			fmt.Printf("%-14s %-10s %-10s %s\n", "ID", "KIND", "ROLE", "SOURCE_URI")
			for _, n := range ns {
				fmt.Printf("%-14s %-10s %-10s %s\n", short(n.ID), n.Kind, n.Role, n.SourceURI)
			}
			return nil
		},
	}
}

func syncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Materialize all source/derived nodes into <vault>/_zyme/",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.VaultPath == "" {
				return fmt.Errorf("ZYME_VAULT is not set")
			}
			ctx := context.Background()
			s, err := store.Open(ctx, cfg.PostgresDSN)
			if err != nil {
				return err
			}
			defer s.Close()
			rows, err := s.AllContents(ctx)
			if err != nil {
				return err
			}
			n := 0
			for _, r := range rows {
				if _, err := materialize.WriteNode(cfg.VaultPath, r.ID, r.Kind, r.Role, r.SourceURI, r.Markdown); err != nil {
					log.Printf("skip %s: %v", short(r.ID), err)
					continue
				}
				n++
			}
			log.Printf("synced %d/%d nodes -> %s/_zyme/", n, len(rows), cfg.VaultPath)
			return nil
		},
	}
}

func materializeCmd() *cobra.Command {
	var id string
	c := &cobra.Command{
		Use:   "materialize",
		Short: "Write one node's content as markdown into <vault>/_zyme/",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.VaultPath == "" {
				return fmt.Errorf("ZYME_VAULT is not set")
			}
			ctx := context.Background()
			s, err := store.Open(ctx, cfg.PostgresDSN)
			if err != nil {
				return err
			}
			defer s.Close()
			n, err := s.GetNode(ctx, id)
			if err != nil {
				return fmt.Errorf("get node: %w", err)
			}
			v, err := s.LatestVersion(ctx, n.ID)
			if err != nil {
				return fmt.Errorf("latest version: %w", err)
			}
			path, err := materialize.WriteNode(cfg.VaultPath, n.ID, string(n.Kind), string(n.Role), n.SourceURI, v.Markdown)
			if err != nil {
				return err
			}
			log.Printf("materialized node %s -> %s", n.ID, path)
			return nil
		},
	}
	c.Flags().StringVar(&id, "id", "", "node id (or unique prefix) to materialize (required)")
	_ = c.MarkFlagRequired("id")
	return c
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
