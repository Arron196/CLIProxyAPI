package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

func testMalformedClaudeSignature() string {
	return base64.StdEncoding.EncodeToString([]byte{0x12, 0xff, 0xfe, 0xfd})
}

func testAntigravitySignatureAuth(baseURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		Attributes: map[string]string{
			"base_url": baseURL,
		},
		Metadata: map[string]any{
			"access_token": "token-123",
			"expired":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		},
	}
}

func invalidClaudeThinkingPayload() []byte {
	return []byte(`{
		"model": "claude-sonnet-4-6",
		"messages": [{
			"role": "assistant",
			"content": [
				{"type": "thinking", "thinking": "bad", "signature": "` + testMalformedClaudeSignature() + `"},
				{"type": "text", "text": "hello"}
			]
		}]
	}`)
}

func TestAntigravityExecutor_StrictBypassRejectsInvalidSignature(t *testing.T) {
	previousCache := cache.SignatureCacheEnabled()
	previousStrict := cache.SignatureBypassStrictMode()
	cache.SetSignatureCacheEnabled(false)
	cache.SetSignatureBypassStrictMode(true)
	t.Cleanup(func() {
		cache.SetSignatureCacheEnabled(previousCache)
		cache.SetSignatureBypassStrictMode(previousStrict)
	})

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}}`))
	}))
	defer server.Close()

	executor := NewAntigravityExecutor(nil)
	auth := testAntigravitySignatureAuth(server.URL)
	payload := invalidClaudeThinkingPayload()
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude"), OriginalRequest: payload}
	req := cliproxyexecutor.Request{Model: "claude-sonnet-4-6", Payload: payload}

	tests := []struct {
		name   string
		invoke func() error
	}{
		{
			name: "execute",
			invoke: func() error {
				_, err := executor.Execute(context.Background(), auth, req, opts)
				return err
			},
		},
		{
			name: "stream",
			invoke: func() error {
				_, err := executor.ExecuteStream(context.Background(), auth, req, cliproxyexecutor.Options{SourceFormat: opts.SourceFormat, OriginalRequest: payload, Stream: true})
				return err
			},
		},
		{
			name: "count tokens",
			invoke: func() error {
				_, err := executor.CountTokens(context.Background(), auth, req, opts)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.invoke()
			if err == nil {
				t.Fatal("expected invalid signature to return an error")
			}
			statusProvider, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("expected status error, got %T: %v", err, err)
			}
			if statusProvider.StatusCode() != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", statusProvider.StatusCode(), http.StatusBadRequest)
			}
		})
	}

	if got := hits.Load(); got != 0 {
		t.Fatalf("expected invalid signature to be rejected before upstream request, got %d upstream hits", got)
	}
}

func TestAntigravityExecutor_NonStrictBypassSkipsPrecheck(t *testing.T) {
	previousCache := cache.SignatureCacheEnabled()
	previousStrict := cache.SignatureBypassStrictMode()
	cache.SetSignatureCacheEnabled(false)
	cache.SetSignatureBypassStrictMode(false)
	t.Cleanup(func() {
		cache.SetSignatureCacheEnabled(previousCache)
		cache.SetSignatureBypassStrictMode(previousStrict)
	})

	payload := bytes.Clone(invalidClaudeThinkingPayload())
	from := sdktranslator.FromString("claude")

	if _, err := validateAntigravityRequestSignatures(from, payload); err != nil {
		t.Fatalf("non-strict bypass should skip precheck, got: %v", err)
	}
}

func TestAntigravityEnsureAccessToken_WarmTokenRefreshesCreditsHint(t *testing.T) {
	authID := "auth-warm-token-credits-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	executor := NewAntigravityExecutor(&config.Config{
		QuotaExceeded: config.QuotaExceeded{AntigravityCredits: true},
	})
	auth := &cliproxyauth.Auth{
		ID: authID,
		Metadata: map[string]any{
			"access_token": "token",
			"expired":      time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist" {
			t.Fatalf("unexpected request url %s", req.URL.String())
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization = %q, want Bearer token", got)
		}
		body, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if !strings.Contains(string(body), `"ide_name":"antigravity"`) {
			t.Fatalf("loadCodeAssist body missing ide metadata: %s", string(body))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"paidTier":{"id":"tier-1","availableCredits":[{"creditType":"GOOGLE_ONE_AI","creditAmount":"25000","minimumCreditAmountForUsage":"50"}]}}`)),
		}, nil
	}))

	token, updatedAuth, err := executor.ensureAccessToken(ctx, auth)
	if err != nil {
		t.Fatalf("ensureAccessToken error: %v", err)
	}
	if token != "token" {
		t.Fatalf("token = %q, want token", token)
	}
	if updatedAuth != nil {
		t.Fatalf("updatedAuth = %v, want nil for warm token", updatedAuth)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !cliproxyauth.HasKnownAntigravityCreditsHint(authID) {
		time.Sleep(10 * time.Millisecond)
	}
	hint, ok := cliproxyauth.GetAntigravityCreditsHint(authID)
	if !ok || !hint.Known {
		t.Fatal("expected credits hint to be populated")
	}
	if !hint.Available || hint.CreditAmount != 25000 || hint.MinCreditAmount != 50 || hint.PaidTierID != "tier-1" {
		t.Fatalf("credits hint = %+v, want available tier-1 balance", hint)
	}
}
