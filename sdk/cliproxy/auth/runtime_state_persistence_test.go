package auth

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMetadataWithPersistedRuntimeState_RoundTripWithoutLastError(t *testing.T) {
	t.Parallel()

	next := time.Date(2026, 4, 1, 8, 30, 0, 0, time.UTC)
	source := &Auth{
		ID:             "auth-1",
		Provider:       "codex",
		Status:         StatusError,
		StatusMessage:  "quota exhausted",
		Unavailable:    true,
		NextRetryAfter: next,
		Quota: QuotaState{
			Exceeded:      true,
			Reason:        "quota",
			NextRecoverAt: next,
			BackoffLevel:  2,
			StrikeCount:   3,
		},
		LastError: &Error{HTTPStatus: 429, Message: "quota"},
		ModelStates: map[string]*ModelState{
			"gpt-5.4": {
				Status:         StatusError,
				StatusMessage:  "model quota",
				Unavailable:    true,
				NextRetryAfter: next,
				LastError:      &Error{HTTPStatus: 429, Message: "model quota"},
				Quota: QuotaState{
					Exceeded:      true,
					Reason:        "quota",
					NextRecoverAt: next,
					BackoffLevel:  4,
					StrikeCount:   5,
				},
			},
		},
		Metadata: map[string]any{
			"type":  "codex",
			"email": "user@example.com",
		},
	}

	metadata := MetadataWithPersistedRuntimeState(source)
	if metadata == nil {
		t.Fatal("MetadataWithPersistedRuntimeState() = nil, want non-nil")
	}
	if _, ok := source.Metadata[PersistedRuntimeStateMetadataKey]; ok {
		t.Fatal("expected source metadata to remain untouched")
	}

	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(encoded) == "" {
		t.Fatal("expected encoded metadata to be non-empty")
	}
	if contains := jsonContainsField(encoded, "last_error"); contains {
		t.Fatalf("persisted runtime state should not contain last_error: %s", string(encoded))
	}

	restored := &Auth{
		ID:       "auth-1",
		Provider: "codex",
		Metadata: metadata,
	}
	RestorePersistedRuntimeState(restored, next.Add(-time.Minute))

	if restored.LastError != nil {
		t.Fatalf("restored.LastError = %#v, want nil", restored.LastError)
	}
	if restored.Status != StatusError {
		t.Fatalf("restored.Status = %q, want %q", restored.Status, StatusError)
	}
	if restored.StatusMessage != "quota exhausted" {
		t.Fatalf("restored.StatusMessage = %q, want %q", restored.StatusMessage, "quota exhausted")
	}
	if !restored.Unavailable {
		t.Fatal("restored.Unavailable = false, want true")
	}
	if !restored.NextRetryAfter.Equal(next) {
		t.Fatalf("restored.NextRetryAfter = %v, want %v", restored.NextRetryAfter, next)
	}
	if !restored.Quota.NextRecoverAt.Equal(next) {
		t.Fatalf("restored.Quota.NextRecoverAt = %v, want %v", restored.Quota.NextRecoverAt, next)
	}
	state := restored.ModelStates["gpt-5.4"]
	if state == nil {
		t.Fatal("expected restored model state for gpt-5.4")
	}
	if state.LastError != nil {
		t.Fatalf("restored model LastError = %#v, want nil", state.LastError)
	}
	if !state.NextRetryAfter.Equal(next) {
		t.Fatalf("restored model NextRetryAfter = %v, want %v", state.NextRetryAfter, next)
	}
	if _, ok := restored.Metadata[PersistedRuntimeStateMetadataKey]; ok {
		t.Fatal("expected restored metadata to strip persisted runtime key")
	}
}

func TestRestorePersistedRuntimeState_ClearsExpiredCooldown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	source := &Auth{
		ID:             "auth-1",
		Provider:       "codex",
		Status:         StatusError,
		StatusMessage:  "quota exhausted",
		Unavailable:    true,
		NextRetryAfter: expired,
		Quota: QuotaState{
			Exceeded:      true,
			Reason:        "quota",
			NextRecoverAt: expired,
			BackoffLevel:  1,
			StrikeCount:   2,
		},
		ModelStates: map[string]*ModelState{
			"gpt-5.4": {
				Status:         StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: expired,
				Quota: QuotaState{
					Exceeded:      true,
					Reason:        "quota",
					NextRecoverAt: expired,
					BackoffLevel:  1,
					StrikeCount:   2,
				},
			},
		},
		Metadata: map[string]any{"type": "codex"},
	}

	metadata := MetadataWithPersistedRuntimeState(source)
	restored := &Auth{
		ID:       "auth-1",
		Provider: "codex",
		Metadata: metadata,
	}
	RestorePersistedRuntimeState(restored, now)

	if restored.Unavailable {
		t.Fatal("restored.Unavailable = true, want false after expired cooldown")
	}
	if !restored.NextRetryAfter.IsZero() {
		t.Fatalf("restored.NextRetryAfter = %v, want zero", restored.NextRetryAfter)
	}
	if restored.Status != StatusActive {
		t.Fatalf("restored.Status = %q, want %q", restored.Status, StatusActive)
	}
	if restored.StatusMessage != "" {
		t.Fatalf("restored.StatusMessage = %q, want empty", restored.StatusMessage)
	}
	if restored.Quota.Exceeded || !restored.Quota.NextRecoverAt.IsZero() {
		t.Fatalf("restored.Quota = %#v, want cleared quota state", restored.Quota)
	}
	state := restored.ModelStates["gpt-5.4"]
	if state == nil {
		t.Fatal("expected restored model state for gpt-5.4")
	}
	if state.Status != StatusActive {
		t.Fatalf("restored model Status = %q, want %q", state.Status, StatusActive)
	}
	if state.Unavailable {
		t.Fatal("restored model Unavailable = true, want false")
	}
	if !state.NextRetryAfter.IsZero() {
		t.Fatalf("restored model NextRetryAfter = %v, want zero", state.NextRetryAfter)
	}
}

func jsonContainsField(data []byte, field string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return false
	}
	return mapContainsField(decoded, field)
}

func mapContainsField(data map[string]any, field string) bool {
	for key, value := range data {
		if key == field {
			return true
		}
		switch nested := value.(type) {
		case map[string]any:
			if mapContainsField(nested, field) {
				return true
			}
		}
	}
	return false
}
