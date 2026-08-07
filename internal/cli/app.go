// Package cli wires the command layer: global flags, config resolution, error
// handling, and the unified output envelope.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ANPWorld/anp-cli/internal/buildinfo"
	"github.com/ANPWorld/anp-cli/internal/cmdmeta"
	appconfig "github.com/ANPWorld/anp-cli/internal/config"
	"github.com/ANPWorld/anp-cli/internal/identity"
	"github.com/ANPWorld/anp-cli/internal/output"
	"github.com/ANPWorld/anp-cli/internal/store"
	"github.com/ANPWorld/anp-cli/internal/transport"
)

type GlobalOptions struct {
	Format          string
	FormatChanged   bool
	JSON            bool
	JQ              string
	DryRun          bool
	Yes             bool
	Identity        string
	IdentityChanged bool
}

type App struct {
	globals GlobalOptions
	catalog *cmdmeta.Catalog
}

func Execute() int {
	app := &App{
		globals: GlobalOptions{Format: string(output.FormatJSON)},
		catalog: cmdmeta.NewCatalog(),
	}
	rootCmd := newRootCommand(app)
	rootCmd.SetContext(context.Background())
	if err := rootCmd.Execute(); err != nil {
		return app.handleError(err)
	}
	return 0
}

func (a *App) handleError(err error) int {
	format := output.FormatJSON
	if resolved, resolveErr := output.NormalizeFormat(a.globals.Format); resolveErr == nil {
		format = resolved
	}
	detail := output.ErrorDetail{
		Code:      "internal_error",
		Message:   err.Error(),
		Retryable: false,
	}
	exitCode := 1
	var exitErr *output.ExitError
	if errors.As(err, &exitErr) {
		detail = exitErr.Detail
		exitCode = exitErr.Code
	}
	envelope := output.ErrorEnvelope{
		OK:    false,
		Error: detail,
		Meta: output.Meta{
			Version: buildinfo.Version,
			DryRun:  a.globals.DryRun,
			Format:  string(format),
		},
	}
	if identityMeta := a.identityMeta(); identityMeta != nil {
		envelope.Meta.Identity = identityMeta
	}
	if renderErr := output.RenderError(os.Stderr, format, a.globals.JQ, envelope); renderErr != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	}
	return exitCode
}

func (a *App) renderSuccess(command string, format output.Format, data any, summary string, warnings []string) error {
	envelope := output.SuccessEnvelope{
		OK:       true,
		Command:  command,
		Data:     data,
		Warnings: warnings,
		Summary:  summary,
		Meta: output.Meta{
			Version:  buildinfo.Version,
			Identity: a.identityMeta(),
			DryRun:   a.globals.DryRun,
			Format:   string(format),
		},
	}
	return output.RenderSuccess(os.Stdout, format, a.globals.JQ, envelope)
}

// renderPlan renders the --dry-run execution plan in place of data.
func (a *App) renderPlan(command string, format output.Format, plan any, summary string) error {
	envelope := output.SuccessEnvelope{
		OK:      true,
		Command: command,
		Plan:    plan,
		Summary: summary,
		Meta: output.Meta{
			Version:  buildinfo.Version,
			Identity: a.identityMeta(),
			DryRun:   true,
			Format:   string(format),
		},
	}
	return output.RenderSuccess(os.Stdout, format, a.globals.JQ, envelope)
}

func (a *App) resolveConfig() (*appconfig.Resolved, error) {
	return appconfig.Resolve(appconfig.Overrides{
		Identity:        a.globals.Identity,
		IdentityChanged: a.globals.IdentityChanged,
		Format:          a.globals.Format,
		FormatChanged:   a.globals.FormatChanged,
	})
}

func (a *App) identityService() (*identity.Service, *appconfig.Resolved, error) {
	resolved, err := a.resolveConfig()
	if err != nil {
		return nil, nil, err
	}
	return identity.NewService(resolved.Paths.IdentityDir), resolved, nil
}

// openDB opens the workspace database, creating the file if needed.
func (a *App) openDB(resolved *appconfig.Resolved) (*store.DB, error) {
	return store.Open(resolved.Paths.DatabaseFile)
}

func (a *App) activeIdentity() (*identity.Identity, error) {
	service, _, err := a.identityService()
	if err != nil {
		return nil, err
	}
	active, err := service.Active()
	if err != nil {
		return nil, err
	}
	return active, nil
}

func (a *App) signedClient(resolved *appconfig.Resolved, active *identity.Identity) (*transport.Client, error) {
	if resolved == nil || active == nil {
		return nil, fmt.Errorf("no active identity; run `anp-cli init` first")
	}
	if resolved.Backend == "" {
		return nil, fmt.Errorf("no backend configured; set ANP_BACKEND or `anp-cli config set --backend <url>`")
	}
	signer, err := identity.SignerFor(active)
	if err != nil {
		return nil, err
	}
	return transport.NewClient(resolved.Backend, signer), nil
}

func (a *App) identityMeta() *output.IdentityMeta {
	if a.globals.Identity != "" {
		return &output.IdentityMeta{Name: a.globals.Identity}
	}
	return nil
}

// outputFormat resolves the effective format. Canonical commands default to
// json; shortcut commands default to pretty unless the user chose explicitly.
func (a *App) outputFormat(shortcut bool) output.Format {
	if a.globals.FormatChanged || a.globals.JSON {
		format, err := output.NormalizeFormat(a.globals.Format)
		if err != nil {
			return output.FormatJSON
		}
		return format
	}
	if shortcut {
		return output.FormatPretty
	}
	return output.FormatJSON
}

func normalizedFormat(raw string) output.Format {
	format, err := output.NormalizeFormat(raw)
	if err != nil {
		return output.FormatJSON
	}
	return format
}
