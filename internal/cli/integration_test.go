package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ANPWorld/anp-cli/internal/testutil/mockbackend"
)

var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "anp-cli-test-bin")
	if err != nil {
		panic(err)
	}
	binaryPath = filepath.Join(dir, "anp")
	build := exec.Command("go", "build", "-o", binaryPath, "github.com/ANPWorld/anp-cli/cmd/anp-cli")
	if out, err := build.CombinedOutput(); err != nil {
		panic("build anp-cli binary: " + err.Error() + ": " + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type runResult struct {
	stdout []byte
	err    error
}

func runCLI(t *testing.T, workspace string, backend string, args ...string) runResult {
	t.Helper()
	command := exec.Command(binaryPath, args...)
	command.Env = append(os.Environ(), "ANP_WORKSPACE="+workspace)
	if backend != "" {
		command.Env = append(command.Env, "ANP_BACKEND="+backend)
	}
	out, err := command.CombinedOutput()
	return runResult{stdout: out, err: err}
}

func parseEnvelope(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("invalid envelope %s: %v", string(raw), err)
	}
	return envelope
}

func requireOK(t *testing.T, result runResult) map[string]any {
	t.Helper()
	envelope := parseEnvelope(t, result.stdout)
	if ok, _ := envelope["ok"].(bool); !ok {
		t.Fatalf("command failed: %s", string(result.stdout))
	}
	return envelope
}

func TestEndToEndAgainstMockBackend(t *testing.T) {
	workspace := t.TempDir()
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	// init
	envelope := requireOK(t, runCLI(t, workspace, baseURL, "init", "alice"))
	data, _ := envelope["data"].(map[string]any)
	identity, _ := data["identity"].(map[string]any)
	did, _ := identity["did"].(string)
	if !strings.HasPrefix(did, "did:wba:") {
		t.Fatalf("init did = %q", did)
	}

	// whoami (shortcut, pretty default)
	whoami := requireOK(t, runCLI(t, workspace, baseURL, "whoami"))
	if _, ok := whoami["meta"]; !ok {
		t.Fatalf("whoami missing meta")
	}

	// register handle
	registerResult := runCLI(t, workspace, baseURL, "register", "--handle", "alice.agent")
	register := requireOK(t, registerResult)
	if status, _ := register["data"].(map[string]any)["result"].(map[string]any)["status"].(string); status != "registered" {
		t.Fatalf("register result: %s", string(registerResult.stdout))
	}

	// squatting: a second identity trying the same handle must get handle_taken.
	bobWorkspace := t.TempDir()
	requireOK(t, runCLI(t, bobWorkspace, baseURL, "init", "bob"))
	squat := runCLI(t, bobWorkspace, baseURL, "register", "--handle", "alice.agent")
	squatEnvelope := parseEnvelope(t, squat.stdout)
	if code, _ := squatEnvelope["error"].(map[string]any)["code"].(string); code != "handle_taken" {
		t.Fatalf("squatted register error code = %v, want handle_taken: %s", code, string(squat.stdout))
	}
	hint, _ := squatEnvelope["error"].(map[string]any)["hint"].(string)
	if !strings.Contains(hint, "alice.agent.1") {
		t.Fatalf("squatted register hint should suggest variants: %s", hint)
	}

	// send a direct message
	sendResult := runCLI(t, workspace, baseURL, "msg", "send", "--to", "did:wba:example.com:agent:bob", "--text", "hello bob")
	send := requireOK(t, sendResult)
	sent, _ := send["data"].(map[string]any)
	if sent["message_id"] == "" {
		t.Fatalf("send returned no message_id: %s", string(sendResult.stdout))
	}

	// inbox reads the outbound message locally
	inboxResult := runCLI(t, workspace, baseURL, "msg", "inbox", "--scope", "direct")
	inbox := requireOK(t, inboxResult)
	messages, _ := inbox["data"].(map[string]any)["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("inbox messages = %d, want 1: %s", len(messages), string(inboxResult.stdout))
	}

	// history with the peer
	history := requireOK(t, runCLI(t, workspace, baseURL, "msg", "history", "--with", "did:wba:example.com:agent:bob"))
	historyMessages, _ := history["data"].(map[string]any)["messages"].([]any)
	if len(historyMessages) != 1 {
		t.Fatalf("history messages = %d, want 1", len(historyMessages))
	}

	// dry-run send returns a plan
	dry := runCLI(t, workspace, baseURL, "msg", "send", "--to", "did:wba:example.com:agent:bob", "--text", "dry", "--dry-run")
	envelope = parseEnvelope(t, dry.stdout)
	if dry.err != nil {
		t.Fatalf("dry-run should succeed: %v", dry.err)
	}
	if _, hasPlan := envelope["plan"]; !hasPlan {
		t.Fatalf("dry-run envelope missing plan: %s", string(dry.stdout))
	}

	// group lifecycle
	groupResult := runCLI(t, workspace, baseURL, "group", "create", "--name", "team")
	group := requireOK(t, groupResult)
	groupDID, _ := group["data"].(map[string]any)["group_did"].(string)
	if groupDID == "" {
		t.Fatalf("group create returned no group_did: %s", string(groupResult.stdout))
	}
	requireOK(t, runCLI(t, workspace, baseURL, "group", "join", "--group", groupDID))
	requireOK(t, runCLI(t, workspace, baseURL, "group", "members", "--group", groupDID))
	requireOK(t, runCLI(t, workspace, baseURL, "group", "leave", "--group", groupDID))

	// schema
	schema := requireOK(t, runCLI(t, workspace, baseURL, "schema"))
	commands, _ := schema["data"].(map[string]any)["commands"].([]any)
	if len(commands) < 20 {
		t.Fatalf("schema commands = %d, want >= 20", len(commands))
	}

	// doctor + version + status
	requireOK(t, runCLI(t, workspace, baseURL, "doctor"))
	version := requireOK(t, runCLI(t, workspace, baseURL, "version"))
	if version["data"].(map[string]any)["cli"] != "anp-cli" {
		t.Fatalf("version cli = %v", version["data"])
	}
	requireOK(t, runCLI(t, workspace, baseURL, "status"))

	// proof sign + verify
	hello := filepath.Join(workspace, "hello.txt")
	if err := os.WriteFile(hello, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	sign := requireOK(t, runCLI(t, workspace, baseURL, "proof", "sign", hello))
	signature, _ := sign["data"].(map[string]any)["signature"].(string)
	if signature == "" {
		t.Fatalf("sign returned no signature")
	}
	verifyResult := runCLI(t, workspace, baseURL, "proof", "verify", hello, "--signature", signature)
	verify := requireOK(t, verifyResult)
	if valid, _ := verify["data"].(map[string]any)["valid"].(bool); !valid {
		t.Fatalf("signature not valid: %s", string(verifyResult.stdout))
	}
}

func TestMultiIdentityManagement(t *testing.T) {
	workspace := t.TempDir()
	requireOK(t, runCLI(t, workspace, "", "init", "alice"))
	requireOK(t, runCLI(t, workspace, "", "init", "bob"))

	// Default stays with the first identity (init does not hijack it).
	whoami := requireOK(t, runCLI(t, workspace, "", "whoami"))
	if name, _ := whoami["data"].(map[string]any)["name"].(string); name != "alice" {
		t.Fatalf("default identity = %q, want alice", name)
	}

	// id list shows both, with the current marker on alice.
	list := requireOK(t, runCLI(t, workspace, "", "id", "list"))
	rows, _ := list["data"].(map[string]any)["identities"].([]any)
	if len(rows) != 2 {
		t.Fatalf("id list len = %d, want 2", len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if first["current"] != true || first["name"] != "alice" {
		t.Fatalf("unexpected first row: %+v", first)
	}

	// id use switches the persistent default.
	requireOK(t, runCLI(t, workspace, "", "id", "use", "bob"))
	whoami = requireOK(t, runCLI(t, workspace, "", "whoami"))
	if name, _ := whoami["data"].(map[string]any)["name"].(string); name != "bob" {
		t.Fatalf("after id use bob, default = %q", name)
	}

	// id current reflects the switch.
	current := requireOK(t, runCLI(t, workspace, "", "id", "current"))
	if name, _ := current["data"].(map[string]any)["name"].(string); name != "bob" {
		t.Fatalf("id current = %q, want bob", name)
	}

	// --identity still selects the other one per-command.
	selected := requireOK(t, runCLI(t, workspace, "", "whoami", "--identity", "alice"))
	if name, _ := selected["data"].(map[string]any)["name"].(string); name != "alice" {
		t.Fatalf("--identity alice gave %q", name)
	}
}

func TestDiscoveryCrawlAndSearch(t *testing.T) {
	workspace := t.TempDir()
	adServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "ad.json"):
			_, _ = w.Write([]byte(`{"name":"OCR Service","description":"converts images","capabilities":["ocr","vision"],"interfaces":[{"name":"ocr"}]}`))
		default:
			_, _ = w.Write([]byte(`{"interfaces":[{"name":"ocr","schema":{}}]}`))
		}
	}))
	defer adServer.Close()

	crawlResult := runCLI(t, workspace, "", "discovery", "crawl", adServer.URL+"/ad.json")
	crawl := requireOK(t, crawlResult)
	name, _ := crawl["data"].(map[string]any)["name"].(string)
	if name != "OCR Service" {
		t.Fatalf("crawl name = %q: %s", name, string(crawlResult.stdout))
	}
	searchResult := runCLI(t, workspace, "", "discovery", "search", "ocr")
	search := requireOK(t, searchResult)
	agents, _ := search["data"].(map[string]any)["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("search agents = %d, want 1: %s", len(agents), string(searchResult.stdout))
	}
}

func TestInitDryRunDoesNotMutate(t *testing.T) {
	workspace := t.TempDir()
	envelope := requireOK(t, runCLI(t, workspace, "", "init", "--dry-run"))
	if _, hasPlan := envelope["plan"]; !hasPlan {
		t.Fatalf("dry-run init missing plan: %v", envelope)
	}
	if _, err := os.Stat(filepath.Join(workspace, "identities")); !os.IsNotExist(err) {
		t.Fatalf("dry-run mutated workspace")
	}
}
