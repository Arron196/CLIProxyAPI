package claude

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// StripInvalidSignatureThinkingBlocks removes Claude thinking blocks whose
// signatures are empty or not in Claude's expected envelope format.
// Valid Claude signatures start with 'E' or 'R' after stripping any optional
// cache/model-group prefix like "claude#".
func StripInvalidSignatureThinkingBlocks(payload []byte) []byte {
	messages := gjson.GetBytes(payload, "messages")
	if !messages.IsArray() {
		return payload
	}

	modified := false
	for i, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.IsArray() {
			continue
		}

		var kept []string
		stripped := false
		for _, part := range content.Array() {
			if part.Get("type").String() == "thinking" && !hasValidClaudeSignature(part.Get("signature").String()) {
				stripped = true
				continue
			}
			kept = append(kept, part.Raw)
		}
		if !stripped {
			continue
		}
		modified = true
		if len(kept) == 0 {
			payload, _ = sjson.SetRawBytes(payload, fmt.Sprintf("messages.%d.content", i), []byte("[]"))
			continue
		}
		payload, _ = sjson.SetRawBytes(payload, fmt.Sprintf("messages.%d.content", i), []byte("["+strings.Join(kept, ",")+"]"))
	}

	if !modified {
		return payload
	}
	return payload
}

func hasValidClaudeSignature(sig string) bool {
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return false
	}
	if idx := strings.IndexByte(sig, '#'); idx >= 0 {
		sig = strings.TrimSpace(sig[idx+1:])
	}
	if sig == "" {
		return false
	}
	return sig[0] == 'E' || sig[0] == 'R'
}
