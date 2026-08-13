package cli

import (
	"context"
	"os"
	"strings"

	"github.com/eccstartup/anp-cli/internal/output"
	"github.com/eccstartup/anp-cli/internal/proof"
)

func (a *App) runProofSign(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	path := ""
	if len(args) > 0 {
		path = strings.TrimSpace(args[0])
	}
	if path == "" {
		path, _ = cmd.Flags().GetString("file")
	}
	outputPath, _ := cmd.Flags().GetString("output")
	if path == "" {
		return output.NewExitError("invalid_argument", 2, "sign requires a file.", "Run `anp-cli proof sign <file>`.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	active, err := a.activeIdentity()
	if err != nil {
		return output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` first.")
	}
	plan := map[string]any{"file": path, "signer_did": active.DID, "actions": []string{"sign file bytes with Ed25519 key-1"}}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Sign plan")
	}
	signed, err := proof.Sign(active, data)
	if err != nil {
		return err
	}
	if outputPath != "" {
		raw, _ := jsonMarshalIndent(signed)
		if err := os.WriteFile(outputPath, raw, 0o600); err != nil {
			return err
		}
		return a.renderSuccess(cmd.CommandPath(), format, map[string]any{"file": path, "proof_file": outputPath}, "Signature written", nil)
	}
	return a.renderSuccess(cmd.CommandPath(), format, signed, "Signed", nil)
}

func (a *App) runProofVerify(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	path := ""
	if len(args) > 0 {
		path = strings.TrimSpace(args[0])
	}
	if path == "" {
		path, _ = cmd.Flags().GetString("file")
	}
	signatureArg, _ := cmd.Flags().GetString("signature")
	did, _ := cmd.Flags().GetString("did")
	if path == "" {
		return output.NewExitError("invalid_argument", 2, "verify requires a file.", "Run `anp-cli proof verify <file> --signature <hex|proof.json>`.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	signatureHex := strings.TrimSpace(signatureArg)
	if signatureHex == "" {
		return output.NewExitError("invalid_argument", 2, "--signature is required.", "Pass the hex signature, or a path to a proof JSON written by `anp-cli proof sign --output`.")
	}
	// Accept a path to a proof file in place of a raw hex signature.
	if _, statErr := os.Stat(signatureArg); statErr == nil && strings.HasSuffix(signatureArg, ".json") {
		saved, err := proof.ParseProofFile(signatureArg)
		if err != nil {
			return err
		}
		signatureHex = saved.Signature
		if did == "" {
			did = saved.SignerDID
		}
	}
	active, _ := a.activeIdentity()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"file": path, "did": did, "actions": []string{"verify signature against the signer's key"}}, "Verify plan")
	}
	result, err := proof.Verify(context.Background(), active, did, data, signatureHex)
	if err != nil {
		return err
	}
	if !result.Valid {
		return output.NewExitError("verification_failed", 6, "signature verification failed.", "The file or signature does not match the signer's key.")
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Signature is valid", nil)
}
