package auth

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type antigravityCreditsFallbackExecutor struct {
	streamCreditsRequested []bool
}

func (e *antigravityCreditsFallbackExecutor) Identifier() string { return "antigravity" }

func (e *antigravityCreditsFallbackExecutor) Execute(ctx context.Context, _ *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if !AntigravityCreditsRequested(ctx) {
		return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota exhausted"}
	}
	return cliproxyexecutor.Response{Payload: []byte("credits fallback: " + req.Model)}, nil
}

func (e *antigravityCreditsFallbackExecutor) ExecuteStream(ctx context.Context, _ *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	creditsRequested := AntigravityCreditsRequested(ctx)
	e.streamCreditsRequested = append(e.streamCreditsRequested, creditsRequested)

	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	if !creditsRequested {
		ch <- cliproxyexecutor.StreamChunk{Err: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota exhausted"}}
		close(ch)
		return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Initial": {req.Model}}, Chunks: ch}, nil
	}
	ch <- cliproxyexecutor.StreamChunk{Payload: []byte("credits fallback")}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Credits": {req.Model}}, Chunks: ch}, nil
}

func (e *antigravityCreditsFallbackExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *antigravityCreditsFallbackExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "CountTokens not implemented"}
}

func (e *antigravityCreditsFallbackExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func TestManagerExecuteStream_AntigravityCreditsFallbackAfterBootstrap429(t *testing.T) {
	model := "claude-opus-4-6-thinking-" + stringsForTestName(t.Name())
	authID := "ag-credits-" + stringsForTestName(t.Name())
	executor := &antigravityCreditsFallbackExecutor{}
	manager := NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{
		QuotaExceeded: internalconfig.QuotaExceeded{AntigravityCredits: true},
	})
	manager.RegisterExecutor(executor)

	registry.GetGlobalRegistry().RegisterClient(authID, "antigravity", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(authID) })
	if _, errRegister := manager.Register(context.Background(), &Auth{ID: authID, Provider: "antigravity"}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	streamResult, errExecute := manager.ExecuteStream(context.Background(), []string{"antigravity"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("execute stream: %v", errExecute)
	}

	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	if string(payload) != "credits fallback" {
		t.Fatalf("payload = %q, want %q", string(payload), "credits fallback")
	}
	if got := streamResult.Headers.Get("X-Credits"); got != model {
		t.Fatalf("X-Credits header = %q, want routed model", got)
	}
	if len(executor.streamCreditsRequested) != 2 {
		t.Fatalf("stream calls = %d, want 2", len(executor.streamCreditsRequested))
	}
	if executor.streamCreditsRequested[0] || !executor.streamCreditsRequested[1] {
		t.Fatalf("credits flags = %v, want [false true]", executor.streamCreditsRequested)
	}
}

func TestManagerExecute_AntigravityCreditsFallbackAfter429(t *testing.T) {
	model := "claude-sonnet-4-6-" + stringsForTestName(t.Name())
	authID := "ag-credits-nonstream-" + stringsForTestName(t.Name())
	executor := &antigravityCreditsFallbackExecutor{}
	manager := NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{
		QuotaExceeded: internalconfig.QuotaExceeded{AntigravityCredits: true},
	})
	manager.RegisterExecutor(executor)

	registry.GetGlobalRegistry().RegisterClient(authID, "antigravity", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(authID) })
	if _, errRegister := manager.Register(context.Background(), &Auth{ID: authID, Provider: "antigravity"}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	resp, errExecute := manager.Execute(context.Background(), []string{"antigravity"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("execute: %v", errExecute)
	}
	if got := string(resp.Payload); got != "credits fallback: "+model {
		t.Fatalf("payload = %q, want credits fallback", got)
	}
}

func TestStatusCodeFromError_UnwrapsStreamBootstrap429(t *testing.T) {
	bootstrapErr := newStreamBootstrapError(&Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota exhausted"}, nil)
	wrappedErr := fmt.Errorf("conductor stream failed: %w", bootstrapErr)

	if status := statusCodeFromError(wrappedErr); status != http.StatusTooManyRequests {
		t.Fatalf("statusCodeFromError() = %d, want %d", status, http.StatusTooManyRequests)
	}
}

func TestFindAllAntigravityCreditsCandidateAuths_PrefersKnownCreditsThenUnknown(t *testing.T) {
	m := &Manager{
		auths: map[string]*Auth{
			"zz-credits": {ID: "zz-credits", Provider: "antigravity"},
			"aa-unknown": {ID: "aa-unknown", Provider: "antigravity"},
			"mm-no":      {ID: "mm-no", Provider: "antigravity"},
		},
		executors: map[string]ProviderExecutor{
			"antigravity": schedulerTestExecutor{},
		},
	}

	SetAntigravityCreditsHint("zz-credits", AntigravityCreditsHint{
		Known:     true,
		Available: true,
		UpdatedAt: time.Now(),
	})
	SetAntigravityCreditsHint("mm-no", AntigravityCreditsHint{
		Known:     true,
		Available: false,
		UpdatedAt: time.Now(),
	})

	candidates := m.findAllAntigravityCreditsCandidateAuths("claude-sonnet-4-6", cliproxyexecutor.Options{})
	if len(candidates) != 2 {
		t.Fatalf("candidates len = %d, want 2", len(candidates))
	}
	if candidates[0].auth.ID != "zz-credits" {
		t.Fatalf("candidates[0].auth.ID = %q, want %q", candidates[0].auth.ID, "zz-credits")
	}
	if candidates[1].auth.ID != "aa-unknown" {
		t.Fatalf("candidates[1].auth.ID = %q, want %q", candidates[1].auth.ID, "aa-unknown")
	}

	nonClaude := m.findAllAntigravityCreditsCandidateAuths("gemini-3-flash", cliproxyexecutor.Options{})
	if len(nonClaude) != 0 {
		t.Fatalf("nonClaude len = %d, want 0", len(nonClaude))
	}

	pinnedOpts := cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.PinnedAuthMetadataKey: "aa-unknown"},
	}
	pinned := m.findAllAntigravityCreditsCandidateAuths("claude-sonnet-4-6", pinnedOpts)
	if len(pinned) != 1 {
		t.Fatalf("pinned len = %d, want 1", len(pinned))
	}
	if pinned[0].auth.ID != "aa-unknown" {
		t.Fatalf("pinned[0].auth.ID = %q, want %q", pinned[0].auth.ID, "aa-unknown")
	}
}

func stringsForTestName(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
			continue
		}
		out = append(out, '-')
	}
	return string(out)
}
