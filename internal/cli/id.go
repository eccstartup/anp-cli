package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ANPWorld/anp-cli/internal/identity"
	"github.com/ANPWorld/anp-cli/internal/output"
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
	active, err := service.Active()
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
		return err
	}
	data := map[string]any{"handle": handle, "did": active.DID, "result": result}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("Handle %s registered", handle), nil)
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
	active, err := service.Active()
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
