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
	"zyme/internal/model"
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
	root.AddCommand(migrateCmd(), ingestCmd(), nodesCmd(), materializeCmd())
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

func ingestCmd() *cobra.Command {
	var url string
	c := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest a URL: fetch -> extract text -> embed -> store node",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := context.Background()

			res, err := ingest.FetchWeb(ctx, url, cfg.SnapshotDir)
			if err != nil {
				return err
			}
			log.Printf("fetched: %q (%s) -> %s", res.Title, res.URL, res.NodeID)

			emb, err := embed.New(cfg.OllamaURL, cfg.EmbedModel).Embed(ctx, res.Markdown)
			if err != nil {
				return err
			}
			log.Printf("embedded: %d-dim via %s", len(emb), cfg.EmbedModel)

			s, err := store.Open(ctx, cfg.PostgresDSN)
			if err != nil {
				return err
			}
			defer s.Close()

			n := model.Node{
				ID: res.NodeID, Kind: model.KindPage, Role: model.RoleSource, SourceURI: res.URL,
			}
			if err := s.InsertNode(ctx, n); err != nil {
				return fmt.Errorf("insert node: %w", err)
			}
			v, err := s.CurrentVersion(ctx, res.NodeID)
			if err != nil {
				return fmt.Errorf("current version: %w", err)
			}
			if err := s.InsertVersion(ctx, res.NodeID, v+1, res.Markdown, res.SnapshotKey, cfg.EmbedModel, emb); err != nil {
				return fmt.Errorf("insert version: %w", err)
			}
			log.Printf("stored: node %s version %d", res.NodeID, v+1)
			return nil
		},
	}
	c.Flags().StringVar(&url, "url", "", "URL to ingest (required)")
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
			fmt.Printf("%-16s %-8s %-10s %s\n", "ID", "KIND", "ROLE", "SOURCE_URI")
			for _, n := range ns {
				id := n.ID
				if len(id) > 16 {
					id = id[:16]
				}
				fmt.Printf("%-16s %-8s %-10s %s\n", id, n.Kind, n.Role, n.SourceURI)
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
