package cli

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kardianos/service"

	appconfig "github.com/eccstartup/anp-cli/internal/config"
	"github.com/eccstartup/anp-cli/internal/message"
	"github.com/eccstartup/anp-cli/internal/output"
)

const (
	serviceName        = "anp-runtime"
	serviceDisplayName = "ANP Runtime Receiver"
	serviceDescription = "ANP message receiver daemon: polls the backend inbox and persists messages locally"
)

// receiverProgram implements service.Interface so the service manager can
// start and stop the polling loop.
type receiverProgram struct {
	service *message.Service
	started chan struct{}
	cancel  context.CancelFunc
	mu      sync.Mutex
}

func newReceiverProgram(svc *message.Service) *receiverProgram {
	return &receiverProgram{service: svc, started: make(chan struct{})}
}

func (p *receiverProgram) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()
	go func() {
		_ = runPollLoopCtx(ctx, p.service, "http", 15*time.Second)
	}()
	close(p.started)
	return nil
}

func (p *receiverProgram) Stop(s service.Service) error {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// daemonService builds the kardianos/service config for the runtime receiver.
func daemonService(resolved *appconfig.Resolved) (*service.Config, error) {
	executable, err := currentExecutable()
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	if resolved.Paths.Root != "" {
		env["ANP_WORKSPACE"] = resolved.Paths.Root
	}
	if resolved.Backend != "" {
		env["ANP_BACKEND"] = resolved.Backend
	}
	return &service.Config{
		Name:             serviceName,
		DisplayName:      serviceDisplayName,
		Description:      serviceDescription,
		Executable:       executable,
		Arguments:        []string{"runtime", "listen-service"},
		EnvVars:          env,
		WorkingDirectory: resolved.Paths.Root,
		Option:           service.KeyValue{"UserService": true},
	}, nil
}

// runRuntimeListenService is the hidden entry point invoked by the service
// manager: it runs the receiver under kardianos/service so Start/Stop are
// driven by LaunchAgent / systemd / Windows Service.
func (a *App) runRuntimeListenService(cmd *Command, args []string) error {
	svc, closeDB, err := a.msgService()
	if err != nil {
		return err
	}
	defer closeDB()
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	config, err := daemonService(resolved)
	if err != nil {
		return err
	}
	program := newReceiverProgram(svc)
	systemService, err := service.New(program, config)
	if err != nil {
		return err
	}
	return systemService.Run()
}

func (a *App) runRuntimeInstall(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	if resolved.Backend == "" {
		return output.NewExitError("invalid_argument", 2, "a backend is required to run the receiver.", "Set ANP_BACKEND or run `anp-cli config set --backend <url>`.")
	}
	config, err := daemonService(resolved)
	if err != nil {
		return err
	}
	if a.globals.DryRun {
		plan := map[string]any{"service": serviceName, "executable": config.Executable, "arguments": config.Arguments, "env": config.EnvVars, "actions": []string{"install system service"}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "Install plan")
	}
	systemService, err := service.New(nil, config)
	if err != nil {
		return err
	}
	if err := systemService.Install(); err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, map[string]any{"service": serviceName, "installed": true}, "Service installed", nil)
}

func (a *App) runRuntimeServiceControl(cmd *Command, args []string, action string) error {
	format := a.outputFormat(false)
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	config, err := daemonService(resolved)
	if err != nil {
		return err
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"service": serviceName, "action": action, "actions": []string{action + " system service"}}, "Service control plan")
	}
	systemService, err := service.New(nil, config)
	if err != nil {
		return err
	}
	if err := service.Control(systemService, action); err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, map[string]any{"service": serviceName, "action": action}, fmt.Sprintf("Service %s", action), nil)
}

func (a *App) runRuntimeStatus(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	config, err := daemonService(resolved)
	if err != nil {
		return err
	}
	systemService, err := service.New(nil, config)
	if err != nil {
		return err
	}
	status, err := systemService.Status()
	if err != nil {
		return err
	}
	data := map[string]any{"service": serviceName, "status": serviceStatusName(status)}
	return a.renderSuccess(cmd.CommandPath(), format, data, "Service status", nil)
}

func serviceStatusName(status service.Status) string {
	switch status {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
