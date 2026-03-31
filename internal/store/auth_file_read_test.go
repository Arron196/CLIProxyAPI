package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestGitTokenStoreReadAuthFileIgnoresNonAuthJSON(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "usage-statistics.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store := &GitTokenStore{}
	auth, err := store.readAuthFile(path, tempDir)
	if err != nil {
		t.Fatalf("readAuthFile() error = %v", err)
	}
	if auth != nil {
		t.Fatalf("readAuthFile() = %#v, want nil", auth)
	}
}

func TestObjectTokenStoreReadAuthFileIgnoresNonAuthJSON(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "usage-statistics.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store := &ObjectTokenStore{}
	auth, err := store.readAuthFile(path, tempDir)
	if err != nil {
		t.Fatalf("readAuthFile() error = %v", err)
	}
	if auth != nil {
		t.Fatalf("readAuthFile() = %#v, want nil", auth)
	}
}

func TestGitTokenStoreReadAuthFileRestoresRuntimeState(t *testing.T) {
	testTokenStoreReadAuthFileRestoresRuntimeState(t, func() authFileReader {
		return &GitTokenStore{}
	})
}

func TestObjectTokenStoreReadAuthFileRestoresRuntimeState(t *testing.T) {
	testTokenStoreReadAuthFileRestoresRuntimeState(t, func() authFileReader {
		return &ObjectTokenStore{}
	})
}

type authFileReader interface {
	readAuthFile(path, baseDir string) (*cliproxyauth.Auth, error)
}

func testTokenStoreReadAuthFileRestoresRuntimeState(t *testing.T, newReader func() authFileReader) {
	t.Helper()

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "codex.json")
	next := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	data := []byte(`{
  "type": "codex",
  "email": "user@example.com",
  "cli_proxy_runtime_state": {
    "status": "error",
    "status_message": "quota exhausted",
    "unavailable": true,
    "next_retry_after": "` + next.Format(time.RFC3339) + `",
    "quota": {
      "exceeded": true,
      "reason": "quota",
      "next_recover_at": "` + next.Format(time.RFC3339) + `",
      "backoff_level": 2,
      "strike_count": 3
    },
    "model_states": {
      "gpt-5.4": {
        "status": "error",
        "status_message": "model quota",
        "unavailable": true,
        "next_retry_after": "` + next.Format(time.RFC3339) + `",
        "quota": {
          "exceeded": true,
          "reason": "quota",
          "next_recover_at": "` + next.Format(time.RFC3339) + `",
          "backoff_level": 4,
          "strike_count": 5
        },
        "last_error": {
          "message": "should be ignored"
        }
      }
    },
    "last_error": {
      "message": "should be ignored"
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	auth, err := newReader().readAuthFile(path, tempDir)
	if err != nil {
		t.Fatalf("readAuthFile() error = %v", err)
	}
	if auth == nil {
		t.Fatal("readAuthFile() returned nil auth")
	}
	if auth.LastError != nil {
		t.Fatalf("auth.LastError = %#v, want nil", auth.LastError)
	}
	if auth.Status != cliproxyauth.StatusError {
		t.Fatalf("auth.Status = %q, want %q", auth.Status, cliproxyauth.StatusError)
	}
	if auth.StatusMessage != "quota exhausted" {
		t.Fatalf("auth.StatusMessage = %q, want %q", auth.StatusMessage, "quota exhausted")
	}
	if !auth.NextRetryAfter.Equal(next) {
		t.Fatalf("auth.NextRetryAfter = %v, want %v", auth.NextRetryAfter, next)
	}
	state := auth.ModelStates["gpt-5.4"]
	if state == nil {
		t.Fatal("expected restored model state for gpt-5.4")
	}
	if state.LastError != nil {
		t.Fatalf("model LastError = %#v, want nil", state.LastError)
	}
	if !state.NextRetryAfter.Equal(next) {
		t.Fatalf("model NextRetryAfter = %v, want %v", state.NextRetryAfter, next)
	}
	if _, ok := auth.Metadata[cliproxyauth.PersistedRuntimeStateMetadataKey]; ok {
		t.Fatal("expected metadata to strip persisted runtime state after restore")
	}
}
