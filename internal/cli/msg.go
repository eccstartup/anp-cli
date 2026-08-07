package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ANPWorld/anp-cli/internal/message"
	"github.com/ANPWorld/anp-cli/internal/output"
	"github.com/ANPWorld/anp-cli/internal/store"
)

// msgService assembles the message service from resolved config + active
// identity + signed client, opening the local database.
func (a *App) msgService() (*message.Service, func(), error) {
	resolved, err := a.resolveConfig()
	if err != nil {
		return nil, nil, err
	}
	service := message.NewService(nil, nil, nil, nil)
	db, err := a.openDB(resolved)
	if err != nil {
		return nil, nil, err
	}
	active, err := a.activeIdentity()
	if err != nil {
		db.Close()
		return nil, nil, output.NewExitError("not_initialized", 3, err.Error(), "Run `anp-cli init` first.")
	}
	client, err := a.signedClient(resolved, active)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	service.Config = resolved
	service.DB = db
	service.Active = active
	service.Client = client
	return service, func() { db.Close() }, nil
}

func (a *App) runMsgSend(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	to, _ := cmd.Flags().GetString("to")
	groupDID, _ := cmd.Flags().GetString("group")
	text, _ := cmd.Flags().GetString("text")
	msgType, _ := cmd.Flags().GetString("type")
	secureRaw, _ := cmd.Flags().GetString("secure")
	return a.sendMessageCore(cmd, format, message.SendOptions{
		To: to, Group: groupDID, Type: msgType, Text: text, Secure: strings.EqualFold(secureRaw, "on"),
	})
}

func (a *App) runDM(cmd *Command, args []string) error {
	format := a.outputFormat(true)
	if len(args) < 1 {
		return output.NewExitError("invalid_argument", 2, "dm requires a target DID or handle.", "Run `anp-cli dm <did> \"message text\"`.")
	}
	target := strings.TrimSpace(args[0])
	text := ""
	if len(args) > 1 {
		text = strings.TrimSpace(strings.Join(args[1:], " "))
	}
	if text == "" {
		text, _ = cmd.Flags().GetString("text")
	}
	if text == "" {
		return output.NewExitError("invalid_argument", 2, "message text is required.", "Run `anp-cli dm <did> \"message text\"`.")
	}
	return a.sendMessageCore(cmd, format, message.SendOptions{To: target, Type: "text", Text: text})
}

func (a *App) sendMessageCore(cmd *Command, format output.Format, options message.SendOptions) error {
	if options.To == "" && options.Group == "" {
		return output.NewExitError("invalid_argument", 2, "either --to or --group is required.", "Run `anp-cli msg send --to <did> --text \"...\"`.")
	}
	service, closeDB, err := a.msgService()
	if err != nil {
		return err
	}
	defer closeDB()
	plan := map[string]any{
		"to": options.To, "group": options.Group, "text": options.Text, "secure": options.Secure,
		"actions": []string{"sign request with HTTP Message Signatures", "deliver via backend msg.send", "persist outbound message locally"},
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Message send plan")
	}
	sent, err := service.Send(context.Background(), options)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, sent, "Message sent", nil)
}

func (a *App) runMsgInbox(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	return a.readInbox(cmd, format, false)
}

func (a *App) runInbox(cmd *Command, args []string) error {
	format := a.outputFormat(true)
	return a.readInbox(cmd, format, true)
}

func (a *App) readInbox(cmd *Command, format output.Format, shortcut bool) error {
	scope, _ := cmd.Flags().GetString("scope")
	unread, _ := cmd.Flags().GetBool("unread")
	limit, _ := cmd.Flags().GetInt("limit")
	service, closeDB, err := a.msgService()
	if err != nil {
		return err
	}
	defer closeDB()
	if a.globals.DryRun {
		plan := map[string]any{"scope": scope, "unread": unread, "limit": limit, "actions": []string{"sync inbox from backend", "read local message store"}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "Inbox read plan")
	}
	// Best-effort sync; local store is still readable when offline.
	_, syncErr := service.Sync(context.Background())
	filter := store.MessageFilter{Scope: scope, UnreadOnly: unread, Limit: limit}
	messages, err := service.Inbox(filter)
	if err != nil {
		return err
	}
	warnings := []string{}
	if syncErr != nil {
		warnings = append(warnings, "inbox sync failed: "+syncErr.Error())
	}
	data := map[string]any{"messages": messages, "synced": syncErr == nil}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("%d message(s)", len(messages)), warnings)
}

func (a *App) runMsgHistory(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	peer, _ := cmd.Flags().GetString("with")
	limit, _ := cmd.Flags().GetInt("limit")
	return a.readHistoryCore(cmd, format, peer, limit)
}

func (a *App) runHistory(cmd *Command, args []string) error {
	format := a.outputFormat(true)
	if len(args) < 1 {
		return output.NewExitError("invalid_argument", 2, "history requires a peer DID or handle.", "Run `anp-cli history <did>`.")
	}
	peer := strings.TrimSpace(args[0])
	limit, _ := cmd.Flags().GetInt("limit")
	return a.readHistoryCore(cmd, format, peer, limit)
}

func (a *App) readHistoryCore(cmd *Command, format output.Format, peer string, limit int) error {
	if strings.TrimSpace(peer) == "" {
		return output.NewExitError("invalid_argument", 2, "--with is required.", "Run `anp-cli msg history --with <did>`.")
	}
	service, closeDB, err := a.msgService()
	if err != nil {
		return err
	}
	defer closeDB()
	if a.globals.DryRun {
		plan := map[string]any{"with": peer, "limit": limit, "actions": []string{"sync inbox from backend", "read local thread"}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "History read plan")
	}
	_, _ = service.Sync(context.Background())
	messages, err := service.History(peer, limit)
	if err != nil {
		return err
	}
	data := map[string]any{"with": peer, "messages": messages}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("%d message(s)", len(messages)), nil)
}
