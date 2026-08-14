package cli

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eccstartup/anp-cli/internal/attachment"
	"github.com/eccstartup/anp-cli/internal/message"
	"github.com/eccstartup/anp-cli/internal/output"
)

// attachmentService assembles the attachment control-plane service plus the
// message service (for manifest delivery) from resolved config + identity +
// signed client.
func (a *App) attachmentDeps() (*attachment.Service, *message.Service, func(), error) {
	resolved, db, active, client, closeDB, err := a.wireDeps()
	if err != nil {
		return nil, nil, nil, err
	}
	return attachment.NewService(resolved, active, client), message.NewService(resolved, db, active, client), closeDB, nil
}

func (a *App) runAttachSend(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	to, _ := cmd.Flags().GetString("to")
	file, _ := cmd.Flags().GetString("file")
	caption, _ := cmd.Flags().GetString("text")
	mimeType, _ := cmd.Flags().GetString("mime")
	if strings.TrimSpace(to) == "" {
		return output.NewExitError("invalid_argument", 2, "--to is required.", "Run `anp-cli attach send --to <did> --file <path>`.")
	}
	if strings.TrimSpace(file) == "" {
		return output.NewExitError("invalid_argument", 2, "--file is required.", "Run `anp-cli attach send --to <did> --file <path>`.")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return output.NewExitError("invalid_argument", 2, fmt.Sprintf("read file: %v", err), "Provide a readable file path.")
	}

	attSvc, msgSvc, closeDB, err := a.attachmentDeps()
	if err != nil {
		return err
	}
	defer closeDB()

	detectedMIME := mimeType
	if detectedMIME == "" {
		detectedMIME = mime.TypeByExtension(filepath.Ext(file))
	}
	if detectedMIME == "" {
		detectedMIME = "application/octet-stream"
	}
	digest := attachment.SHA256B64U(data)
	attachmentID := attachment.NewAttachmentID()
	plan := map[string]any{
		"to":        to,
		"file":      file,
		"size":      len(data),
		"mime_type": detectedMIME,
		"digest":    "sha-256:" + digest,
		"caption":   caption,
		"actions":   []string{"create upload slot via attachment.create_slot", "upload bytes via HTTPS PUT", "commit via attachment.commit_object", "send attachment_manifest via direct.send"},
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Attachment send plan")
	}

	ctx := context.Background()
	slot, err := attSvc.CreateSlot(ctx, attachment.CreateSlotRequest{
		AttachmentID:   attachmentID,
		ExpectedSize:   int64(len(data)),
		MimeType:       detectedMIME,
		Filename:       filepath.Base(file),
		ExpectedDigest: map[string]any{"alg": "sha-256", "value_b64u": digest},
	})
	if err != nil {
		return err
	}
	uploadURI, _ := slot["upload_uri"].(string)
	slotID, _ := slot["slot_id"].(string)
	commitToken, _ := slot["commit_token"].(string)
	objectURI, _ := slot["object_uri"].(string)
	if uploadURI == "" || slotID == "" || commitToken == "" {
		return output.NewExitError("backend_error", 1, "attachment.create_slot returned an incomplete slot", "The backend must return upload_uri, slot_id, and commit_token.")
	}
	if err := attachment.Upload(ctx, uploadURI, data); err != nil {
		// Best-effort abort on upload failure.
		_, _ = attSvc.AbortObject(ctx, attachmentID, slotID)
		return err
	}
	if _, err := attSvc.CommitObject(ctx, attachment.CommitRequest{
		AttachmentID: attachmentID,
		SlotID:       slotID,
		CommitToken:  commitToken,
		Size:         int64(len(data)),
		DigestValue:  digest,
	}); err != nil {
		return err
	}
	manifest := map[string]any{
		"attachments": []any{map[string]any{
			"attachment_id":   attachmentID,
			"mime_type":       detectedMIME,
			"size":            strconv.Itoa(len(data)),
			"digest":          map[string]any{"alg": "sha-256", "value_b64u": digest},
			"access_info":     map[string]any{"object_uri": objectURI},
			"encryption_info": map[string]any{"mode": attachment.EncryptionNone},
			"filename":        filepath.Base(file),
		}},
	}
	if caption != "" {
		manifest["caption"] = caption
	}
	sent, err := msgSvc.SendAttachmentManifest(ctx, to, manifest)
	if err != nil {
		return err
	}
	result := map[string]any{
		"attachment_id": attachmentID,
		"message_id":    sent.MessageID,
		"object_uri":    objectURI,
		"size":          len(data),
		"digest":        "sha-256:" + digest,
		"manifest":      manifest,
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Attachment sent", nil)
}

func (a *App) runAttachDownload(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	messageID, _ := cmd.Flags().GetString("message-id")
	outDir, _ := cmd.Flags().GetString("out")
	if strings.TrimSpace(messageID) == "" {
		return output.NewExitError("invalid_argument", 2, "--message-id is required.", "Run `anp-cli attach download --message-id <mid> [--out <dir>]`.")
	}
	attSvc, _, closeDB, err := a.attachmentDeps()
	if err != nil {
		return err
	}
	defer closeDB()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"message_id": messageID, "out": outDir, "actions": []string{"find attachment manifest in inbox", "get download ticket via attachment.get_download_ticket", "download via HTTPS GET", "verify size and sha-256", "write file"}}, "Attachment download plan")
	}

	ctx := context.Background()
	if err := attSvc.RegisterDocument(ctx); err != nil {
		return err
	}
	meta, body, err := a.findAttachmentMessage(ctx, attSvc, messageID)
	if err != nil {
		return err
	}
	manifest, err := extractManifest(body)
	if err != nil {
		return err
	}
	objectURI := ""
	if accessInfo, ok := manifest["access_info"].(map[string]any); ok {
		objectURI, _ = accessInfo["object_uri"].(string)
	}
	if objectURI == "" {
		return output.NewExitError("backend_error", 1, "manifest has no object_uri", "The attachment manifest must carry access_info.object_uri.")
	}
	size, err := strconv.ParseInt(fmt.Sprintf("%v", manifest["size"]), 10, 64)
	if err != nil {
		return output.NewExitError("backend_error", 1, "manifest size is invalid", "The attachment manifest must carry a decimal string size.")
	}
	digestValue := ""
	if digest, ok := manifest["digest"].(map[string]any); ok {
		digestValue, _ = digest["value_b64u"].(string)
	}
	securityProfile, _ := meta["security_profile"].(string)
	ticket, err := attSvc.GetDownloadTicket(ctx, attachment.DownloadTicketRequest{
		AttachmentID:           fmt.Sprintf("%v", manifest["attachment_id"]),
		ObjectURI:              objectURI,
		MessageID:              messageID,
		MessageSecurityProfile: securityProfile,
		MessageTargetDID:       attSvc.Active.DID,
	})
	if err != nil {
		return err
	}
	ticketB64U, _ := ticket["download_ticket_b64u"].(string)
	if ticketB64U == "" {
		return output.NewExitError("backend_error", 1, "attachment.get_download_ticket returned no ticket", "The backend must return download_ticket_b64u.")
	}
	data, err := attachment.Download(ctx, objectURI, ticketB64U, size, digestValue)
	if err != nil {
		return err
	}
	filename, _ := manifest["filename"].(string)
	if filename == "" {
		filename = fmt.Sprintf("%v", manifest["attachment_id"])
	}
	if outDir == "" {
		outDir = "."
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(outDir, filepath.Base(filename))
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, map[string]any{
		"attachment_id": manifest["attachment_id"],
		"message_id":    messageID,
		"path":          target,
		"size":          len(data),
		"digest":        "sha-256:" + digestValue,
	}, "Attachment downloaded", nil)
}

// findAttachmentMessage locates a message by its message_id in the backend
// inbox and returns its meta and body.
func (a *App) findAttachmentMessage(ctx context.Context, attSvc *attachment.Service, messageID string) (map[string]any, map[string]any, error) {
	result, err := attSvc.Client.CallRaw(ctx, "msg.inbox", map[string]any{"scope": "all"})
	if err != nil {
		return nil, nil, fmt.Errorf("read inbox: %w", err)
	}
	rows, _ := result["messages"].([]any)
	for _, row := range rows {
		entry, _ := row.(map[string]any)
		meta, _ := entry["meta"].(map[string]any)
		body, _ := entry["body"].(map[string]any)
		if id, _ := meta["message_id"].(string); id == messageID {
			return meta, body, nil
		}
	}
	return nil, nil, output.NewExitError("not_found", 5, fmt.Sprintf("message %q not found in inbox", messageID), "Sync the inbox first or check the message id.")
}

// extractManifest pulls the first attachment manifest from an attachment
// message body ({payload: {attachments: [...]}}).
func extractManifest(body map[string]any) (map[string]any, error) {
	payload, _ := body["payload"].(map[string]any)
	attachments, ok := payload["attachments"].([]any)
	if !ok || len(attachments) == 0 {
		return nil, output.NewExitError("backend_error", 1, "message has no attachment manifest", "The message body must contain payload.attachments.")
	}
	manifest, ok := attachments[0].(map[string]any)
	if !ok {
		return nil, output.NewExitError("backend_error", 1, "invalid attachment manifest", "The first attachments entry must be an object.")
	}
	return manifest, nil
}
