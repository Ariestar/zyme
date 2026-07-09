// Command zyme is the Zyme CLI. Subcommands: migrate, ingest, nodes, materialize.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

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
	reg := ingest.NewRegistry(ingest.Web{}, ingest.RSS{})
	root.AddCommand(migrateCmd(), ingestCmd(reg), nodesCmd(), materializeCmd())
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
	c := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest a source via an adapter (web, rss): fetch -> extract -> embed -> store",
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

			payloads, err := a.Fetch(ctx, ingest.SourceRef{Adapter: adapter, URI: url})
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
					log.Printf("[%d/%d] stored: %s v%d — %s", i+1, len(payloads), short(id), ver, p.Title)
				}
			}
			log.Printf("done: %d stored, %d unchanged, %d failed", stored, unchanged, failed)
			return nil
		},
	}
	c.Flags().StringVar(&adapter, "adapter", "web", "source adapter: web, rss")
	c.Flags().StringVar(&url, "url", "", "source URL (required)")
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
			fmt.Printf("%-16s %-10s %-10s %s\n", "ID", "KIND", "ROLE", "SOURCE_URI")
			for _, n := range ns {
				fmt.Printf("%-16s %-10s %-10s %s\n", short(n.ID), n.Kind, n.Role, n.SourceURI)
			}
			return nil
		},
	}
}

func materializeCmd() *cobra.Command {
	var id string
	c := &cobra.Command{
		Use:   "materialize",
		Short: "Write a node's content as markdown into <vault>/_zyme/",
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
			path, err := materialize.WriteNode(cfg.VaultPath, n, v.Markdown)
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
