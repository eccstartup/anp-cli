package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/eccstartup/anp-cli/internal/message"
	"github.com/eccstartup/anp-cli/internal/output"
)

func (a *App) runRuntimeListen(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	mode, _ := cmd.Flags().GetString("mode")
	everyRaw, _ := cmd.Flags().GetString("every")
	once, _ := cmd.Flags().GetBool("once")
	if mode == "" {
		mode = "http"
	}
	interval, err := time.ParseDuration(everyRaw)
	if err != nil || interval <= 0 {
		interval = 15 * time.Second
	}
	service, closeDB, err := a.msgService()
	if err != nil {
		return err
	}
	defer closeDB()
	plan := map[string]any{
		"mode": mode, "every": interval.String(), "once": once,
		"actions": []string{"poll backend inbox", "persist incoming messages", "track contacts"},
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Receiver plan")
	}
	if once {
		pulled, err := service.Sync(context.Background())
		if err != nil {
			return err
		}
		return a.renderSuccess(cmd.CommandPath(), format, map[string]any{"mode": mode, "pulled": len(pulled), "messages": pulled}, fmt.Sprintf("Pulled %d message(s)", len(pulled)), nil)
	}
	return runPollLoop(cmd, service, mode, interval)
}

func runPollLoop(cmd *Command, service *message.Service, mode string, interval time.Duration) error {
	fmt.Fprintf(os.Stderr, "[anp-cli] receiver %s polling every %s (Ctrl-C to stop)\n", mode, interval)
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	return runPollLoopCtx(ctx, service, mode, interval)
}

func runPollLoopCtx(ctx context.Context, service *message.Service, mode string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		syncOnce(service)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func syncOnce(service *message.Service) {
	start := time.Now()
	pulled, err := service.Sync(context.Background())
	// Never emit message bodies to stdout: in service mode this leaks E2EE
	// plaintext into the system log. Emit metadata only.
	entries := make([]map[string]any, 0, len(pulled))
	for _, m := range pulled {
		entries = append(entries, map[string]any{
			"message_id":    m.MessageID,
			"sender_did":    m.SenderDID,
			"recipient_did": m.RecipientDID,
			"type":          m.Type,
			"secure":        m.Secure,
			"direction":     m.Direction,
			"sent_at":       m.SentAt,
		})
	}
	entry := map[string]any{
		"ok":       err == nil,
		"command":  "anp-cli runtime listen",
		"pulled":   len(pulled),
		"messages": entries,
		"took_ms":  time.Since(start).Milliseconds(),
	}
	if err != nil {
		entry["error"] = map[string]any{"message": err.Error()}
	}
	raw, _ := json.Marshal(entry)
	fmt.Fprintln(os.Stdout, string(raw))
}

func (a *App) runRuntimeHeartbeat(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	everyRaw, _ := cmd.Flags().GetString("every")
	install, _ := cmd.Flags().GetBool("install")
	if install {
		return output.NewExitError("invalid_argument", 2, "--install is not implemented yet (heartbeat runs once only)", "Run `anp-cli runtime heartbeat` without --install.")
	}
	service, closeDB, err := a.msgService()
	if err != nil {
		return err
	}
	defer closeDB()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"install": install, "every": everyRaw, "actions": []string{"send heartbeat", "sync inbox"}}, "Heartbeat plan")
	}
	start := time.Now()
	_, syncErr := service.Sync(context.Background())
	data := map[string]any{
		"heartbeat_at": time.Now().UTC().Format(time.RFC3339),
		"synced":       syncErr == nil,
		"took_ms":      time.Since(start).Milliseconds(),
	}
	summary := "Heartbeat sent"
	if syncErr != nil {
		summary = "Heartbeat sent (inbox sync failed)"
	}
	warnings := []string{}
	if syncErr != nil {
		warnings = append(warnings, syncErr.Error())
	}
	return a.renderSuccess(cmd.CommandPath(), format, data, summary, warnings)
}

func (a *App) runSetup(cmd *Command, args []string) error {
	format := a.outputFormat(true)
	_ = cmd.Flags().Set("mode", "http")
	service, closeDB, err := a.msgService()
	if err != nil {
		return err
	}
	defer closeDB()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"mode": "http", "actions": []string{"start http receiver loop"}}, "Setup plan")
	}
	interval := 15 * time.Second
	return runPollLoop(cmd, service, "http", interval)
}

// messageService is the runtime-facing message service type.
type messageService = message.Service
