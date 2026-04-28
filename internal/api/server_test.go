package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubDefaultTransport(t *testing.T, transport http.RoundTripper) {
	t.Helper()

	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = original
	})
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
	}

	authManager := auth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()

	configPath := filepath.Join(tmpDir, "config.yaml")
	return NewServer(cfg, authManager, accessManager, configPath)
}

func TestHealthz(t *testing.T) {
	server := newTestServer(t)

	t.Run("GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var resp struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response JSON: %v; body=%s", err, rr.Body.String())
		}
		if resp.Status != "ok" {
			t.Fatalf("unexpected response status: got %q want %q", resp.Status, "ok")
		}
	})

	t.Run("HEAD", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/healthz", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}
		if rr.Body.Len() != 0 {
			t.Fatalf("expected empty body for HEAD request, got %q", rr.Body.String())
		}
	})
}

func TestAmpProviderModelRoutes(t *testing.T) {
	testCases := []struct {
		name         string
		path         string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "openai root models",
			path:         "/api/provider/openai/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "groq root models",
			path:         "/api/provider/groq/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "openai models",
			path:         "/api/provider/openai/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "anthropic models",
			path:         "/api/provider/anthropic/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"data"`,
		},
		{
			name:         "google models v1",
			path:         "/api/provider/google/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
		{
			name:         "google models v1beta",
			path:         "/api/provider/google/v1beta/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer test-key")

			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("unexpected status code for %s: got %d want %d; body=%s", tc.path, rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, tc.wantContains) {
				t.Fatalf("response body for %s missing %q: %s", tc.path, tc.wantContains, body)
			}
		})
	}
}

func TestGeminiCLIRouteDisabledByDefault(t *testing.T) {
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1internal:generateContent", bytes.NewBufferString(`{"model":"gemini-2.5-pro"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:4567"

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Gemini CLI endpoint is disabled") {
		t.Fatalf("body = %q, want disabled endpoint error", rr.Body.String())
	}
}

func TestGeminiCLIRouteRejectsNonLocalWhenEnabled(t *testing.T) {
	server := newTestServer(t)
	server.cfg.EnableGeminiCLIEndpoint = true

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1internal:generateContent", bytes.NewBufferString(`{"model":"gemini-2.5-pro"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.7:4567"

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "CLI reply only allow local access") {
		t.Fatalf("body = %q, want local access error", rr.Body.String())
	}
}

func TestGeminiCLIPassthroughPreservesAuthorizationForLocalEndpoint(t *testing.T) {
	server := newTestServer(t)
	server.cfg.EnableGeminiCLIEndpoint = true

	var upstreamAuthorization string
	stubDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		upstreamAuthorization = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			Request:    req,
		}, nil
	}))

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1internal:countTokens", bytes.NewBufferString(`{"model":"gemini-2.5-pro"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer upstream-token")
	req.RemoteAddr = "127.0.0.1:4567"

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if upstreamAuthorization != "Bearer upstream-token" {
		t.Fatalf("upstream Authorization = %q, want %q", upstreamAuthorization, "Bearer upstream-token")
	}
}

func TestGeminiCLIRouteAllowsLoopbackHostForms(t *testing.T) {
	tests := []struct {
		name       string
		targetURL  string
		remoteAddr string
	}{
		{name: "localhost host", targetURL: "http://localhost/v1internal:countTokens", remoteAddr: "127.0.0.1:4567"},
		{name: "localhost with port", targetURL: "http://localhost:8080/v1internal:countTokens", remoteAddr: "127.0.0.1:4567"},
		{name: "ipv6 loopback", targetURL: "http://[::1]:8080/v1internal:countTokens", remoteAddr: "[::1]:4567"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)
			server.cfg.EnableGeminiCLIEndpoint = true

			stubDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
					Request:    req,
				}, nil
			}))

			req := httptest.NewRequest(http.MethodPost, tc.targetURL, bytes.NewBufferString(`{"model":"gemini-2.5-pro"}`))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = tc.remoteAddr

			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
			}
		})
	}
}

func TestGeminiCLIPassthroughFiltersHopByHopHeaders(t *testing.T) {
	server := newTestServer(t)
	server.cfg.EnableGeminiCLIEndpoint = true

	var upstreamHeaders http.Header
	stubDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		upstreamHeaders = req.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			Request:    req,
		}, nil
	}))

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1internal:countTokens", bytes.NewBufferString(`{"model":"gemini-2.5-pro"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer upstream-token")
	req.Header.Set("Connection", "keep-alive, X-Hop")
	req.Header.Set("X-Hop", "drop-me")
	req.Header.Set("Transfer-Encoding", "chunked")
	req.RemoteAddr = "127.0.0.1:4567"

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if upstreamHeaders.Get("Authorization") != "Bearer upstream-token" {
		t.Fatalf("Authorization = %q, want preserved upstream token", upstreamHeaders.Get("Authorization"))
	}
	if upstreamHeaders.Get("Connection") != "" || upstreamHeaders.Get("Transfer-Encoding") != "" || upstreamHeaders.Get("X-Hop") != "" {
		t.Fatalf("hop-by-hop headers leaked upstream: %v", upstreamHeaders)
	}
}

func TestGeminiCLIPassthroughPreservesGoogleAPIKeyForLocalEndpoint(t *testing.T) {
	server := newTestServer(t)
	server.cfg.EnableGeminiCLIEndpoint = true

	var (
		upstreamAuthorization string
		upstreamGoogleAPIKey  string
	)
	stubDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		upstreamAuthorization = req.Header.Get("Authorization")
		upstreamGoogleAPIKey = req.Header.Get("X-Goog-Api-Key")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			Request:    req,
		}, nil
	}))

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1internal:countTokens", bytes.NewBufferString(`{"model":"gemini-2.5-pro"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", "upstream-key")
	req.Header.Set("Authorization", "Bearer upstream-token")
	req.RemoteAddr = "127.0.0.1:4567"

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if upstreamAuthorization != "Bearer upstream-token" {
		t.Fatalf("upstream Authorization = %q, want %q", upstreamAuthorization, "Bearer upstream-token")
	}
	if upstreamGoogleAPIKey != "upstream-key" {
		t.Fatalf("upstream X-Goog-Api-Key = %q, want %q", upstreamGoogleAPIKey, "upstream-key")
	}
}

func TestDefaultRequestLoggerFactory_UsesResolvedLogDirectory(t *testing.T) {
	t.Setenv("WRITABLE_PATH", "")
	t.Setenv("writable_path", "")

	originalWD, errGetwd := os.Getwd()
	if errGetwd != nil {
		t.Fatalf("failed to get current working directory: %v", errGetwd)
	}

	tmpDir := t.TempDir()
	if errChdir := os.Chdir(tmpDir); errChdir != nil {
		t.Fatalf("failed to switch working directory: %v", errChdir)
	}
	defer func() {
		if errChdirBack := os.Chdir(originalWD); errChdirBack != nil {
			t.Fatalf("failed to restore working directory: %v", errChdirBack)
		}
	}()

	// Force ResolveLogDirectory to fallback to auth-dir/logs by making ./logs not a writable directory.
	if errWriteFile := os.WriteFile(filepath.Join(tmpDir, "logs"), []byte("not-a-directory"), 0o644); errWriteFile != nil {
		t.Fatalf("failed to create blocking logs file: %v", errWriteFile)
	}

	configDir := filepath.Join(tmpDir, "config")
	if errMkdirConfig := os.MkdirAll(configDir, 0o755); errMkdirConfig != nil {
		t.Fatalf("failed to create config dir: %v", errMkdirConfig)
	}
	configPath := filepath.Join(configDir, "config.yaml")

	authDir := filepath.Join(tmpDir, "auth")
	if errMkdirAuth := os.MkdirAll(authDir, 0o700); errMkdirAuth != nil {
		t.Fatalf("failed to create auth dir: %v", errMkdirAuth)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			RequestLog: false,
		},
		AuthDir:           authDir,
		ErrorLogsMaxFiles: 10,
	}

	logger := defaultRequestLoggerFactory(cfg, configPath)
	fileLogger, ok := logger.(*internallogging.FileRequestLogger)
	if !ok {
		t.Fatalf("expected *FileRequestLogger, got %T", logger)
	}

	errLog := fileLogger.LogRequestWithOptions(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"input":"hello"}`),
		http.StatusBadGateway,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"error":"upstream failure"}`),
		nil,
		nil,
		nil,
		true,
		"issue-1711",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("failed to write forced error request log: %v", errLog)
	}

	authLogsDir := filepath.Join(authDir, "logs")
	authEntries, errReadAuthDir := os.ReadDir(authLogsDir)
	if errReadAuthDir != nil {
		t.Fatalf("failed to read auth logs dir %s: %v", authLogsDir, errReadAuthDir)
	}
	foundErrorLogInAuthDir := false
	for _, entry := range authEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			foundErrorLogInAuthDir = true
			break
		}
	}
	if !foundErrorLogInAuthDir {
		t.Fatalf("expected forced error log in auth fallback dir %s, got entries: %+v", authLogsDir, authEntries)
	}

	configLogsDir := filepath.Join(configDir, "logs")
	configEntries, errReadConfigDir := os.ReadDir(configLogsDir)
	if errReadConfigDir != nil && !os.IsNotExist(errReadConfigDir) {
		t.Fatalf("failed to inspect config logs dir %s: %v", configLogsDir, errReadConfigDir)
	}
	for _, entry := range configEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("unexpected forced error log in config dir %s", configLogsDir)
		}
	}
}
