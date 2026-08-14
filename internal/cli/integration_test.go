package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eccstartup/anp-cli/internal/testutil/mockbackend"
)

var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "anp-cli-test-bin")
	if err != nil {
		panic(err)
	}
	binaryPath = filepath.Join(dir, "anp-cli")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", binaryPath, "github.com/eccstartup/anp-cli/cmd/anp-cli")
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

// ======================== real server tests ========================
func TestEndToEndAgainstMockBackend(t *testing.T) {
	workspace := t.TempDir()
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	envelope := requireOK(t, runCLI(t, workspace, baseURL, "init", "alice"))
	data, _ := envelope["data"].(map[string]any)
	identity, _ := data["identity"].(map[string]any)
	did, _ := identity["did"].(string)
	if !strings.HasPrefix(did, "did:wba:") {
		t.Fatalf("init did = %q", did)
	}

	whoami := requireOK(t, runCLI(t, workspace, baseURL, "whoami"))
	if _, ok := whoami["meta"]; !ok {
		t.Fatalf("whoami missing meta")
	}

	register := requireOK(t, runCLI(t, workspace, baseURL, "register", "--handle", "alice.example.com"))
	if status, _ := register["data"].(map[string]any)["result"].(map[string]any)["status"].(string); status != "registered" {
		t.Fatalf("register result: %s", "register failed")
	}

	bobWorkspace := t.TempDir()
	requireOK(t, runCLI(t, bobWorkspace, baseURL, "init", "bob"))
	// Bob must publish his prekey bundle + register his DID document so Alice
	// can establish an E2EE session with him.
	requireOK(t, runCLI(t, bobWorkspace, baseURL, "e2ee", "init"))
	bobWhoami := requireOK(t, runCLI(t, bobWorkspace, baseURL, "whoami"))
	bobDID, _ := bobWhoami["data"].(map[string]any)["did"].(string)
	if !strings.HasPrefix(bobDID, "did:wba:") {
		t.Fatalf("bob did = %q", bobDID)
	}
	squat := runCLI(t, bobWorkspace, baseURL, "register", "--handle", "alice.example.com")
	squatEnvelope := parseEnvelope(t, squat.stdout)
	if code, _ := squatEnvelope["error"].(map[string]any)["code"].(string); code != "handle_taken" {
		t.Fatalf("squatted register: %s", string(squat.stdout))
	}

	send := requireOK(t, runCLI(t, workspace, baseURL, "msg", "send", "--to", bobDID, "--text", "hello bob"))
	if send["data"].(map[string]any)["message_id"] == "" {
		t.Fatal("send: no message_id")
	}

	inbox := requireOK(t, runCLI(t, workspace, baseURL, "msg", "inbox", "--scope", "direct"))
	messages, _ := inbox["data"].(map[string]any)["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("inbox: got %d msgs", len(messages))
	}

	history := requireOK(t, runCLI(t, workspace, baseURL, "msg", "history", "--with", bobDID))
	historyMessages, _ := history["data"].(map[string]any)["messages"].([]any)
	if len(historyMessages) != 1 {
		t.Fatalf("history messages = %d, want 1", len(historyMessages))
	}

	dry := runCLI(t, workspace, baseURL, "msg", "send", "--to", bobDID, "--text", "dry", "--dry-run")
	envelope = parseEnvelope(t, dry.stdout)
	if _, hasPlan := envelope["plan"]; !hasPlan {
		t.Fatalf("dry-run envelope missing plan: %s", string(dry.stdout))
	}

	schema := requireOK(t, runCLI(t, workspace, baseURL, "schema"))
	commands, _ := schema["data"].(map[string]any)["commands"].([]any)
	if len(commands) < 15 {
		t.Fatalf("schema commands = %d, want >= 15", len(commands))
	}

	requireOK(t, runCLI(t, workspace, baseURL, "doctor"))
	version := requireOK(t, runCLI(t, workspace, baseURL, "version"))
	if version["data"].(map[string]any)["cli"] != "anp-cli" {
		t.Fatalf("version cli = %v", version["data"])
	}
	requireOK(t, runCLI(t, workspace, baseURL, "status"))

	hello := filepath.Join(workspace, "hello.txt")
	if err := os.WriteFile(hello, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	sign := requireOK(t, runCLI(t, workspace, baseURL, "proof", "sign", hello))
	signature, _ := sign["data"].(map[string]any)["signature"].(string)
	if signature == "" {
		t.Fatalf("sign returned no signature")
	}
	verify := requireOK(t, runCLI(t, workspace, baseURL, "proof", "verify", hello, "--signature", signature))
	if valid, _ := verify["data"].(map[string]any)["valid"].(bool); !valid {
		t.Fatalf("signature not valid: %s", "verify failed")
	}
}

func TestGroupCommandsEndToEnd(t *testing.T) {
	workspace := t.TempDir()
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	requireOK(t, runCLI(t, workspace, baseURL, "init", "alice"))
	bobWorkspace := t.TempDir()
	requireOK(t, runCLI(t, bobWorkspace, baseURL, "init", "bob"))
	bobWhoami := requireOK(t, runCLI(t, bobWorkspace, baseURL, "whoami"))
	bobDID, _ := bobWhoami["data"].(map[string]any)["did"].(string)

	create := requireOK(t, runCLI(t, workspace, baseURL, "group", "create",
		"--name", "dev room",
		"--policy", `{"admission_mode":"open-join","permissions":{}}`))
	groupDID, _ := create["data"].(map[string]any)["group_did"].(string)
	if groupDID == "" {
		t.Fatalf("group create: no group_did in %v", create["data"])
	}

	info := requireOK(t, runCLI(t, workspace, baseURL, "group", "info", "--group", groupDID, "--include-members"))
	if _, ok := info["data"].(map[string]any)["group_profile"]; !ok {
		t.Fatalf("group info missing group_profile")
	}

	requireOK(t, runCLI(t, workspace, baseURL, "group", "add", "--group", groupDID, "--member-did", bobDID))

	members := requireOK(t, runCLI(t, workspace, baseURL, "group", "members", "--group", groupDID))
	memberList, _ := members["data"].(map[string]any)["member_list"].([]any)
	if len(memberList) != 2 {
		t.Fatalf("group members len = %d, want 2", len(memberList))
	}

	send := requireOK(t, runCLI(t, workspace, baseURL, "group", "send", "--group", groupDID, "--text", "hello group"))
	if accepted, _ := send["data"].(map[string]any)["accepted"].(bool); !accepted {
		t.Fatalf("group send not accepted")
	}

	// Bob receives the group message via `msg inbox --scope group`.
	groupInbox := requireOK(t, runCLI(t, bobWorkspace, baseURL, "msg", "inbox", "--scope", "group"))
	groupMsgs, _ := groupInbox["data"].(map[string]any)["messages"].([]any)
	if len(groupMsgs) != 1 {
		t.Fatalf("bob group inbox len = %d, want 1", len(groupMsgs))
	}

	requireOK(t, runCLI(t, workspace, baseURL, "group", "profile", "--group", groupDID, "--patch", `{"description":"new"}`))
	requireOK(t, runCLI(t, workspace, baseURL, "group", "policy", "--group", groupDID, "--patch", `{"admission_mode":"admin-add"}`))

	requireOK(t, runCLI(t, bobWorkspace, baseURL, "group", "join", "--group", groupDID))
	requireOK(t, runCLI(t, workspace, baseURL, "group", "remove", "--group", groupDID, "--member-did", bobDID))
	requireOK(t, runCLI(t, bobWorkspace, baseURL, "group", "leave", "--group", groupDID))
}

func TestMultiIdentityManagement(t *testing.T) {
	workspace := t.TempDir()
	requireOK(t, runCLI(t, workspace, "", "init", "alice"))
	requireOK(t, runCLI(t, workspace, "", "init", "bob"))

	whoami := requireOK(t, runCLI(t, workspace, "", "whoami"))
	if name, _ := whoami["data"].(map[string]any)["name"].(string); name != "alice" {
		t.Fatalf("default identity = %q, want alice", name)
	}

	list := requireOK(t, runCLI(t, workspace, "", "id", "list"))
	rows, _ := list["data"].(map[string]any)["identities"].([]any)
	if len(rows) != 2 {
		t.Fatalf("id list len = %d, want 2", len(rows))
	}

	requireOK(t, runCLI(t, workspace, "", "id", "use", "bob"))
	whoami = requireOK(t, runCLI(t, workspace, "", "whoami"))
	if name, _ := whoami["data"].(map[string]any)["name"].(string); name != "bob" {
		t.Fatalf("after id use bob, default = %q", name)
	}

	current := requireOK(t, runCLI(t, workspace, "", "id", "current"))
	if name, _ := current["data"].(map[string]any)["name"].(string); name != "bob" {
		t.Fatalf("id current = %q, want bob", name)
	}

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

	crawl := requireOK(t, runCLI(t, workspace, "", "discovery", "crawl", adServer.URL+"/ad.json"))
	name, _ := crawl["data"].(map[string]any)["name"].(string)
	if name != "OCR Service" {
		t.Fatalf("crawl name = %q: %s", name, "crawl failed")
	}
	search := requireOK(t, runCLI(t, workspace, "", "discovery", "search", "ocr"))
	agents, _ := search["data"].(map[string]any)["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("search agents = %d, want 1: %s", len(agents), "search failed")
	}
}

func TestAttachmentCommandsEndToEnd(t *testing.T) {
	workspace := t.TempDir()
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	requireOK(t, runCLI(t, workspace, baseURL, "init", "alice"))
	bobWorkspace := t.TempDir()
	requireOK(t, runCLI(t, bobWorkspace, baseURL, "init", "bob"))
	bobWhoami := requireOK(t, runCLI(t, bobWorkspace, baseURL, "whoami"))
	bobDID, _ := bobWhoami["data"].(map[string]any)["did"].(string)

	payload := []byte("attachment payload bytes")
	file := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(file, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	send := requireOK(t, runCLI(t, workspace, baseURL, "attach", "send", "--to", bobDID, "--file", file, "--text", "here is a note"))
	messageID, _ := send["data"].(map[string]any)["message_id"].(string)
	if messageID == "" {
		t.Fatalf("attach send: no message_id in %v", send["data"])
	}

	outDir := filepath.Join(bobWorkspace, "downloads")
	dl := requireOK(t, runCLI(t, bobWorkspace, baseURL, "attach", "download", "--message-id", messageID, "--out", outDir))
	path, _ := dl["data"].(map[string]any)["path"].(string)
	if path == "" {
		t.Fatalf("attach download: no path in %v", dl["data"])
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded bytes mismatch: %q vs %q", got, payload)
	}
}

func TestGroupMentionEndToEnd(t *testing.T) {
	workspace := t.TempDir()
	mock := mockbackend.New()
	baseURL, closeFn, err := mock.Start()
	if err != nil {
		t.Fatalf("mockbackend.Start: %v", err)
	}
	defer closeFn()

	requireOK(t, runCLI(t, workspace, baseURL, "init", "alice"))
	whoami := requireOK(t, runCLI(t, workspace, baseURL, "whoami"))
	aliceDID, _ := whoami["data"].(map[string]any)["did"].(string)

	create := requireOK(t, runCLI(t, workspace, baseURL, "group", "create",
		"--name", "dev room",
		"--policy", `{"admission_mode":"open-join","permissions":{}}`))
	groupDID, _ := create["data"].(map[string]any)["group_did"].(string)

	requireOK(t, runCLI(t, workspace, baseURL, "group", "send", "--group", groupDID,
		"--text", "@alice please review", "--mention", "@alice:"+aliceDID))

	messages := mock.Messages()
	if len(messages) != 1 {
		t.Fatalf("mock stored %d messages, want 1", len(messages))
	}
	payload, _ := messages[0].Body["payload"].(map[string]any)
	mentions, _ := payload["mentions"].([]any)
	if len(mentions) != 1 {
		t.Fatalf("mentions = %v, want 1 entry", payload)
	}
	mention := mentions[0].(map[string]any)
	rng := mention["range"].(map[string]any)
	if rng["start"] != float64(0) || rng["end"] != float64(6) {
		t.Fatalf("mention range = %v, want 0:6", rng)
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
