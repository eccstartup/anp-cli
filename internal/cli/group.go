package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/eccstartup/anp-cli/internal/group"
	"github.com/eccstartup/anp-cli/internal/output"
)

// groupService assembles the group service from resolved config + active
// identity + signed client.
func (a *App) groupService() (*group.Service, func(), error) {
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
	return group.NewService(resolved, active, client), func() {}, nil
}

// groupDID resolves the target group DID from --group or the first positional arg.
func (a *App) groupDID(cmd *Command, args []string) (string, error) {
	did, _ := cmd.Flags().GetString("group")
	did = strings.TrimSpace(did)
	if did == "" && len(args) > 0 {
		did = strings.TrimSpace(args[0])
	}
	if did == "" {
		return "", output.NewExitError("invalid_argument", 2, "--group is required.", "Run `anp-cli group <cmd> --group <group-did>`.")
	}
	return did, nil
}

func parseJSONObject(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var obj map[string]any
	if err := unmarshalJSON([]byte(raw), &obj); err != nil {
		return nil, output.NewExitError("invalid_argument", 2, fmt.Sprintf("invalid JSON: %v", err), "Provide a JSON object.")
	}
	return obj, nil
}

func (a *App) runGroupCreate(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	name, _ := cmd.Flags().GetString("name")
	profile, err := parseJSONObject(mustGetString(cmd, "group-profile"))
	if err != nil {
		return err
	}
	policy, err := parseJSONObject(mustGetString(cmd, "policy"))
	if err != nil {
		return err
	}
	if policy == nil {
		return output.NewExitError("invalid_argument", 2, "--policy is required.", `Run "anp-cli group create --name <n> --policy '{"admission_mode":"open-join","permissions":{}}'".`)
	}
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		plan := map[string]any{"name": name, "group_profile": profile, "policy": policy, "actions": []string{"create group via group.create"}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "Group create plan")
	}
	result, err := service.Create(context.Background(), group.CreateOptions{Name: name, GroupProfile: profile, Policy: policy})
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Group created", nil)
}

func (a *App) runGroupInfo(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	includePolicy, _ := cmd.Flags().GetBool("include-policy")
	includeMembers, _ := cmd.Flags().GetBool("include-members")
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		plan := map[string]any{"group": did, "include_policy": includePolicy, "include_members": includeMembers, "actions": []string{"fetch group info via group.get_info"}}
		return a.renderPlan(cmd.CommandPath(), format, plan, "Group info plan")
	}
	result, err := service.GetInfo(context.Background(), did, includePolicy, includeMembers)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Group info", nil)
}

func (a *App) runGroupJoin(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": did, "actions": []string{"join group via group.join"}}, "Group join plan")
	}
	result, err := service.Join(context.Background(), did)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Joined group", nil)
}

func (a *App) runGroupAdd(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	memberDID, _ := cmd.Flags().GetString("member-did")
	memberHandle, _ := cmd.Flags().GetString("member-handle")
	role, _ := cmd.Flags().GetString("role")
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": did, "member_did": memberDID, "member_handle": memberHandle, "role": role, "actions": []string{"add member via group.add"}}, "Group add plan")
	}
	result, err := service.Add(context.Background(), did, memberDID, memberHandle, role)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Member added", nil)
}

func (a *App) runGroupRemove(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	memberDID, _ := cmd.Flags().GetString("member-did")
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": did, "member_did": memberDID, "actions": []string{"remove member via group.remove"}}, "Group remove plan")
	}
	result, err := service.Remove(context.Background(), did, memberDID)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Member removed", nil)
}

func (a *App) runGroupLeave(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": did, "actions": []string{"leave group via group.leave"}}, "Group leave plan")
	}
	result, err := service.Leave(context.Background(), did)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Left group", nil)
}

func (a *App) runGroupProfile(cmd *Command, args []string) error {
	return a.runGroupPatch(cmd, args, false)
}

func (a *App) runGroupPolicy(cmd *Command, args []string) error {
	return a.runGroupPatch(cmd, args, true)
}

func (a *App) runGroupPatch(cmd *Command, args []string, policy bool) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	patch, err := parseJSONObject(mustGetString(cmd, "patch"))
	if err != nil {
		return err
	}
	if patch == nil {
		return output.NewExitError("invalid_argument", 2, "--patch is required.", "Provide an RFC 7386 JSON merge patch.")
	}
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	method := "group.update_profile"
	summary := "Group profile updated"
	if policy {
		method = "group.update_policy"
		summary = "Group policy updated"
	}
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": did, "patch": patch, "actions": []string{"apply patch via " + method}}, summary+" plan")
	}
	var result map[string]any
	if policy {
		result, err = service.UpdatePolicy(context.Background(), did, patch)
	} else {
		result, err = service.UpdateProfile(context.Background(), did, patch)
	}
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, summary, nil)
}

func (a *App) runGroupSend(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	text, _ := cmd.Flags().GetString("text")
	payload, err := parseJSONObject(mustGetString(cmd, "payload"))
	if err != nil {
		return err
	}
	mentionRaw, _ := cmd.Flags().GetStringArray("mention")
	if payload == nil && strings.TrimSpace(text) == "" {
		return output.NewExitError("invalid_argument", 2, "--text or --payload is required.", "Run `anp-cli group send --group <group-did> --text \"...\"`.")
	}
	// P9 mentions: parse each --mention spec and build the standard mentions array.
	mentions, err := buildMentionObjects(text, mentionRaw)
	if err != nil {
		return err
	}
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": did, "text": text, "payload": payload, "mentions": mentions, "actions": []string{"send group message via group.send"}}, "Group send plan")
	}
	result, err := service.Send(context.Background(), did, group.SendOptions{Text: text, Payload: payload, Mentions: mentions})
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Group message sent", nil)
}

// buildMentionObjects parses --mention specs and builds the P9 mentions array.
func buildMentionObjects(text string, raw []string) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	specs := make([]group.MentionSpec, 0, len(raw))
	for _, item := range raw {
		spec, err := group.ParseMentionSpec(item)
		if err != nil {
			return nil, output.NewExitError("invalid_argument", 2, fmt.Sprintf("invalid --mention %q: %v", item, err), "Use @surface:did:<did>, @surface:agent:<did>, @surface:all|agents|humans, or [role:]kind:value.")
		}
		specs = append(specs, spec)
	}
	mentions, err := group.BuildMentions(text, specs)
	if err != nil {
		return nil, output.NewExitError("invalid_argument", 2, err.Error(), "Ensure the @surface text appears in --text.")
	}
	return mentions, nil
}

func (a *App) runGroupMembers(cmd *Command, args []string) error {
	format := a.outputFormat(false)
	did, err := a.groupDID(cmd, args)
	if err != nil {
		return err
	}
	service, closeFn, err := a.groupService()
	if err != nil {
		return err
	}
	defer closeFn()
	if a.globals.DryRun {
		return a.renderPlan(cmd.CommandPath(), format, map[string]any{"group": did, "actions": []string{"list members via group.get_info"}}, "Group members plan")
	}
	result, err := service.GetInfo(context.Background(), did, false, true)
	if err != nil {
		return err
	}
	return a.renderSuccess(cmd.CommandPath(), format, result, "Group members", nil)
}

func mustGetString(cmd *Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}
