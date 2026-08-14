// P9 message mentions: a group.send payload may carry a structured `mentions`
// array (body.payload.mentions) describing who or what is referenced in the
// message text. Each mention carries a Unicode-code-point range locating the
// surface text (including a leading "@") inside the message text.
package group

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Mention target kinds (ANP P9).
const (
	MentionKindHuman         = "human"
	MentionKindAgent         = "agent"
	MentionKindGroupSelector = "group_selector"
)

// Mention roles (ANP P9). Addressee is the default when omitted.
const (
	MentionRoleAddressee = "addressee"
	MentionRoleCC        = "cc"
)

// MentionSpec is a parsed --mention CLI argument, before range resolution.
type MentionSpec struct {
	Surface  string // surface text (without "@"); when set, the range is located in the message text
	Role     string // "addressee" (default) or "cc"
	Kind     string // human | agent | group_selector
	DID      string // target DID for human/agent
	Selector string // all | agents | humans for group_selector
	Start    *int   // explicit range start (codepoint offset); nil means unset
	End      *int   // explicit range end (codepoint offset); nil means unset
}

// ParseMentionSpec parses a single --mention value.
//
// Accepted forms:
//
//	@surface:did:<did>                  human mention, surface located in text
//	@surface:human:<did>                explicit human mention
//	@surface:agent:<did>                agent mention
//	@surface:all|agents|humans          group selector mention
//	@surface                            group selector (surface = all|agents|humans)
//	[role:]human:<did>                  no surface; ,range=start:end required
//	[role:]agent:<did>
//	[role:]group_selector:<all|agents|humans>
//
// Any form may append `,range=START:END` (codepoint offsets) to override the
// automatically located range.
func ParseMentionSpec(raw string) (MentionSpec, error) {
	spec := MentionSpec{}
	s := strings.TrimSpace(raw)
	if s == "" {
		return spec, fmt.Errorf("empty mention spec")
	}

	// Split off comma-separated options (role=..., range=start:end). DIDs never
	// contain commas in practice; the comma is reserved for options.
	core := s
	options := ""
	if idx := strings.IndexByte(s, ','); idx >= 0 {
		core = s[:idx]
		options = s[idx+1:]
	}
	for _, opt := range strings.Split(options, ",") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		key, value, found := strings.Cut(opt, "=")
		if !found {
			return spec, fmt.Errorf("invalid mention option %q (expected key=value)", opt)
		}
		switch strings.TrimSpace(key) {
		case "role":
			role, err := normalizeRole(strings.TrimSpace(value))
			if err != nil {
				return spec, err
			}
			spec.Role = role
		case "range":
			start, end, err := parseRange(strings.TrimSpace(value))
			if err != nil {
				return spec, err
			}
			spec.Start, spec.End = &start, &end
		default:
			return spec, fmt.Errorf("unknown mention option %q", key)
		}
	}

	// Optional "@surface:" prefix.
	rest := strings.TrimSpace(core)
	if strings.HasPrefix(rest, "@") {
		rest = rest[1:]
		surface, remainder, found := strings.Cut(rest, ":")
		if !found {
			// "@surface" alone: a group selector whose selector is the surface.
			selector, err := normalizeSelector(surface)
			if err != nil {
				return spec, err
			}
			spec.Surface = surface
			spec.Kind = MentionKindGroupSelector
			spec.Selector = selector
			return spec, nil
		}
		spec.Surface = strings.TrimSpace(surface)
		rest = remainder
	}

	if rest == "" {
		return spec, fmt.Errorf("mention target is empty")
	}

	// Optional role prefix token (addressee|cc).
	if role, remainder, found := cutRole(rest); found {
		if spec.Role != "" {
			return spec, fmt.Errorf("mention role specified twice")
		}
		spec.Role = role
		rest = remainder
	}

	// "did:<did>" shorthand defaults to a human target; the DID keeps its
	// leading "did:" prefix (it contains colons).
	if strings.HasPrefix(rest, "did:") {
		spec.Kind = MentionKindHuman
		spec.DID = rest
		return spec, nil
	}
	kind, value, found := strings.Cut(rest, ":")
	if !found {
		// Bare selector keyword (all|agents|humans).
		if selector, err := normalizeSelector(rest); err == nil {
			spec.Kind = MentionKindGroupSelector
			spec.Selector = selector
			return spec, nil
		}
		return spec, fmt.Errorf("mention spec %q must be [@surface:]kind:value", raw)
	}
	kind = strings.TrimSpace(kind)
	value = strings.TrimSpace(value)

	switch kind {
	case MentionKindHuman, MentionKindAgent:
		if value == "" {
			return spec, fmt.Errorf("%s mention requires a DID", kind)
		}
		spec.Kind = kind
		spec.DID = value
	case MentionKindGroupSelector:
		selector, err := normalizeSelector(value)
		if err != nil {
			return spec, err
		}
		spec.Kind = MentionKindGroupSelector
		spec.Selector = selector
	default:
		// Shorthand selectors (all|agents|humans) map to group_selector.
		if selector, err := normalizeSelector(kind); err == nil {
			spec.Kind = MentionKindGroupSelector
			spec.Selector = selector
			break
		}
		return spec, fmt.Errorf("unknown mention kind %q (want human, agent, group_selector, all, agents, humans, or did)", kind)
	}
	return spec, nil
}

// cutRole splits a leading addressee/cc role token from the rest, if present.
func cutRole(s string) (role string, rest string, found bool) {
	token, remainder, hasColon := strings.Cut(s, ":")
	if !hasColon {
		return "", s, false
	}
	if _, err := normalizeRole(strings.TrimSpace(token)); err == nil {
		return strings.TrimSpace(token), remainder, true
	}
	return "", s, false
}

func normalizeRole(role string) (string, error) {
	switch role {
	case "", MentionRoleAddressee:
		return MentionRoleAddressee, nil
	case MentionRoleCC:
		return MentionRoleCC, nil
	default:
		return "", fmt.Errorf("unknown mention_role %q (want addressee or cc)", role)
	}
}

func normalizeSelector(selector string) (string, error) {
	switch selector {
	case "all", "agents", "humans":
		return selector, nil
	default:
		return "", fmt.Errorf("unknown group selector %q (want all, agents, or humans)", selector)
	}
}

func parseRange(value string) (start, end int, err error) {
	startStr, endStr, found := strings.Cut(value, ":")
	if !found {
		return 0, 0, fmt.Errorf("invalid range %q (want start:end)", value)
	}
	start, err = strconv.Atoi(strings.TrimSpace(startStr))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range start %q", startStr)
	}
	end, err = strconv.Atoi(strings.TrimSpace(endStr))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range end %q", endStr)
	}
	return start, end, nil
}

// BuildMentions constructs the standard P9 mentions array from message text
// and parsed specs. Surfaces (with a leading "@") are located in the text and
// their Unicode-code-point ranges computed; specs without a surface must carry
// an explicit range.
func BuildMentions(text string, specs []MentionSpec) ([]map[string]any, error) {
	mentions := make([]map[string]any, 0, len(specs))
	for index, spec := range specs {
		mention, err := BuildMention(text, index, spec)
		if err != nil {
			return nil, err
		}
		mentions = append(mentions, mention)
	}
	return mentions, nil
}

// BuildMention builds a single mention object from a spec and a 1-based index
// used for the message-local id.
func BuildMention(text string, index int, spec MentionSpec) (map[string]any, error) {
	var start, end int
	if spec.Surface != "" {
		locatedStart, locatedEnd, ok := CodepointRange(text, spec.Surface)
		if !ok {
			return nil, fmt.Errorf("surface %q not found in message text", "@"+spec.Surface)
		}
		start, end = locatedStart, locatedEnd
		// Explicit range overrides the auto-located range.
		if spec.Start != nil {
			start = *spec.Start
		}
		if spec.End != nil {
			end = *spec.End
		}
	} else if spec.Start != nil && spec.End != nil {
		start, end = *spec.Start, *spec.End
	} else {
		return nil, fmt.Errorf("mention %d has no valid range (use @surface or ,range=start:end)", index+1)
	}
	if start >= end {
		return nil, fmt.Errorf("mention %d has invalid range %d:%d", index+1, start, end)
	}
	if end > utf8.RuneCountInString(text) {
		return nil, fmt.Errorf("mention %d range end %d exceeds text length %d", index+1, end, utf8.RuneCountInString(text))
	}

	target := map[string]any{"kind": spec.Kind}
	switch spec.Kind {
	case MentionKindGroupSelector:
		target["selector"] = spec.Selector
	case MentionKindHuman, MentionKindAgent:
		target["did"] = spec.DID
	}

	mention := map[string]any{
		"id":     fmt.Sprintf("men_%d", index+1),
		"range":  map[string]any{"start": start, "end": end, "unit": "unicode_code_point"},
		"target": target,
	}
	// mention_role defaults to addressee when omitted; emit it only for cc.
	if spec.Role == MentionRoleCC {
		mention["mention_role"] = MentionRoleCC
	}
	return mention, nil
}

// CodepointRange locates the surface "@"+surface in text and returns its
// inclusive start and exclusive end offsets measured in Unicode code points.
func CodepointRange(text, surface string) (start, end int, ok bool) {
	needle := "@" + surface
	byteIndex := strings.Index(text, needle)
	if byteIndex < 0 {
		return 0, 0, false
	}
	start = utf8.RuneCountInString(text[:byteIndex])
	end = start + utf8.RuneCountInString(needle)
	return start, end, true
}
