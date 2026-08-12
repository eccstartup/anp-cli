package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ANPWorld/anp-cli/internal/buildinfo"
	"github.com/ANPWorld/anp-cli/internal/cmdmeta"
	appconfig "github.com/ANPWorld/anp-cli/internal/config"
	"github.com/ANPWorld/anp-cli/internal/doctor"
	"github.com/ANPWorld/anp-cli/internal/identity"
	"github.com/ANPWorld/anp-cli/internal/output"
	"github.com/spf13/cobra"
)

func newRootCommand(app *App) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "anp-cli",
		Short:         "Agent Network Protocol CLI",
		Long:          `anp-cli — an Agent Network Protocol (ANP) CLI. Manages DID identity, messaging, groups, discovery, and proofs against any ANP backend.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			app.globals.FormatChanged = cmd.Flags().Changed("format") || cmd.Flags().Changed("json")
			app.globals.IdentityChanged = cmd.Flags().Changed("identity")
			if _, err := output.NormalizeFormat(app.globals.Format); err != nil {
				return output.NewExitError("invalid_argument", 2, err.Error(), "Use --format json, pretty, or table.")
			}
			return nil
		},
	}
	rootCmd.PersistentFlags().StringVar(&app.globals.Format, "format", string(output.FormatJSON), "Output format: json | pretty | table")
	rootCmd.PersistentFlags().BoolVar(&app.globals.JSON, "json", false, "Alias for --format json")
	rootCmd.PersistentFlags().StringVar(&app.globals.JQ, "jq", "", "Apply a jq expression to the JSON envelope")
	rootCmd.PersistentFlags().BoolVar(&app.globals.DryRun, "dry-run", false, "Render the execution plan without mutating state")
	rootCmd.PersistentFlags().BoolVar(&app.globals.Yes, "yes", false, "Skip confirmation prompts")
	rootCmd.PersistentFlags().StringVar(&app.globals.Identity, "identity", "", "Select the active identity")

	commandsByName := map[string]*cobra.Command{"": rootCmd}
	specs := app.catalog.Specs()
	sort.Slice(specs, func(i, j int) bool {
		leftDepth := strings.Count(specs[i].Name, ".")
		rightDepth := strings.Count(specs[j].Name, ".")
		if leftDepth == rightDepth {
			return specs[i].Name < specs[j].Name
		}
		return leftDepth < rightDepth
	})
	for _, spec := range specs {
		command := app.commandFromSpec(spec)
		parent := commandsByName[parentName(spec.Name)]
		if parent == nil {
			panic(fmt.Sprintf("missing parent command for %s", spec.Name))
		}
		parent.AddCommand(command)
		commandsByName[strings.ToLower(spec.Name)] = command
	}
	return rootCmd
}

func parentName(name string) string {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	index := strings.LastIndex(trimmed, ".")
	if index < 0 {
		return ""
	}
	return trimmed[:index]
}

func (a *App) commandFromSpec(spec cmdmeta.CommandSpec) *cobra.Command {
	command := &cobra.Command{
		Use:     spec.Use,
		Short:   spec.Short,
		Long:    spec.Long,
		Aliases: spec.Aliases,
		Hidden:  spec.Hidden,
	}
	for _, flag := range spec.Flags {
		switch flag.Type {
		case "string":
			command.Flags().String(flag.Name, flag.Default, flag.Usage)
		case "bool":
			defaultValue := strings.EqualFold(flag.Default, "true")
			command.Flags().Bool(flag.Name, defaultValue, flag.Usage)
		case "int":
			defaultValue := 0
			if strings.TrimSpace(flag.Default) != "" {
				if parsed, err := strconv.Atoi(flag.Default); err == nil {
					defaultValue = parsed
				}
			}
			command.Flags().Int(flag.Name, defaultValue, flag.Usage)
		default:
			command.Flags().String(flag.Name, flag.Default, flag.Usage)
		}
		if flag.Required {
			_ = command.MarkFlagRequired(flag.Name)
		}
	}
	if handler := a.handlerFor(spec); handler != nil {
		command.RunE = handler
	}
	return command
}

func (a *App) handlerFor(spec cmdmeta.CommandSpec) func(*cobra.Command, []string) error {
	switch spec.Handler {
	case "init":
		return a.runInit
	case "status":
		return a.runStatus
	case "schema":
		return a.runSchema
	case "doctor":
		return a.runDoctor
	case "version":
		return a.runVersion
	case "describe":
		return a.runDescribe
	case "config.show":
		return a.runConfigShow
	case "config.set":
		return a.runConfigSet
	case "id.show":
		return a.runIDShow
	case "id.list":
		return a.runIDList
	case "id.current":
		return a.runIDCurrent
	case "id.use":
		return a.runIDUse
	case "id.resolve":
		return a.runIDResolve
	case "id.register":
		return a.runIDRegister
	case "id.recover":
		return a.runIDRecover
	case "msg.send":
		return a.runMsgSend
	case "msg.inbox":
		return a.runMsgInbox
	case "msg.history":
		return a.runMsgHistory
	case "group.create":
		return a.runGroupCreate
	case "group.join":
		return a.runGroupJoin
	case "group.leave":
		return a.runGroupLeave
	case "group.members":
		return a.runGroupMembers
	case "runtime.listen":
		return a.runRuntimeListen
	case "runtime.listen-service":
		return a.runRuntimeListenService
	case "runtime.heartbeat":
		return a.runRuntimeHeartbeat
	case "runtime.install":
		return a.runRuntimeInstall
	case "runtime.start":
		return func(cmd *Command, args []string) error { return a.runRuntimeServiceControl(cmd, args, "start") }
	case "runtime.stop":
		return func(cmd *Command, args []string) error { return a.runRuntimeServiceControl(cmd, args, "stop") }
	case "runtime.restart":
		return func(cmd *Command, args []string) error { return a.runRuntimeServiceControl(cmd, args, "restart") }
	case "runtime.uninstall":
		return func(cmd *Command, args []string) error { return a.runRuntimeServiceControl(cmd, args, "uninstall") }
	case "runtime.status":
		return a.runRuntimeStatus
	case "setup":
		return a.runSetup
	case "register":
		return a.runRegister
	case "whoami":
		return a.runWhoami
	case "inbox":
		return a.runInbox
	case "dm":
		return a.runDM
	case "history":
		return a.runHistory
	case "discovery.crawl":
		return a.runDiscoveryCrawl
	case "discovery.search":
		return a.runDiscoverySearch
	case "e2ee.init":
		return a.runE2EEInit
	case "e2ee.status":
		return a.runE2EEStatus
	case "proof.sign":
		return a.runProofSign
	case "proof.verify":
		return a.runProofVerify
	case "completion.bash":
		return func(cmd *cobra.Command, args []string) error { return cmd.Root().GenBashCompletion(cmd.OutOrStdout()) }
	case "completion.zsh":
		return func(cmd *cobra.Command, args []string) error { return cmd.Root().GenZshCompletion(cmd.OutOrStdout()) }
	case "completion.fish":
		return func(cmd *cobra.Command, args []string) error {
			return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
		}
	default:
		return nil
	}
}

// ---------------------------------------------------------------- top level

func (a *App) runInit(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	name := "alice"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		name = args[0]
	}
	plan := map[string]any{
		"workspace": resolved.Paths.Root,
		"config":    resolved.Paths.ConfigFile,
		"identity":  map[string]any{"name": name, "did_domain": resolved.DidDomain},
		"actions":   []string{"create workspace directories", "write config.yaml", "generate e1 did:wba identity and key material"},
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Workspace initialization plan")
	}
	service := identity.NewService(resolved.Paths.IdentityDir)
	generated, err := service.Init(resolved, name, true)
	if err != nil {
		return err
	}
	// Persist defaults into config.yaml when it does not exist yet.
	if !resolved.ConfigExists {
		file := appconfig.File{Identity: name}
		if resolved.Backend != "" {
			file.Backend = resolved.Backend
		}
		if resolved.DidDomain != "" {
			file.DidDomain = resolved.DidDomain
		}
		if err := appconfig.WriteFile(resolved.Paths.ConfigFile, file); err != nil {
			return err
		}
	}
	data := map[string]any{
		"workspace": resolved.Paths.Root,
		"identity":  identity.PublicView(generated),
	}
	return a.renderSuccess(cmd.CommandPath(), format, data, "Workspace initialized", nil)
}

func (a *App) runStatus(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	service := identity.NewService(resolved.Paths.IdentityDir)
	active, activeErr := service.Active()
	items, _ := service.List()
	state := map[string]any{}
	if active != nil {
		state = map[string]any{
			"identity": identity.PublicView(active),
		}
	} else if activeErr != nil {
		state["error"] = activeErr.Error()
	}
	data := map[string]any{
		"workspace":      resolved.Paths,
		"config":         map[string]any{"exists": resolved.ConfigExists, "error": resolved.ConfigError, "sources": resolved.Sources},
		"backend":        resolved.Backend,
		"did_domain":     resolved.DidDomain,
		"identities":     items,
		"identity_state": state,
	}
	return a.renderSuccess(cmd.CommandPath(), format, data, "Workspace status", nil)
}

func (a *App) runSchema(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	if len(args) == 0 {
		data := map[string]any{
			"commands": a.catalog.Specs(),
			"protocol": buildinfo.Current().Protocol,
		}
		return a.renderSuccess(cmd.CommandPath(), format, data, "Command contract", nil)
	}
	target := strings.Join(args, " ")
	spec, ok := a.catalog.Lookup(target)
	if !ok {
		return output.NewExitError("not_found", 5, fmt.Sprintf("Unknown command schema target %q", target), "Run `anp-cli schema` to list commands.")
	}
	data := map[string]any{
		"command":  spec,
		"children": a.catalog.ChildrenOf(spec.Name),
	}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("Contract for %s", spec.Name), nil)
}

func (a *App) runDoctor(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	report := doctor.Run(resolved)
	return a.renderSuccess(cmd.CommandPath(), format, report, report.Summary, nil)
}

func (a *App) runVersion(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	return a.renderSuccess(cmd.CommandPath(), format, buildinfo.Current(), "Build information", nil)
}

func (a *App) runConfigShow(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	service := identity.NewService(resolved.Paths.IdentityDir)
	current, _ := service.Store.CurrentName()
	data := map[string]any{
		"paths":            resolved.Paths,
		"config_exists":    resolved.ConfigExists,
		"config_error":     resolved.ConfigError,
		"backend":          resolved.Backend,
		"did_domain":       resolved.DidDomain,
		"active_identity":  resolved.ActiveIdentity,
		"default_identity": current,
		"sources":          resolved.Sources,
	}
	return a.renderSuccess(cmd.CommandPath(), format, data, "Resolved configuration", nil)
}

func (a *App) runConfigSet(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	backend, _ := cmd.Flags().GetString("backend")
	didDomain, _ := cmd.Flags().GetString("did-domain")
	if backend == "" && didDomain == "" {
		return output.NewExitError("invalid_argument", 2, "config set requires --backend or --did-domain.", "Run `anp-cli config set --backend <url>` to persist the backend.")
	}
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	if a.globals.DryRun {
		plan := map[string]any{"config_file": resolved.Paths.ConfigFile, "set": map[string]any{"backend": backend, "did_domain": didDomain}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "Config update plan")
	}
	file := resolved.File
	if backend != "" {
		file.Backend = backend
	}
	if didDomain != "" {
		file.DidDomain = didDomain
	}
	if err := appconfig.WriteFile(resolved.Paths.ConfigFile, file); err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, map[string]any{"config_file": resolved.Paths.ConfigFile, "file": file}, "Configuration updated", nil)
}

// ---------------------------------------------------------------- describe

func (a *App) runDescribe(cmd *cobra.Command, args []string) error {
	format := a.outputFormat(false)
	set, _ := cmd.Flags().GetString("set")
	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")
	capabilities, _ := cmd.Flags().GetString("capabilities")
	active, err := a.activeIdentity()
	if err != nil {
		return err
	}
	adPath, _ := filepath.Abs(filepath.Join(filepath.Dir(active.Keys.Key1Private), "ad.json"))
	existing := map[string]any{}
	if raw, err := os.ReadFile(adPath); err == nil {
		_ = unmarshalJSON(raw, &existing)
	}
	if set == "" && name == "" && description == "" && capabilities == "" {
		if len(existing) == 0 {
			return output.NewExitError("not_found", 5, "No agent description at "+adPath, "Create one with `anp-cli describe --name ...`.")
		}
		return a.renderSuccess(cmd.CommandPath(), format, map[string]any{"ad": existing, "path": adPath}, "Agent description", nil)
	}
	plan := map[string]any{"path": adPath, "actions": []string{"write ad.json"}}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Describe update plan")
	}
	next := map[string]any{}
	if set != "" {
		if err := unmarshalJSON([]byte(set), &next); err != nil {
			return output.NewExitError("invalid_argument", 2, fmt.Sprintf("--set must be valid JSON: %v", err), "Run `anp-cli describe` to see the current ad.json.")
		}
	} else {
		for key, value := range existing {
			next[key] = value
		}
		if name != "" {
			next["name"] = name
		}
		if description != "" {
			next["description"] = description
		}
		if capabilities != "" {
			next["capabilities"] = splitCSV(capabilities)
		}
		next["did"] = active.DID
	}
	raw, _ := jsonMarshalIndent(next)
	if err := os.WriteFile(adPath, raw, 0o600); err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, map[string]any{"ad": next, "path": adPath}, "Agent description updated", nil)
}
