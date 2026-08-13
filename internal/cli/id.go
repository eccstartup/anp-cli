package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/identity"
	"github.com/eccstartup/anp-cli/internal/output"
)

func (a *App) runIDShow(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	active, err := a.activeIdentity()
	if err != nil {
		return output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` to generate an identity.")
	}
	return a.renderSuccess(cmd.CommandPath(), format, identity.PublicView(active), "Active identity", nil)
}

func (a *App) runWhoami(cmd *Command, args []string) error {
	active, err := a.activeIdentity()
	if err != nil {
		return output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` to generate an identity.")
	}
	format := a.outputFormat(true)
	return a.renderSuccess(cmd.CommandPath(), format, identity.PublicView(active), "Active identity", nil)
}

func (a *App) runIDList(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	service, _, err := a.identityService()
	if err != nil {
		return err
	}
	items, err := service.List()
	if err != nil {
		return err
	}
	current, _ := service.Store.CurrentName()
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rows = append(rows, map[string]any{
			"name":       item.Name,
			"did":        item.DID,
			"handle":     item.Handle,
			"created_at": item.CreatedAt,
			"current":    item.Name == current,
		})
	}
	data := map[string]any{"identities": rows, "current": current}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("%d identit%s", len(rows), plural(len(rows))), nil)
}

func (a *App) runIDCurrent(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	service, _, err := a.identityService()
	if err != nil {
		return err
	}
	name, err := service.Store.CurrentName()
	if err != nil {
		return err
	}
	active, err := service.Store.Load(name)
	if err != nil {
		return err
	}
	data := map[string]any{"name": name, "did": active.DID, "handle": active.Handle}
	return a.renderSuccess(cmd.CommandPath(), format, data, "Default identity", nil)
}

func (a *App) runIDUse(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return output.NewExitError("invalid_argument", 2, "use requires an identity name.", "Run `anp-cli id list` to see available identities.")
	}
	name := strings.TrimSpace(args[0])
	service, resolved, err := a.identityService()
	if err != nil {
		return err
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"identity": name, "actions": []string{"persist default identity in config.yaml and identity index"}}, "Switch identity plan")
	}
	if err := service.Store.SetCurrent(name); err != nil {
		return output.NewExitError("not_found", 5, err.Error(), "Run `anp-cli id list` to see available identities.")
	}
	// Persist the default in config.yaml so it wins over the identity index
	// across commands, preserving existing backend/did_domain settings.
	if err := config.UpdateFile(resolved.Paths.ConfigFile, func(f *config.File) error {
		f.Identity = name
		return nil
	}); err != nil {
		return err
	}
	data := map[string]any{"name": name}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("Default identity is now %s", name), nil)
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func (a *App) runIDResolve(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	service, resolved, err := a.identityService()
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return output.NewExitError("invalid_argument", 2, "resolve requires a DID or handle argument.", "Run `anp-cli id resolve <did|handle>`.")
	}
	target := strings.TrimSpace(args[0])
	if a.globals.DryRun {
		plan := map[string]any{"target": target, "actions": []string{"resolve DID or WNS handle to a DID document"}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "Resolve plan")
	}
	doc, err := service.Resolve(context.Background(), resolved, target)
	if err != nil {
		return err
	}
	data := map[string]any{"target": target, "did": doc["id"], "did_document": doc}
	return a.renderSuccess(cmd.CommandPath(), format, data, "Resolved", nil)
}

func (a *App) runIDRegister(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	return a.registerHandle(cmd, format, false)
}

func (a *App) runRegister(cmd *Command, args []string) error {
	format := a.outputFormat(true)
	return a.registerHandle(cmd, format, true)
}

func (a *App) registerHandle(cmd *Command, format output.Format, shortcut bool) error {
	handle, _ := cmd.Flags().GetString("handle")
	phone, _ := cmd.Flags().GetString("phone")
	email, _ := cmd.Flags().GetString("email")
	otp, _ := cmd.Flags().GetString("otp")
	if strings.TrimSpace(handle) == "" {
		return output.NewExitError("invalid_argument", 2, "--handle is required.", "Run `anp-cli id register --handle <h> [--phone|--email]`.")
	}
	service, resolved, err := a.identityService()
	if err != nil {
		return err
	}
	active, err := service.Store.Load(resolved.ActiveIdentity)
	if err != nil {
		return output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` first.")
	}
	plan := map[string]any{
		"handle":  handle,
		"did":     active.DID,
		"actions": []string{"register WNS handle with backend", "bind handle to local identity"},
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Handle registration plan")
	}
	result, err := service.RegisterHandle(context.Background(), resolved, active, handle, phone, email, otp)
	if err != nil {
		return friendlyHandleError(handle, err)
	}
	data := map[string]any{"handle": handle, "did": active.DID, "result": result}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("Handle %s registered", handle), nil)
}

// friendlyHandleError maps a backend "handle already taken" conflict into a
// clear exit error with alternative suggestions.
func friendlyHandleError(handle string, err error) error {
	if !isHandleTaken(err) {
		return err
	}
	hint := fmt.Sprintf(
		"Handle %q is a namespace: try a variant like %s, or host your own domain via `anp-cli config set --did-domain <your-domain>` and register there.",
		handle,
		strings.Join(suggestHandles(handle, 3), ", "),
	)
	return output.NewExitError("handle_taken", 4, fmt.Sprintf("handle %q is already registered", handle), hint)
}

func isHandleTaken(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"already registered", "already taken", "is taken", "reserved", "in use", "handle_taken"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// suggestHandles generates alternative localparts for a squatted handle.
func suggestHandles(base string, count int) []string {
	base = strings.TrimSpace(base)
	if base == "" || count <= 0 {
		return nil
	}
	seeds := []string{"1", "2", "me", "dev", "real"}
	out := make([]string, 0, count)
	for _, seed := range seeds {
		if len(out) >= count {
			break
		}
		out = append(out, base+"."+seed)
	}
	return out
}

func (a *App) runIDRecover(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	handle, _ := cmd.Flags().GetString("handle")
	phone, _ := cmd.Flags().GetString("phone")
	email, _ := cmd.Flags().GetString("email")
	otp, _ := cmd.Flags().GetString("otp")
	if strings.TrimSpace(handle) == "" {
		return output.NewExitError("invalid_argument", 2, "--handle is required.", "Run `anp-cli id recover --handle <h> [--phone|--email]`.")
	}
	service, resolved, err := a.identityService()
	if err != nil {
		return err
	}
	active, err := service.Store.Load(resolved.ActiveIdentity)
	if err != nil {
		return output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` first.")
	}
	if a.globals.DryRun {
		plan := map[string]any{"handle": handle, "actions": []string{"recover handle binding with backend"}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "Handle recovery plan")
	}
	result, err := service.RecoverHandle(context.Background(), resolved, active, handle, phone, email, otp)
	if err != nil {
		return err
	}
	data := map[string]any{"handle": handle, "did": active.DID, "result": result}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("Handle %s recovered", handle), nil)
}
