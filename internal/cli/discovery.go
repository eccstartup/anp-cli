package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/eccstartup/anp-cli/internal/discovery"
	"github.com/eccstartup/anp-cli/internal/output"
	"github.com/eccstartup/anp-cli/internal/store"
)

func (a *App) openDiscoveryDB() (*store.DB, func(), error) {
	resolved, err := a.resolveConfig()
	if err != nil {
		return nil, nil, err
	}
	db, err := a.openDB(resolved)
	if err != nil {
		return nil, nil, err
	}
	return db, func() { db.Close() }, nil
}

func (a *App) runDiscoveryCrawl(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	target := ""
	if len(args) > 0 {
		target = strings.TrimSpace(args[0])
	}
	if target == "" {
		target, _ = cmd.Flags().GetString("url")
	}
	if target == "" {
		return output.NewExitError("invalid_argument", 2, "crawl requires a URL.", "Run `anp-cli discovery crawl <url>`.")
	}
	db, closeDB, err := a.openDiscoveryDB()
	if err != nil {
		return err
	}
	defer closeDB()
	plan := map[string]any{"url": target, "actions": []string{"fetch ad.json", "fetch interface.json", "index agent locally"}}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Crawl plan")
	}
	result, err := discovery.Crawl(context.Background(), db, target)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Agent indexed", nil)
}

func (a *App) runDiscoverySearch(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	query := ""
	if len(args) > 0 {
		query = strings.TrimSpace(args[0])
	}
	if query == "" {
		query, _ = cmd.Flags().GetString("query")
	}
	limit, _ := cmd.Flags().GetInt("limit")
	if query == "" {
		return output.NewExitError("invalid_argument", 2, "search requires a query.", "Run `anp-cli discovery search <query>`.")
	}
	db, closeDB, err := a.openDiscoveryDB()
	if err != nil {
		return err
	}
	defer closeDB()
	agents, err := discovery.Search(context.Background(), db, query, limit)
	if err != nil {
		return err
	}
	data := map[string]any{"query": query, "agents": agents}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("%d result(s)", len(agents)), nil)
}
