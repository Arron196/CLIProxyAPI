package claude

import (
	"encoding/base64"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/tidwall/gjson"
)

func TestHasValidClaudeSignature(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sig  string
		want bool
	}{
		{name: "empty", sig: "", want: false},
		{name: "plain hex", sig: "d5cb9cd0823142109f451861", want: false},
		{name: "E prefixed", sig: "Eabc123", want: true},
		{name: "R prefixed", sig: "Rabc123", want: true},
		{name: "cache prefixed E", sig: "claude#Eabc123", want: true},
		{name: "cache prefixed invalid", sig: "claude#d5cb9cd0823142109f451861", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hasValidClaudeSignature(tt.sig); got != tt.want {
				t.Fatalf("hasValidClaudeSignature(%q) = %v, want %v", tt.sig, got, tt.want)
			}
		})
	}
}

func TestStripInvalidSignatureThinkingBlocks_PreservesEPrefixedFakeSignature(t *testing.T) {
	t.Parallel()

	input := []byte(`{
		"messages": [{
			"role": "assistant",
			"content": [
				{"type":"thinking","thinking":"kept","signature":"E////"},
				{"type":"text","text":"hello"}
			]
		}]
	}`)

	out := StripInvalidSignatureThinkingBlocks(input)
	if string(out) != string(input) {
		t.Fatalf("StripInvalidSignatureThinkingBlocks() should preserve E-prefixed signature block")
	}
}

func TestNormalizeClaudeBypassSignature_NormalizesSingleLayerToRForm(t *testing.T) {
	previousCache := cache.SignatureCacheEnabled()
	previousStrict := cache.SignatureBypassStrictMode()
	cache.SetSignatureCacheEnabled(false)
	cache.SetSignatureBypassStrictMode(false)
	t.Cleanup(func() {
		cache.SetSignatureCacheEnabled(previousCache)
		cache.SetSignatureBypassStrictMode(previousStrict)
	})

	raw := base64.StdEncoding.EncodeToString([]byte{0x12})
	normalized, err := normalizeClaudeBypassSignature("claude#" + raw)
	if err != nil {
		t.Fatalf("normalizeClaudeBypassSignature error: %v", err)
	}
	want := base64.StdEncoding.EncodeToString([]byte(raw))
	if normalized != want {
		t.Fatalf("normalized = %q, want %q", normalized, want)
	}
}

func TestValidateClaudeBypassSignatures_StrictRejectsMalformedTree(t *testing.T) {
	previousCache := cache.SignatureCacheEnabled()
	previousStrict := cache.SignatureBypassStrictMode()
	cache.SetSignatureCacheEnabled(false)
	cache.SetSignatureBypassStrictMode(true)
	t.Cleanup(func() {
		cache.SetSignatureCacheEnabled(previousCache)
		cache.SetSignatureBypassStrictMode(previousStrict)
	})

	raw := base64.StdEncoding.EncodeToString([]byte{0x12})
	input := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"bad","signature":"` + raw + `"}]}]}`)

	if err := ValidateClaudeBypassSignatures(input); err == nil {
		t.Fatal("expected strict validation to reject malformed protobuf tree")
	}
}

func TestConvertClaudeRequestToAntigravity_BypassUsesClientSignature(t *testing.T) {
	previousCache := cache.SignatureCacheEnabled()
	previousStrict := cache.SignatureBypassStrictMode()
	cache.SetSignatureCacheEnabled(false)
	cache.SetSignatureBypassStrictMode(false)
	t.Cleanup(func() {
		cache.SetSignatureCacheEnabled(previousCache)
		cache.SetSignatureBypassStrictMode(previousStrict)
	})

	rawSignature := base64.StdEncoding.EncodeToString([]byte{0x12})
	input := []byte(`{
		"model": "claude-sonnet-4-6",
		"messages": [{
			"role": "assistant",
			"content": [
				{"type":"thinking","thinking":"kept","signature":"` + rawSignature + `"},
				{"type":"text","text":"done"}
			]
		}]
	}`)

	out := ConvertClaudeRequestToAntigravity("claude-sonnet-4-6", input, false)
	got := gjson.GetBytes(out, "request.contents.0.parts.0.thoughtSignature").String()
	want := base64.StdEncoding.EncodeToString([]byte(rawSignature))
	if got != want {
		t.Fatalf("thoughtSignature = %q, want normalized %q; output=%s", got, want, string(out))
	}
}
