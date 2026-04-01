package management

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestUploadAuthFile_BatchMultipart(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	files := []struct {
		name    string
		content string
	}{
		{name: "alpha.json", content: `{"type":"codex","email":"alpha@example.com"}`},
		{name: "beta.json", content: `{"type":"claude","email":"beta@example.com"}`},
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range files {
		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatalf("failed to create multipart file: %v", err)
		}
		if _, err = part.Write([]byte(file.content)); err != nil {
			t.Fatalf("failed to write multipart content: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx.Request = req

	h.UploadAuthFile(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected upload status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got, ok := payload["uploaded"].(float64); !ok || int(got) != len(files) {
		t.Fatalf("expected uploaded=%d, got %#v", len(files), payload["uploaded"])
	}

	for _, file := range files {
		fullPath := filepath.Join(authDir, file.name)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("expected uploaded file %s to exist: %v", file.name, err)
		}
		var stored map[string]any
		if err = json.Unmarshal(data, &stored); err != nil {
			t.Fatalf("failed to decode stored file %s: %v", file.name, err)
		}
		if stored["type"] == nil {
			t.Fatalf("expected stored file %s to keep type", file.name)
		}
		if _, ok := stored[coreauth.FirstRegisteredAtMetadataKey].(string); !ok {
			t.Fatalf("expected stored file %s to include %s, got %#v", file.name, coreauth.FirstRegisteredAtMetadataKey, stored)
		}
	}

	auths := manager.List()
	if len(auths) != len(files) {
		t.Fatalf("expected %d auth entries, got %d", len(files), len(auths))
	}
}

func TestUploadAuthFile_BatchMultipart_InvalidJSONDoesNotOverwriteExistingFile(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	existingName := "alpha.json"
	existingContent := `{"type":"codex","email":"alpha@example.com"}`
	if err := os.WriteFile(filepath.Join(authDir, existingName), []byte(existingContent), 0o600); err != nil {
		t.Fatalf("failed to seed existing auth file: %v", err)
	}

	files := []struct {
		name    string
		content string
	}{
		{name: existingName, content: `{"type":"codex"`},
		{name: "beta.json", content: `{"type":"claude","email":"beta@example.com"}`},
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range files {
		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatalf("failed to create multipart file: %v", err)
		}
		if _, err = part.Write([]byte(file.content)); err != nil {
			t.Fatalf("failed to write multipart content: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx.Request = req

	h.UploadAuthFile(ctx)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected upload status %d, got %d with body %s", http.StatusMultiStatus, rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(authDir, existingName))
	if err != nil {
		t.Fatalf("expected existing auth file to remain readable: %v", err)
	}
	if string(data) != existingContent {
		t.Fatalf("expected existing auth file to remain %q, got %q", existingContent, string(data))
	}

	betaData, err := os.ReadFile(filepath.Join(authDir, "beta.json"))
	if err != nil {
		t.Fatalf("expected valid auth file to be created: %v", err)
	}
	var betaStored map[string]any
	if err = json.Unmarshal(betaData, &betaStored); err != nil {
		t.Fatalf("failed to decode beta auth file: %v", err)
	}
	if _, ok := betaStored[coreauth.FirstRegisteredAtMetadataKey].(string); !ok {
		t.Fatalf("expected beta auth file to include %s, got %#v", coreauth.FirstRegisteredAtMetadataKey, betaStored)
	}
}

func TestUploadAuthFile_BatchMultipart_TrimsTypeBeforeRegister(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "trimmed.json")
	if err != nil {
		t.Fatalf("failed to create multipart file: %v", err)
	}
	content := `{"type":"codex ","email":"trimmed@example.com"}`
	if _, err = part.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write multipart content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx.Request = req

	h.UploadAuthFile(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected upload status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	auth, ok := manager.GetByID("trimmed.json")
	if !ok || auth == nil {
		t.Fatal("expected uploaded auth to be registered")
	}
	if auth.Provider != "codex" {
		t.Fatalf("auth provider = %q, want %q", auth.Provider, "codex")
	}
	if got, _ := auth.Metadata["type"].(string); got != "codex" {
		t.Fatalf("auth metadata type = %q, want %q", got, "codex")
	}
	if _, ok := auth.Metadata[coreauth.FirstRegisteredAtMetadataKey].(string); !ok {
		t.Fatalf("expected auth metadata to include %s, got %#v", coreauth.FirstRegisteredAtMetadataKey, auth.Metadata)
	}
}

func TestUploadAuthFile_BatchMultipart_RejectsBlankTypeWithoutWritingFile(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	files := []struct {
		name    string
		content string
	}{
		{name: "blank.json", content: `{"type":" ","email":"blank@example.com"}`},
		{name: "valid.json", content: `{"type":"claude","email":"valid@example.com"}`},
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range files {
		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatalf("failed to create multipart file: %v", err)
		}
		if _, err = part.Write([]byte(file.content)); err != nil {
			t.Fatalf("failed to write multipart content: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx.Request = req

	h.UploadAuthFile(ctx)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected upload status %d, got %d with body %s", http.StatusMultiStatus, rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(authDir, "blank.json")); !os.IsNotExist(err) {
		t.Fatalf("expected invalid auth file not to be written, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(authDir, "valid.json")); err != nil {
		t.Fatalf("expected valid auth file to be written: %v", err)
	}

	auths := manager.List()
	if len(auths) != 1 {
		t.Fatalf("expected 1 auth entry, got %d", len(auths))
	}
	if auths[0].Provider != "claude" {
		t.Fatalf("auth provider = %q, want %q", auths[0].Provider, "claude")
	}
}

func TestUploadAuthFile_BatchMultipart_PreservesFirstRegisteredAtOnSameNameUpload(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	firstContent := `{"type":"codex","email":"alpha@example.com"}`
	if err := h.writeAuthFile(context.Background(), "alpha.json", []byte(firstContent)); err != nil {
		t.Fatalf("first writeAuthFile() error = %v", err)
	}
	firstAuth, ok := manager.GetByID("alpha.json")
	if !ok || firstAuth == nil {
		t.Fatalf("expected first auth to be registered")
	}
	firstRegisteredAt, ok := coreauth.FirstRegisteredAt(firstAuth)
	if !ok {
		t.Fatalf("expected first auth to have first registered time")
	}

	secondContent := `{"type":"codex","email":"alpha+updated@example.com"}`
	if err := h.writeAuthFile(context.Background(), "alpha.json", []byte(secondContent)); err != nil {
		t.Fatalf("second writeAuthFile() error = %v", err)
	}

	secondAuth, ok := manager.GetByID("alpha.json")
	if !ok || secondAuth == nil {
		t.Fatalf("expected updated auth to remain registered")
	}
	secondRegisteredAt, ok := coreauth.FirstRegisteredAt(secondAuth)
	if !ok {
		t.Fatalf("expected updated auth to keep first registered time")
	}
	if !secondRegisteredAt.Equal(firstRegisteredAt) {
		t.Fatalf("first registered time changed from %s to %s", firstRegisteredAt, secondRegisteredAt)
	}

	data, err := os.ReadFile(filepath.Join(authDir, "alpha.json"))
	if err != nil {
		t.Fatalf("failed to read updated auth file: %v", err)
	}
	var stored map[string]any
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("failed to decode updated auth file: %v", err)
	}
	storedRegisteredAt, ok := coreauth.ParseFirstRegisteredAtValue(stored[coreauth.FirstRegisteredAtMetadataKey])
	if !ok {
		t.Fatalf("expected stored auth file to include %s", coreauth.FirstRegisteredAtMetadataKey)
	}
	if !storedRegisteredAt.Equal(firstRegisteredAt) {
		t.Fatalf("stored first registered time = %s, want %s", storedRegisteredAt, firstRegisteredAt)
	}
}

func TestDeleteAuthFile_BatchQuery(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	files := []string{"alpha.json", "beta.json"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(authDir, name), []byte(`{"type":"codex"}`), 0o600); err != nil {
			t.Fatalf("failed to write auth file %s: %v", name, err)
		}
	}

	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(
		http.MethodDelete,
		"/v0/management/auth-files?name="+url.QueryEscape(files[0])+"&name="+url.QueryEscape(files[1]),
		nil,
	)
	ctx.Request = req

	h.DeleteAuthFile(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected delete status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got, ok := payload["deleted"].(float64); !ok || int(got) != len(files) {
		t.Fatalf("expected deleted=%d, got %#v", len(files), payload["deleted"])
	}

	for _, name := range files {
		if _, err := os.Stat(filepath.Join(authDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected auth file %s to be removed, stat err: %v", name, err)
		}
	}
}
