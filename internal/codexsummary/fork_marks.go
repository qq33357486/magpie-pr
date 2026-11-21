package codexsummary

import (
	"encoding/base64"
	"strings"
)

// This fork's compaction carries a mark of its own. A rollout is a file
// a customer can open, and the stack's name must not reach a customer
// (fork_errors' masking): the gateway writes its compaction items with
// ForkMark ("gw1:") in their encrypted_content, as every fork build
// since the masking began has. Mark ("magpie1:") is what this fork's own
// earlier builds wrote, and what upstream writes: written never here,
// read always, so a conversation held over an upgrade keeps working.
const ForkMark = "gw1:"

// ForkEncrypted is the compaction item's encrypted_content this fork
// writes for a summary.
func ForkEncrypted(summary string) string {
	return ForkMark + base64.StdEncoding.EncodeToString([]byte(summary))
}

// ForkSummary is Summary for either mark: this fork's first, then
// upstream's.
func ForkSummary(encrypted string) (summary string, ours, ok bool) {
	if b64, isOurs := strings.CutPrefix(encrypted, ForkMark); isOurs {
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", true, false
		}
		return string(b), true, true
	}
	return Summary(encrypted)
}

// ForkMessage is Message for either mark: this fork's first, then
// upstream's.
func ForkMessage(encrypted string) (msg map[string]any, ours bool) {
	summary, ours, ok := ForkSummary(encrypted)
	if !ok {
		return nil, ours
	}
	return UserMessage(Prefix + "\n" + summary), true
}

// ForkWritten says whether encrypted_content carries either mark: what
// this stack wrote, as a scan that reads no summary does.
func ForkWritten(encrypted string) bool {
	return strings.HasPrefix(encrypted, ForkMark) || strings.HasPrefix(encrypted, Mark)
}
