package management

import (
	"path/filepath"
	"testing"
)

func TestBuildAuthFromFileDataAppliesCustomHeadersFromMetadata(t *testing.T) {
	h := &Handler{}
	path := filepath.Join(t.TempDir(), "test.json")
	data := []byte(`{
		"type": "codex",
		"email": "user@example.com",
		"headers": {
			"X-Test": "value"
		}
	}`)

	auth, err := h.buildAuthFromFileData(path, data)
	if err != nil {
		t.Fatalf("buildAuthFromFileData() error = %v", err)
	}

	if got := auth.Attributes["header:X-Test"]; got != "value" {
		t.Fatalf("auth.Attributes[header:X-Test] = %q, want %q", got, "value")
	}
}
