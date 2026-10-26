package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Level says how much a hook matters.
type Level int

const (
	Required    Level = iota // without it the light is wrong
	Recommended              // fixes a known gap; on by default
	Optional                 // costs something (a process per tool call); opt-in
)

// NotifyMatcher limits the Notification hook to the notification types that
// mean "Claude needs you". Notification also fires for things that don't
// (auth_success, agent_completed, quota_auto_resume_*, plugin notifications),
// and treating those as "waiting" would turn the light blue for no reason.
const NotifyMatcher = "permission_prompt|idle_prompt|elicitation_dialog|agent_needs_input"

// Spec is one Claude Code hook beacon wants.
type Spec struct {
	Hook    string // Claude Code event name, e.g. "Stop"
	Event   string // the `beacon hook <event>` argument
	Matcher string // optional Claude Code matcher
	Level   Level
	Why     string
}

// Specs lists every hook beacon uses, in display order.
func Specs() []Spec {
	return []Spec{
		{"SessionStart", string(SessionStart), "", Required, "a new session starts out ready"},
		{"UserPromptSubmit", string(EvWorking), "", Required, "you sent a prompt: working"},
		{"Notification", string(EvWaiting), NotifyMatcher, Required, "Claude needs you (permission or input): waiting"},
		{"Stop", string(EvReady), "", Required, "Claude finished: ready"},
		{"SessionEnd", string(SessionEnd), "", Required, "the session is gone"},
		{"StopFailure", string(EvReady), "", Recommended, "a turn ended on an API error and Stop never fires, which would leave the light on working until the session times out"},
		{"PreToolUse", string(EvWorking), "", Optional, "re-asserts working after you answer a permission prompt (one beacon process per tool call)"},
	}
}

// Command renders the shell command for a hook: the binary (quoted if it has
// spaces) followed by "hook <event>".
func Command(bin, event string) string {
	if strings.ContainsAny(bin, " \t") {
		bin = `"` + bin + `"`
	}
	return bin + " hook " + event
}

// SnippetOptions selects the optional hooks.
type SnippetOptions struct {
	PreToolUse  bool
	StopFailure bool
}

// Snippet renders the "hooks" block for settings.json. JSON encoding handles
// escaping of backslashes in Windows paths.
func Snippet(bin string, o SnippetOptions) ([]byte, error) {
	type hk struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type group struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []hk   `json:"hooks"`
	}
	hooks := map[string][]group{}
	for _, s := range Specs() {
		switch {
		case s.Hook == "PreToolUse" && !o.PreToolUse, s.Hook == "StopFailure" && !o.StopFailure:
			continue
		}
		hooks[s.Hook] = []group{{Matcher: s.Matcher, Hooks: []hk{{"command", Command(bin, s.Event)}}}}
	}
	return json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
}

// Found is a hook command seen in a settings file.
type Found struct {
	Hook    string // Claude Code event name
	Matcher string
	Command string
}

// ParseSettings lists the command hooks in a Claude Code settings file. It is
// tolerant: other settings, other hook types, and unknown shapes are ignored.
func ParseSettings(data []byte) ([]Found, error) {
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	var out []Found
	for hook, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if (h.Type == "command" || h.Type == "") && h.Command != "" {
					out = append(out, Found{Hook: hook, Matcher: g.Matcher, Command: h.Command})
				}
			}
		}
	}
	return out, nil
}

// fields splits a command line on spaces, keeping "double quoted" parts whole.
func fields(s string) []string {
	var out []string
	var cur strings.Builder
	inQ, any := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQ, any = !inQ, true
		case (r == ' ' || r == '\t') && !inQ:
			if any || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				any = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if any || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// Beacon reports whether the command is `<binary> hook <event>` for a binary
// named like beacon, returning the binary and event.
func (f Found) Beacon() (bin, event string, ok bool) { return f.BeaconWith(nil) }

// BeaconWith is Beacon, but also claims a binary for which isOurs returns true
// (say, the one that's running, in case it was renamed). The event must be one
// beacon accepts and the name or predicate must match, so an unrelated hook
// that merely contains the word "hook" isn't claimed.
func (f Found) BeaconWith(isOurs func(bin string) bool) (bin, event string, ok bool) {
	t := fields(f.Command)
	if len(t) < 3 || t[1] != "hook" {
		return "", "", false
	}
	if _, valid := ParseEvent(t[2]); !valid {
		return "", "", false
	}
	base := strings.ToLower(t[0])
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".exe")
	if !strings.HasPrefix(base, "beacon") && (isOurs == nil || !isOurs(t[0])) {
		return "", "", false
	}
	return t[0], t[2], true
}
