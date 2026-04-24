package claude

import "testing"

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
