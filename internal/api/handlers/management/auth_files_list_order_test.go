package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestListAuthFiles_SortsByFirstRegisteredAt(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	older := time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	newer := older.Add(10 * time.Minute)
	newerPath := filepath.Join(authDir, "a-newer.json")
	olderPath := filepath.Join(authDir, "z-older.json")
	if err := os.WriteFile(newerPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("write newer file: %v", err)
	}
	if err := os.WriteFile(olderPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("write older file: %v", err)
	}

	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:        "a-newer.json",
		FileName:  "a-newer.json",
		Provider:  "codex",
		CreatedAt: newer,
		Attributes: map[string]string{
			"path": newerPath,
		},
		Metadata: map[string]any{
			"type":                                "codex",
			coreauth.FirstRegisteredAtMetadataKey: newer.Format(time.RFC3339Nano),
		},
	}); err != nil {
		t.Fatalf("register newer auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:        "z-older.json",
		FileName:  "z-older.json",
		Provider:  "codex",
		CreatedAt: older,
		Attributes: map[string]string{
			"path": olderPath,
		},
		Metadata: map[string]any{
			"type":                                "codex",
			coreauth.FirstRegisteredAtMetadataKey: older.Format(time.RFC3339Nano),
		},
	}); err != nil {
		t.Fatalf("register older auth: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)

	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(payload.Files))
	}
	if got := payload.Files[0]["name"]; got != "z-older.json" {
		t.Fatalf("first file name = %#v, want %q", got, "z-older.json")
	}
	if _, ok := payload.Files[0]["first_registered_at"].(string); !ok {
		t.Fatalf("expected first_registered_at in payload, got %#v", payload.Files[0])
	}
}

func TestListAuthFilesFromDisk_SortsByFirstRegisteredAt(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	older := time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	newer := older.Add(10 * time.Minute)

	writeJSON := func(name string, registeredAt time.Time) {
		t.Helper()
		content := map[string]any{
			"type":                                "codex",
			coreauth.FirstRegisteredAtMetadataKey: registeredAt.Format(time.RFC3339Nano),
		}
		data, err := json.Marshal(content)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if err = os.WriteFile(filepath.Join(authDir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	writeJSON("a-newer.json", newer)
	writeJSON("z-older.json", older)

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)

	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(payload.Files))
	}
	if got := payload.Files[0]["name"]; got != "z-older.json" {
		t.Fatalf("first file name = %#v, want %q", got, "z-older.json")
	}
}
