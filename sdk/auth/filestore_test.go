package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestExtractAccessToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata map[string]any
		expected string
	}{
		{
			"antigravity top-level access_token",
			map[string]any{"access_token": "tok-abc"},
			"tok-abc",
		},
		{
			"gemini nested token.access_token",
			map[string]any{
				"token": map[string]any{"access_token": "tok-nested"},
			},
			"tok-nested",
		},
		{
			"top-level takes precedence over nested",
			map[string]any{
				"access_token": "tok-top",
				"token":        map[string]any{"access_token": "tok-nested"},
			},
			"tok-top",
		},
		{
			"empty metadata",
			map[string]any{},
			"",
		},
		{
			"whitespace-only access_token",
			map[string]any{"access_token": "   "},
			"",
		},
		{
			"wrong type access_token",
			map[string]any{"access_token": 12345},
			"",
		},
		{
			"token is not a map",
			map[string]any{"token": "not-a-map"},
			"",
		},
		{
			"nested whitespace-only",
			map[string]any{
				"token": map[string]any{"access_token": "  "},
			},
			"",
		},
		{
			"fallback to nested when top-level empty",
			map[string]any{
				"access_token": "",
				"token":        map[string]any{"access_token": "tok-fallback"},
			},
			"tok-fallback",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractAccessToken(tt.metadata)
			if got != tt.expected {
				t.Errorf("extractAccessToken() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFileTokenStoreReadAuthFile_HydratesGeminiProjectID(t *testing.T) {
	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "gemini-auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"type":"gemini",
		"email":"user@example.com",
		"token":{
			"refresh_token":"refresh-token",
			"client_id":"client-id",
			"client_secret":"client-secret",
			"token_uri":"https://oauth2.googleapis.com/token"
		}
	}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prevClient := fileTokenStoreHTTPClient
	prevFetch := fileTokenStoreFetchProjectIDFn
	t.Cleanup(func() {
		fileTokenStoreHTTPClient = prevClient
		fileTokenStoreFetchProjectIDFn = prevFetch
	})

	fileTokenStoreHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost || req.URL.String() != "https://oauth2.googleapis.com/token" {
				t.Fatalf("unexpected refresh request: %s %s", req.Method, req.URL.String())
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("ReadAll(request body) error = %v", err)
			}
			if err := req.Body.Close(); err != nil {
				t.Fatalf("request body close error = %v", err)
			}
			payload := string(body)
			for _, needle := range []string{
				"grant_type=refresh_token",
				"refresh_token=refresh-token",
				"client_id=client-id",
				"client_secret=client-secret",
			} {
				if !strings.Contains(payload, needle) {
					t.Fatalf("refresh payload %q missing %q", payload, needle)
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"access_token":"refreshed-token"}`)),
			}, nil
		}),
	}

	fileTokenStoreFetchProjectIDFn = func(ctx context.Context, accessToken string, client *http.Client) (string, error) {
		if ctx == nil {
			t.Fatal("fetch project id ctx is nil")
		}
		if accessToken != "refreshed-token" {
			t.Fatalf("access token = %q, want %q", accessToken, "refreshed-token")
		}
		if client != fileTokenStoreHTTPClient {
			t.Fatal("fetch project id client mismatch")
		}
		return "proj-123", nil
	}

	store := NewFileTokenStore()
	auth, err := store.readAuthFile(authPath, tempDir)
	if err != nil {
		t.Fatalf("readAuthFile() error = %v", err)
	}
	if auth == nil {
		t.Fatal("readAuthFile() returned nil auth")
	}
	if got := auth.Metadata["project_id"]; got != "proj-123" {
		t.Fatalf("project_id = %v, want %q", got, "proj-123")
	}

	tokenMap, ok := auth.Metadata["token"].(map[string]any)
	if !ok {
		t.Fatalf("token metadata type = %T, want map[string]any", auth.Metadata["token"])
	}
	if got := tokenMap["access_token"]; got != "refreshed-token" {
		t.Fatalf("token.access_token = %v, want %q", got, "refreshed-token")
	}

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got := persisted["project_id"]; got != "proj-123" {
		t.Fatalf("persisted project_id = %v, want %q", got, "proj-123")
	}
	persistedToken, ok := persisted["token"].(map[string]any)
	if !ok {
		t.Fatalf("persisted token type = %T, want map[string]any", persisted["token"])
	}
	if got := persistedToken["access_token"]; got != "refreshed-token" {
		t.Fatalf("persisted token.access_token = %v, want %q", got, "refreshed-token")
	}
}

func TestFileTokenStoreListIgnoresNonAuthJSON(t *testing.T) {
	tempDir := t.TempDir()
	store := NewFileTokenStore()
	store.SetBaseDir(tempDir)

	if err := os.MkdirAll(filepath.Join(tempDir, "logs"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "logs", "usage-statistics.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile(stats) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "codex.json"), []byte(`{"type":"codex","email":"user@example.com"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(auth) error = %v", err)
	}

	auths, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(auths))
	}
	if auths[0] == nil {
		t.Fatal("List() returned nil auth")
	}
	if auths[0].ID != "codex.json" {
		t.Fatalf("auth ID = %q, want %q", auths[0].ID, "codex.json")
	}
	if auths[0].Provider != "codex" {
		t.Fatalf("auth provider = %q, want %q", auths[0].Provider, "codex")
	}
}

func TestFileTokenStoreSaveAndListRoundTripRuntimeState(t *testing.T) {
	tempDir := t.TempDir()
	store := NewFileTokenStore()
	store.SetBaseDir(tempDir)

	next := time.Date(2026, 4, 1, 9, 45, 0, 0, time.UTC)
	auth := &cliproxyauth.Auth{
		ID:             "codex.json",
		FileName:       "codex.json",
		Provider:       "codex",
		Status:         cliproxyauth.StatusError,
		StatusMessage:  "quota exhausted",
		Unavailable:    true,
		NextRetryAfter: next,
		Quota: cliproxyauth.QuotaState{
			Exceeded:      true,
			Reason:        "quota",
			NextRecoverAt: next,
			BackoffLevel:  2,
			StrikeCount:   3,
		},
		LastError: &cliproxyauth.Error{HTTPStatus: 429, Message: "quota"},
		ModelStates: map[string]*cliproxyauth.ModelState{
			"gpt-5.4": {
				Status:         cliproxyauth.StatusError,
				StatusMessage:  "model quota",
				Unavailable:    true,
				NextRetryAfter: next,
				LastError:      &cliproxyauth.Error{HTTPStatus: 429, Message: "model quota"},
				Quota: cliproxyauth.QuotaState{
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

	path, err := store.Save(context.Background(), auth)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	runtimeState, ok := payload[cliproxyauth.PersistedRuntimeStateMetadataKey].(map[string]any)
	if !ok {
		t.Fatalf("persisted runtime state = %T, want map[string]any", payload[cliproxyauth.PersistedRuntimeStateMetadataKey])
	}
	if _, exists := runtimeState["last_error"]; exists {
		t.Fatalf("persisted auth runtime state unexpectedly contains last_error: %#v", runtimeState)
	}
	modelStates, ok := runtimeState["model_states"].(map[string]any)
	if !ok {
		t.Fatalf("persisted model_states = %T, want map[string]any", runtimeState["model_states"])
	}
	modelState, ok := modelStates["gpt-5.4"].(map[string]any)
	if !ok {
		t.Fatalf("persisted model state = %T, want map[string]any", modelStates["gpt-5.4"])
	}
	if _, exists := modelState["last_error"]; exists {
		t.Fatalf("persisted model runtime state unexpectedly contains last_error: %#v", modelState)
	}

	auths, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(auths))
	}
	got := auths[0]
	if got.LastError != nil {
		t.Fatalf("got.LastError = %#v, want nil", got.LastError)
	}
	if got.Status != cliproxyauth.StatusError {
		t.Fatalf("got.Status = %q, want %q", got.Status, cliproxyauth.StatusError)
	}
	if got.StatusMessage != "quota exhausted" {
		t.Fatalf("got.StatusMessage = %q, want %q", got.StatusMessage, "quota exhausted")
	}
	if !got.NextRetryAfter.Equal(next) {
		t.Fatalf("got.NextRetryAfter = %v, want %v", got.NextRetryAfter, next)
	}
	state := got.ModelStates["gpt-5.4"]
	if state == nil {
		t.Fatal("expected restored model state for gpt-5.4")
	}
	if state.LastError != nil {
		t.Fatalf("got model LastError = %#v, want nil", state.LastError)
	}
	if !state.NextRetryAfter.Equal(next) {
		t.Fatalf("got model NextRetryAfter = %v, want %v", state.NextRetryAfter, next)
	}
}
