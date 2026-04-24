package auth

import (
	"context"
	"testing"
)

func TestExtractCustomHeadersFromMetadata(t *testing.T) {
	headers := ExtractCustomHeadersFromMetadata(map[string]any{
		"headers": map[string]any{
			"X-Test":   " value ",
			"X-Empty":  "   ",
			"":         "skip",
			"X-Number": 123,
		},
	})
	if len(headers) != 1 {
		t.Fatalf("len(headers) = %d, want 1", len(headers))
	}
	if got := headers["X-Test"]; got != "value" {
		t.Fatalf("headers[X-Test] = %q, want %q", got, "value")
	}
}

func TestApplyCustomHeadersFromMetadata(t *testing.T) {
	auth := &Auth{
		Metadata: map[string]any{
			"headers": map[string]any{
				"X-Test": "value",
			},
		},
	}
	ApplyCustomHeadersFromMetadata(auth)
	if got := auth.Attributes["header:X-Test"]; got != "value" {
		t.Fatalf("auth.Attributes[header:X-Test] = %q, want %q", got, "value")
	}
}

func TestManagerRegisterAppliesCustomHeadersFromMetadata(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth := &Auth{
		ID:       "register-headers",
		Provider: "claude",
		Metadata: map[string]any{
			"headers": map[string]any{
				"X-Test": "value",
			},
		},
	}

	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, ok := manager.GetByID(auth.ID)
	if !ok || got == nil {
		t.Fatal("expected auth to be registered")
	}
	if got.Attributes["header:X-Test"] != "value" {
		t.Fatalf("registered auth header = %q, want %q", got.Attributes["header:X-Test"], "value")
	}
}

func TestManagerUpdateAppliesCustomHeadersFromMetadata(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth := &Auth{
		ID:       "update-headers",
		Provider: "claude",
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	updated := auth.Clone()
	updated.Metadata = map[string]any{
		"headers": map[string]any{
			"X-Updated": "next",
		},
	}
	if _, err := manager.Update(context.Background(), updated); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, ok := manager.GetByID(auth.ID)
	if !ok || got == nil {
		t.Fatal("expected auth to remain registered")
	}
	if got.Attributes["header:X-Updated"] != "next" {
		t.Fatalf("updated auth header = %q, want %q", got.Attributes["header:X-Updated"], "next")
	}
}
