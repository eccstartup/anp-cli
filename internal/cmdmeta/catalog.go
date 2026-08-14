// Package cmdmeta holds the static command catalog. It is the single source of
// truth for the command tree, flags, and schema output.
package cmdmeta

import (
	"fmt"
	"sort"
	"strings"
)

type FlagSpec struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Usage      string   `json:"usage"`
	Default    string   `json:"default,omitempty"`
	Required   bool     `json:"required,omitempty"`
	Choices    []string `json:"choices,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
}

type CommandSpec struct {
	Name        string     `json:"name"`
	Use         string     `json:"use"`
	Short       string     `json:"short"`
	Long        string     `json:"long,omitempty"`
	Aliases     []string   `json:"aliases,omitempty"`
	Shortcut    bool       `json:"shortcut,omitempty"`
	Phase       string     `json:"phase"`
	Hidden      bool       `json:"hidden,omitempty"`
	Implemented bool       `json:"implemented"`
	Handler     string     `json:"handler,omitempty"`
	SideEffect  bool       `json:"side_effect"`
	NeedsAuth   bool       `json:"needs_auth,omitempty"`
	Outputs     []string   `json:"outputs,omitempty"`
	Flags       []FlagSpec `json:"flags,omitempty"`
}

type Catalog struct {
	specs []CommandSpec
	index map[string]CommandSpec
}

func NewCatalog() *Catalog {
	specs := defaultSpecs()
	index := make(map[string]CommandSpec, len(specs))
	for _, spec := range specs {
		index[normalizeName(spec.Name)] = spec
	}
	return &Catalog{specs: specs, index: index}
}

func (c *Catalog) Specs() []CommandSpec {
	if c == nil {
		return nil
	}
	return append([]CommandSpec(nil), c.specs...)
}

func (c *Catalog) Lookup(raw string) (CommandSpec, bool) {
	if c == nil {
		return CommandSpec{}, false
	}
	name := normalizeName(raw)
	spec, ok := c.index[name]
	return spec, ok
}

func (c *Catalog) MustLookup(raw string) CommandSpec {
	spec, ok := c.Lookup(raw)
	if !ok {
		panic(fmt.Sprintf("command metadata not found: %s", raw))
	}
	return spec
}

func (c *Catalog) ChildrenOf(parent string) []CommandSpec {
	if c == nil {
		return nil
	}
	needle := normalizeName(parent)
	children := make([]CommandSpec, 0)
	for _, spec := range c.specs {
		if ParentName(spec.Name) == needle {
			children = append(children, spec)
		}
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	return children
}

func normalizeName(raw string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "anp"))
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.ReplaceAll(trimmed, " ", ".")
	trimmed = strings.Trim(trimmed, ".")
	return strings.ToLower(trimmed)
}

// ParentName returns the normalized parent command name of a full command
// name (e.g. "msg.send" -> "msg", "status" -> "").
func ParentName(name string) string {
	normalized := normalizeName(name)
	last := strings.LastIndex(normalized, ".")
	if last < 0 {
		return ""
	}
	return normalized[:last]
}

func defaultSpecs() []CommandSpec {
	return []CommandSpec{
		// ---------------------------------------------------------------- top level
		{Name: "status", Use: "status", Short: "Show workspace status", Phase: "phase1", Implemented: true, Handler: "status", Outputs: []string{"json", "pretty", "table"}},
		{Name: "schema", Use: "schema [command]", Short: "Show the command contract", Phase: "phase1", Implemented: true, Handler: "schema", Outputs: []string{"json", "pretty", "table"}},
		{Name: "doctor", Use: "doctor", Short: "Run environment and storage diagnostics", Phase: "phase4", Implemented: true, Handler: "doctor", Outputs: []string{"json", "pretty", "table"}},
		{Name: "version", Use: "version", Short: "Show build information", Phase: "phase1", Implemented: true, Handler: "version", Outputs: []string{"json", "pretty", "table"}},
		{Name: "init", Use: "init [name]", Short: "Initialize the workspace and generate a DID identity", Long: "Initialize ~/.anp/ (or $ANP_WORKSPACE): writes config.yaml, creates the identity store, and generates a default e1 DID identity. With an argument, names the identity.", Phase: "phase1", Implemented: true, Handler: "init", SideEffect: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "service-did", Type: "string", Usage: "Service DID for the message service (P8 federation)"}, {Name: "sign-as-service", Type: "bool", Usage: "Sign requests with the service identity instead of the agent identity", Default: "false"}}},
		{Name: "describe", Use: "describe", Short: "Show or update the agent description (ad.json)", Long: "Reads the local ad.json. With --set, writes a new description JSON, or --name/--capabilities to patch individual fields.", Phase: "phase2", Implemented: true, Handler: "describe", Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "set", Type: "string", Usage: "Full ad.json JSON to write"}, {Name: "name", Type: "string", Usage: "Agent display name"}, {Name: "description", Type: "string", Usage: "Agent description"}, {Name: "capabilities", Type: "string", Usage: "Comma-separated capability list"}}},
		{Name: "completion", Use: "completion", Short: "Generate shell completion scripts", Phase: "phase1", Implemented: true},
		{Name: "completion.bash", Use: "bash", Short: "Generate Bash completion", Phase: "phase1", Implemented: true, Handler: "completion.bash"},
		{Name: "completion.zsh", Use: "zsh", Short: "Generate Zsh completion", Phase: "phase1", Implemented: true, Handler: "completion.zsh"},
		{Name: "completion.fish", Use: "fish", Short: "Generate Fish completion", Phase: "phase1", Implemented: true, Handler: "completion.fish"},
		{Name: "config", Use: "config", Short: "Inspect resolved configuration", Phase: "phase1", Implemented: true},
		{Name: "config.show", Use: "show", Short: "Show resolved configuration values", Phase: "phase1", Implemented: true, Handler: "config.show", Outputs: []string{"json", "pretty", "table"}},
		{Name: "config.set", Use: "set", Short: "Update persistent configuration", Phase: "phase1", Implemented: true, Handler: "config.set", SideEffect: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "backend", Type: "string", Usage: "Backend base URL"}, {Name: "did-domain", Type: "string", Usage: "DID provider domain for generated identities"}, {Name: "service-did", Type: "string", Usage: "Service DID for the message service (P8 federation)"}, {Name: "sign-as-service", Type: "bool", Usage: "Sign requests with the service identity instead of the agent identity", Default: "false"}}},

		// ---------------------------------------------------------------- id
		{Name: "id", Use: "id", Short: "Identity commands", Phase: "phase1", Implemented: true},
		{Name: "id.show", Use: "show", Short: "Show the active identity", Aliases: []string{"whoami"}, Phase: "phase1", Implemented: true, Handler: "id.show", Outputs: []string{"json", "pretty", "table"}},
		{Name: "id.list", Use: "list", Short: "List local identities", Phase: "phase2", Implemented: true, Handler: "id.list", Outputs: []string{"json", "pretty", "table"}},
		{Name: "id.current", Use: "current", Short: "Show the default identity", Phase: "phase2", Implemented: true, Handler: "id.current", Outputs: []string{"json", "pretty", "table"}},
		{Name: "id.use", Use: "use <name>", Short: "Switch the default identity", Phase: "phase2", Implemented: true, Handler: "id.use", SideEffect: true, Outputs: []string{"json", "pretty"}},
		{Name: "id.resolve", Use: "resolve <did|handle>", Short: "Resolve a DID or handle to a DID document", Phase: "phase1", Implemented: true, Handler: "id.resolve", Outputs: []string{"json", "pretty", "table"}},
		{Name: "id.register", Use: "register --handle <h> [--email|--phone]", Short: "Register a WNS handle for the active identity", Phase: "phase2", Implemented: true, Handler: "id.register", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "handle", Type: "string", Usage: "Handle local part", Required: true}, {Name: "email", Type: "string", Usage: "Contact email (local metadata)"}, {Name: "phone", Type: "string", Usage: "Contact phone (local metadata)"}}},
		{Name: "id.recover", Use: "recover --handle <h>", Short: "Re-bind a handle (signature-authenticated)", Phase: "phase2", Implemented: true, Handler: "id.recover", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "handle", Type: "string", Usage: "Handle local part", Required: true}}},

		// ---------------------------------------------------------------- msg
		{Name: "msg", Use: "msg", Short: "Messaging commands", Phase: "phase1", Implemented: true},
		{Name: "msg.send", Use: "send", Short: "Send a direct message", Phase: "phase2", Implemented: true, Handler: "msg.send", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "to", Type: "string", Usage: "Direct message target DID or handle"}, {Name: "text", Type: "string", Usage: "Message text"}, {Name: "type", Type: "string", Usage: "Message type", Default: "text"}, {Name: "secure", Type: "bool", Usage: "Send end-to-end encrypted (direct_e2ee) instead of plaintext", Default: "false"}}},
		{Name: "msg.inbox", Use: "inbox", Short: "Read the inbox", Phase: "phase2", Implemented: true, Handler: "msg.inbox", NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "scope", Type: "string", Usage: "Message scope", Default: "all", Choices: []string{"all", "direct", "group"}}, {Name: "unread", Type: "bool", Usage: "Only unread messages"}, {Name: "limit", Type: "int", Usage: "Maximum number of results", Default: "20"}}},
		{Name: "msg.history", Use: "history --with <did>", Short: "Read message history with a peer", Phase: "phase2", Implemented: true, Handler: "msg.history", NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "with", Type: "string", Usage: "Direct peer DID or handle", Required: true}, {Name: "limit", Type: "int", Usage: "Maximum number of rows", Default: "50"}}},

		// ---------------------------------------------------------------- group
		{Name: "group", Use: "group", Short: "Group messaging commands", Phase: "phase4", Implemented: true},
		{Name: "group.create", Use: "create", Short: "Create a group", Phase: "phase4", Implemented: true, Handler: "group.create", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "name", Type: "string", Usage: "Group display name"}, {Name: "group-profile", Type: "string", Usage: "Group profile JSON object"}, {Name: "policy", Type: "string", Usage: "Group policy JSON object (required)"}}},
		{Name: "group.info", Use: "info [group-did]", Short: "Show group info", Phase: "phase4", Implemented: true, Handler: "group.info", NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}, {Name: "include-policy", Type: "bool", Usage: "Include the group policy"}, {Name: "include-members", Type: "bool", Usage: "Include the member list"}}},
		{Name: "group.join", Use: "join [group-did]", Short: "Join an open group", Phase: "phase4", Implemented: true, Handler: "group.join", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}}},
		{Name: "group.add", Use: "add [group-did]", Short: "Add a member to a group", Phase: "phase4", Implemented: true, Handler: "group.add", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}, {Name: "member-did", Type: "string", Usage: "Member DID"}, {Name: "member-handle", Type: "string", Usage: "Member WNS handle"}, {Name: "role", Type: "string", Usage: "Member role (member|admin|owner)"}}},
		{Name: "group.remove", Use: "remove [group-did]", Short: "Remove a member from a group", Phase: "phase4", Implemented: true, Handler: "group.remove", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}, {Name: "member-did", Type: "string", Usage: "Member DID"}}},
		{Name: "group.leave", Use: "leave [group-did]", Short: "Leave a group", Phase: "phase4", Implemented: true, Handler: "group.leave", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}}},
		{Name: "group.profile", Use: "profile [group-did]", Short: "Update the group profile", Phase: "phase4", Implemented: true, Handler: "group.profile", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}, {Name: "patch", Type: "string", Usage: "RFC 7386 JSON merge patch"}}},
		{Name: "group.policy", Use: "policy [group-did]", Short: "Update the group policy", Phase: "phase4", Implemented: true, Handler: "group.policy", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}, {Name: "patch", Type: "string", Usage: "RFC 7386 JSON merge patch"}}},
		{Name: "group.send", Use: "send [group-did]", Short: "Send a group message", Phase: "phase4", Implemented: true, Handler: "group.send", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}, {Name: "text", Type: "string", Usage: "Message text"}, {Name: "payload", Type: "string", Usage: "Message payload JSON object"}, {Name: "mention", Type: "string-array", Usage: "Mention a target (repeatable). Forms: @surface:did:<did>, @surface:agent:<did>, @surface:all|agents|humans, or [role:]kind:value[,range=start:end]"}}},
		{Name: "group.members", Use: "members [group-did]", Short: "List group members", Phase: "phase4", Implemented: true, Handler: "group.members", NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "group", Type: "string", Usage: "Group DID"}}},

		// ---------------------------------------------------------------- attach (P7)
		{Name: "attach", Use: "attach", Short: "Attachment and object transfer commands", Phase: "phase7", Implemented: true},
		{Name: "attach.send", Use: "send", Short: "Send a file as an attachment", Phase: "phase7", Implemented: true, Handler: "attach.send", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "to", Type: "string", Usage: "Direct message target DID or handle"}, {Name: "file", Type: "string", Usage: "Local file path to upload"}, {Name: "text", Type: "string", Usage: "Optional attachment caption"}, {Name: "mime", Type: "string", Usage: "Override the detected MIME type"}}},
		{Name: "attach.download", Use: "download", Short: "Download an attachment by message id", Phase: "phase7", Implemented: true, Handler: "attach.download", NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "message-id", Type: "string", Usage: "Message id of the attachment message"}, {Name: "out", Type: "string", Usage: "Output directory (default: current directory)"}}},

		// ---------------------------------------------------------------- e2ee
		{Name: "e2ee", Use: "e2ee", Short: "End-to-end encryption commands", Phase: "phase3", Implemented: true},
		{Name: "e2ee.init", Use: "init", Short: "Publish the local prekey bundle and register the DID document", Long: "Publishes the active identity's signed prekey bundle and one-time prekeys, and registers its DID document, so peers can establish secure sessions. Idempotent.", Phase: "phase3", Implemented: true, Handler: "e2ee.init", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "e2ee.status", Use: "status --with <did>", Short: "Show E2EE session status with a peer", Phase: "phase3", Implemented: true, Handler: "e2ee.status", NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "with", Type: "string", Usage: "Peer DID", Required: true}}},

		// ---------------------------------------------------------------- runtime
		{Name: "runtime", Use: "runtime", Short: "Receiver and heartbeat commands", Phase: "phase4", Implemented: true},
		{Name: "runtime.listen", Use: "listen", Short: "Start the message receiver", Phase: "phase4", Implemented: true, Handler: "runtime.listen", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "mode", Type: "string", Usage: "Transport mode", Default: "http", Choices: []string{"http", "ws"}}, {Name: "every", Type: "string", Usage: "Poll interval", Default: "15s"}, {Name: "once", Type: "bool", Usage: "Poll once and exit"}}},
		{Name: "runtime.heartbeat", Use: "heartbeat", Short: "Run one heartbeat, or install periodic heartbeats", Phase: "phase4", Implemented: true, Handler: "runtime.heartbeat", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "every", Type: "string", Usage: "Heartbeat interval", Default: "15m"}, {Name: "install", Type: "bool", Usage: "Install a periodic heartbeat (cron-like)"}}},
		{Name: "runtime.install", Use: "install", Short: "Install the receiver as a background service", Phase: "phase4", Implemented: true, Handler: "runtime.install", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "runtime.start", Use: "start", Short: "Start the receiver service", Phase: "phase4", Implemented: true, Handler: "runtime.start", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "runtime.stop", Use: "stop", Short: "Stop the receiver service", Phase: "phase4", Implemented: true, Handler: "runtime.stop", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "runtime.restart", Use: "restart", Short: "Restart the receiver service", Phase: "phase4", Implemented: true, Handler: "runtime.restart", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "runtime.uninstall", Use: "uninstall", Short: "Uninstall the receiver service", Phase: "phase4", Implemented: true, Handler: "runtime.uninstall", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "runtime.status", Use: "status", Short: "Show the receiver service status", Phase: "phase4", Implemented: true, Handler: "runtime.status", NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}},
		{Name: "runtime.listen-service", Use: "listen-service", Short: "Run the receiver under the system service manager", Phase: "phase4", Hidden: true, Implemented: true, Handler: "runtime.listen-service", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},

		// ---------------------------------------------------------------- discovery
		{Name: "discovery", Use: "discovery", Short: "Agent discovery commands", Phase: "phase2", Implemented: true},
		{Name: "discovery.crawl", Use: "crawl <url>", Short: "Fetch an agent's ad.json and interface.json", Phase: "phase2", Implemented: true, Handler: "discovery.crawl", SideEffect: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "url", Type: "string", Usage: "Agent description URL"}}},
		{Name: "discovery.search", Use: "search <query>", Short: "Search crawled agents", Phase: "phase2", Implemented: true, Handler: "discovery.search", Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "query", Type: "string", Usage: "Search query"}, {Name: "limit", Type: "int", Usage: "Maximum results", Default: "20"}}},

		// ---------------------------------------------------------------- proof
		{Name: "proof", Use: "proof", Short: "Sign and verify content", Phase: "phase2", Implemented: true},
		{Name: "proof.sign", Use: "sign <file>", Short: "Sign a file with the active identity", Phase: "phase2", Implemented: true, Handler: "proof.sign", SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "file", Type: "string", Usage: "File to sign"}, {Name: "output", Type: "string", Usage: "Write the proof to a file instead of stdout"}}},
		{Name: "proof.verify", Use: "verify <file>", Short: "Verify a signature over a file", Phase: "phase2", Implemented: true, Handler: "proof.verify", Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "file", Type: "string", Usage: "File to verify"}, {Name: "signature", Type: "string", Usage: "Signature hex or path to a proof JSON file"}, {Name: "did", Type: "string", Usage: "Signer DID (defaults to the active identity)"}}},

		// ---------------------------------------------------------------- shortcuts
		{Name: "setup", Use: "setup", Short: "Start the receiver (shortcut for runtime listen --mode http)", Phase: "phase4", Implemented: true, Handler: "setup", Shortcut: true, SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "register", Use: "register --handle <h>", Short: "Register a handle (shortcut for id register)", Phase: "phase2", Implemented: true, Handler: "register", Shortcut: true, SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}, Flags: []FlagSpec{{Name: "handle", Type: "string", Usage: "Handle local part", Required: true}, {Name: "email", Type: "string", Usage: "Contact email (local metadata)"}, {Name: "phone", Type: "string", Usage: "Contact phone (local metadata)"}}},
		{Name: "whoami", Use: "whoami", Short: "Show the active identity (shortcut for id show)", Phase: "phase1", Implemented: true, Handler: "whoami", Shortcut: true, Outputs: []string{"json", "pretty", "table"}},
		{Name: "inbox", Use: "inbox", Short: "Read the inbox (shortcut for msg inbox)", Phase: "phase2", Implemented: true, Handler: "inbox", Shortcut: true, NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "scope", Type: "string", Usage: "Message scope", Default: "all", Choices: []string{"all", "direct", "group"}}, {Name: "unread", Type: "bool", Usage: "Only unread messages"}, {Name: "limit", Type: "int", Usage: "Maximum number of results", Default: "20"}}},
		{Name: "dm", Use: "dm <did> <text>", Short: "Send a direct message (shortcut for msg send --to)", Phase: "phase2", Implemented: true, Handler: "dm", Shortcut: true, SideEffect: true, NeedsAuth: true, Outputs: []string{"json", "pretty"}},
		{Name: "history", Use: "history <did>", Short: "Read history with a peer (shortcut for msg history)", Phase: "phase2", Implemented: true, Handler: "history", Shortcut: true, NeedsAuth: true, Outputs: []string{"json", "pretty", "table"}, Flags: []FlagSpec{{Name: "limit", Type: "int", Usage: "Maximum number of rows", Default: "50"}}},
	}
}
