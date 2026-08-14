package group

import "testing"

func TestParseMentionSpecSurfaceHuman(t *testing.T) {
	spec, err := ParseMentionSpec("@alice:did:wba:example.com:user:alice")
	if err != nil {
		t.Fatalf("ParseMentionSpec: %v", err)
	}
	if spec.Surface != "alice" {
		t.Fatalf("surface = %q, want alice", spec.Surface)
	}
	if spec.Kind != MentionKindHuman {
		t.Fatalf("kind = %q, want human", spec.Kind)
	}
	if spec.DID != "did:wba:example.com:user:alice" {
		t.Fatalf("did = %q", spec.DID)
	}
}

func TestParseMentionSpecRolePrefix(t *testing.T) {
	spec, err := ParseMentionSpec("cc:human:did:wba:example.com:user:bob")
	if err != nil {
		t.Fatalf("ParseMentionSpec: %v", err)
	}
	if spec.Role != MentionRoleCC {
		t.Fatalf("role = %q, want cc", spec.Role)
	}
	if spec.Kind != MentionKindHuman {
		t.Fatalf("kind = %q, want human", spec.Kind)
	}
	if spec.DID != "did:wba:example.com:user:bob" {
		t.Fatalf("did = %q", spec.DID)
	}
}

func TestParseMentionSpecAgentAndSelector(t *testing.T) {
	agent, err := ParseMentionSpec("@bot:agent:did:wba:example.com:agent:product")
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	if agent.Kind != MentionKindAgent || agent.DID != "did:wba:example.com:agent:product" {
		t.Fatalf("agent spec = %+v", agent)
	}

	selector, err := ParseMentionSpec("group_selector:agents")
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if selector.Kind != MentionKindGroupSelector || selector.Selector != "agents" {
		t.Fatalf("selector spec = %+v", selector)
	}

	shorthand, err := ParseMentionSpec("@all:all")
	if err != nil {
		t.Fatalf("shorthand: %v", err)
	}
	if shorthand.Kind != MentionKindGroupSelector || shorthand.Selector != "all" || shorthand.Surface != "all" {
		t.Fatalf("shorthand spec = %+v", shorthand)
	}
}

func TestParseMentionSpecSurfaceOnlySelector(t *testing.T) {
	spec, err := ParseMentionSpec("@agents")
	if err != nil {
		t.Fatalf("ParseMentionSpec: %v", err)
	}
	if spec.Kind != MentionKindGroupSelector || spec.Selector != "agents" || spec.Surface != "agents" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestParseMentionSpecExplicitRange(t *testing.T) {
	spec, err := ParseMentionSpec("agent:did:wba:example.com:agent:bot,range=5:9,role=cc")
	if err != nil {
		t.Fatalf("ParseMentionSpec: %v", err)
	}
	if spec.Start == nil || spec.End == nil || *spec.Start != 5 || *spec.End != 9 {
		t.Fatalf("range = %v:%v, want 5:9", spec.Start, spec.End)
	}
	if spec.Role != MentionRoleCC {
		t.Fatalf("role = %q, want cc", spec.Role)
	}
	if spec.Kind != MentionKindAgent {
		t.Fatalf("kind = %q, want agent", spec.Kind)
	}
}

func TestParseMentionSpecInvalid(t *testing.T) {
	for _, raw := range []string{"", "human:", "bogus:value", "human:did:x,range=bad", "group_selector:nope"} {
		if _, err := ParseMentionSpec(raw); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestBuildMentionsRanges(t *testing.T) {
	text := "@alice please review with @张三 too"
	specs := []MentionSpec{
		{Surface: "alice", Kind: MentionKindHuman, DID: "did:wba:example.com:user:alice"},
		{Surface: "张三", Kind: MentionKindHuman, DID: "did:wba:example.com:user:zhang"},
	}
	mentions, err := BuildMentions(text, specs)
	if err != nil {
		t.Fatalf("BuildMentions: %v", err)
	}
	if len(mentions) != 2 {
		t.Fatalf("len = %d, want 2", len(mentions))
	}
	first := mentions[0]
	rng := first["range"].(map[string]any)
	if rng["start"] != 0 || rng["end"] != 6 {
		t.Fatalf("first range = %v, want start 0 end 6", rng)
	}
	if unit := rng["unit"]; unit != "unicode_code_point" {
		t.Fatalf("unit = %v", unit)
	}
	target := first["target"].(map[string]any)
	if target["kind"] != MentionKindHuman || target["did"] != "did:wba:example.com:user:alice" {
		t.Fatalf("first target = %v", target)
	}

	second := mentions[1]
	rng2 := second["range"].(map[string]any)
	// "@张三" is 3 code points; text is "@alice please review with @张三 too".
	// "@alice please review with " is 26 code points, so start=26, end=29.
	if rng2["start"] != 26 || rng2["end"] != 29 {
		t.Fatalf("second range = %v, want start 26 end 29", rng2)
	}
}

func TestBuildMentionsExplicitRangeNoSurface(t *testing.T) {
	text := "hello world"
	start, end := 0, 5
	specs := []MentionSpec{
		{Kind: MentionKindHuman, DID: "did:wba:example.com:user:alice", Start: &start, End: &end},
	}
	mentions, err := BuildMentions(text, specs)
	if err != nil {
		t.Fatalf("BuildMentions: %v", err)
	}
	rng := mentions[0]["range"].(map[string]any)
	if rng["start"] != 0 || rng["end"] != 5 {
		t.Fatalf("range = %v", rng)
	}
}

func TestBuildMentionsSurfaceNotFound(t *testing.T) {
	specs := []MentionSpec{
		{Surface: "ghost", Kind: MentionKindHuman, DID: "did:wba:example.com:user:ghost"},
	}
	if _, err := BuildMentions("hello", specs); err == nil {
		t.Fatalf("expected error for missing surface")
	}
}

func TestCodepointRangeUnicode(t *testing.T) {
	// "@张三" -> 3 code points after a CJK-free prefix.
	start, end, ok := CodepointRange("hi @张三 there", "张三")
	if !ok {
		t.Fatalf("not found")
	}
	if start != 3 || end != 6 {
		t.Fatalf("range = %d:%d, want 3:6", start, end)
	}
}
