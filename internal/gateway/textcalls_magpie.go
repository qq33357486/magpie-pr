package gateway

import (
	"encoding/json"
	"strings"
)

// The fork's own text-written call, next to DeepSeek's and Hermes': a model
// told the conversation again the way the gateway tells a run of it writes
// its calls the way it reads them made — "[tool call name id=… args={…}]"
// (renderParts, claude_subscription.go; Droid over the subscription
// bridge, #2486) — and its next call the same way instead of making it.
// magpieCall reads one, saying how much of s it took.
func magpieCall(s string, names map[string]bool) (writtenCall, int, bool) {
	name, rest, ok := strings.Cut(s[len("[tool call "):], " ")
	if !ok {
		return writtenCall{}, 0, false
	}
	if n, bridge := strings.CutPrefix(name, "mcp__magpie__"); bridge {
		name = n
	}
	if !names[name] {
		return writtenCall{}, 0, false
	}
	if id, r, ok := strings.Cut(rest, " "); ok && strings.HasPrefix(id, "id=") {
		rest = r
	}
	args, ok := strings.CutPrefix(rest, "args=")
	if !ok {
		return writtenCall{}, 0, false
	}
	dec := json.NewDecoder(strings.NewReader(args))
	dec.UseNumber()
	var v map[string]any
	if dec.Decode(&v) != nil {
		return writtenCall{}, 0, false
	}
	n := len(s) - len(args) + int(dec.InputOffset()) // past the arguments
	tail := strings.TrimLeft(s[n:], " \t\r\n")
	if !strings.HasPrefix(tail, "]") {
		return writtenCall{}, 0, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return writtenCall{}, 0, false
	}
	return writtenCall{Name: name, Args: b}, len(s) - len(tail) + 1, true
}
