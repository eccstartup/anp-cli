package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ANPWorld/anp-cli/internal/group"
	"github.com/ANPWorld/anp-cli/internal/output"
)

func (a *App) groupService() (*group.Service, func(), error) {
	resolved, err := a.resolveConfig()
	if err != nil {
		return nil, nil, err
	}
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
	return group.NewService(db, active, client), func() { db.Close() }, nil
}

func (a *App) runGroupCreate(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	name, _ := cmd.Flags().GetString("name")
	members, _ := cmd.Flags().GetString("members")
	if strings.TrimSpace(name) == "" {
		return output.NewExitError("invalid_argument", 2, "--name is required.", "Run `anp-cli group create --name <n>`.")
	}
	service, closeDB, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeDB()
	plan := map[string]any{"name": name, "members": members, "actions": []string{"create group via backend", "persist local group record"}}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, plan, "Group create plan")
	}
	result, err := service.Create(context.Background(), name, members)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, fmt.Sprintf("Group %s created", name), nil)
}

func (a *App) runGroupJoin(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	groupDID, _ := cmd.Flags().GetString("group")
	if groupDID == "" {
		return output.NewExitError("invalid_argument", 2, "--group is required.", "Run `anp-cli group join --group <gid>`.")
	}
	service, closeDB, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeDB()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": groupDID, "actions": []string{"join group via backend", "persist local group record"}}, "Group join plan")
	}
	result, err := service.Join(context.Background(), groupDID)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Joined group", nil)
}

func (a *App) runGroupLeave(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	groupDID, _ := cmd.Flags().GetString("group")
	if groupDID == "" {
		return output.NewExitError("invalid_argument", 2, "--group is required.", "Run `anp-cli group leave --group <gid>`.")
	}
	service, closeDB, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeDB()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": groupDID, "actions": []string{"leave group via backend", "remove local group record"}}, "Group leave plan")
	}
	result, err := service.Leave(context.Background(), groupDID)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Left group", nil)
}

func (a *App) runGroupMembers(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	groupDID, _ := cmd.Flags().GetString("group")
	if groupDID == "" {
		return output.NewExitError("invalid_argument", 2, "--group is required.", "Run `anp-cli group members --group <gid>`.")
	}
	service, closeDB, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeDB()
	members, err := service.Members(context.Background(), groupDID)
	if err != nil {
		return err
	}
	data := map[string]any{"group": groupDID, "members": members}
	return a.renderSuccess(cmd.CommandPath(), format, data, fmt.Sprintf("%d member(s)", len(members)), nil)
}
