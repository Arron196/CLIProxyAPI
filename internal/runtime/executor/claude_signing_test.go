package executor

import (
	"bytes"
	"testing"

	"github.com/tidwall/gjson"
)

func TestSignAnthropicMessagesBody_RewritesBillingCCH(t *testing.T) {
	body := []byte(`{
		"system": [
			{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63.abc; cc_entrypoint=cli; cch=12345;"},
			{"type":"text","text":"keep me"}
		],
		"messages": [{"role":"user","content":[{"type":"text","text":"hello"}]}]
	}`)

	signed := signAnthropicMessagesBody(body)
	header := gjson.GetBytes(signed, "system.0.text").String()
	if header == gjson.GetBytes(body, "system.0.text").String() {
		t.Fatalf("billing header was not signed: %s", header)
	}
	if !claudeBillingHeaderCCHPattern.MatchString(header) {
		t.Fatalf("signed header missing cch: %s", header)
	}
	if got := gjson.GetBytes(signed, "system.1.text").String(); got != "keep me" {
		t.Fatalf("system.1.text = %q, want preserved", got)
	}
}

func TestSignAnthropicMessagesBody_LeavesMissingBillingHeaderUnchanged(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"ordinary system"}],"messages":[]}`)
	signed := signAnthropicMessagesBody(body)
	if !bytes.Equal(signed, body) {
		t.Fatalf("body changed without billing header: %s", string(signed))
	}
}

func TestSignAnthropicMessagesBody_LeavesMalformedCCHUnchanged(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63.abc; cc_entrypoint=cli; cch=xyz;"}],"messages":[]}`)
	signed := signAnthropicMessagesBody(body)
	if !bytes.Equal(signed, body) {
		t.Fatalf("body changed with malformed cch: %s", string(signed))
	}
}
