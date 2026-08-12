package cli

import (
	"context"

	"github.com/ANPWorld/anp-cli/internal/e2ee"
	"github.com/ANPWorld/anp-cli/internal/output"
)

func (a *App) e2eeService() (*e2ee.Service, func(), error) {
	resolved, err := a.resolveConfig()
	if err != nil {
		return nil, nil, err
	}
	active, err := a.activeIdentity()
	if err != nil {
		return nil, nil, output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` first.")
	}
	client, err := a.signedClient(resolved, active)
	if err != nil {
		return nil, nil, err
	}
	svc, err := e2ee.NewService(context.Background(), resolved, active, client)
	if err != nil {
		return nil, nil, err
	}
	return svc, func() {}, nil
}

func (a *App) runE2EEInit(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	service, resolved, err := a.identityService()
	if err != nil {
		return err
	}
	active, err := service.Store.Load(resolved.ActiveIdentity)
	if err != nil {
		return output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` first.")
	}
	client, err := a.signedClient(resolved, active)
	if err != nil {
		return err
	}
	plan := map[string]any{"did": active.DID, "actions": []string{"register did document", "publish signed prekey bundle and one-time prekeys"}}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "E2EE init plan")
	}
	// Register the DID document then publish the prekey bundle.
	if _, err := client.CallRaw(context.Background(), "did.register_document", map[string]any{
		"did": active.DID, "did_document": active.DIDDocument,
	}); err != nil {
		return err
	}
	svc, err := e2ee.NewService(context.Background(), resolved, active, client)
	if err != nil {
		return err
	}
	result, err := svc.PublishPrekeyBundle()
	if err != nil {
		return err
	}
	data := map[string]any{"did": active.DID, "published": result}
	return a.renderSuccess(cmd.CommandPath(), format, data, "E2EE prekey bundle published", nil)
}

func (a *App) runE2EEStatus(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	peer, _ := cmd.Flags().GetString("with")
	if peer == "" {
		return output.NewExitError("invalid_argument", 2, "--with is required.", "Run `anp-cli e2ee status --with <did>`.")
	}
	svc, closeFn, err := a.e2eeService()
	if err != nil {
		return err
	}
	defer closeFn()
	exists, sessionID, err := svc.SessionStatus(peer)
	if err != nil {
		return err
	}
	data := map[string]any{"with": peer, "session_exists": exists}
	if exists {
		data["session_id"] = sessionID
	}
	return a.renderSuccess(cmd.CommandPath(), format, data, "E2EE status", nil)
}
