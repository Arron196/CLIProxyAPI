package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestSessionAffinitySelector_SameSessionSameAuth(t *testing.T) {
	t.Parallel()

	selector := NewSessionAffinitySelector(&RoundRobinSelector{})
	defer selector.Stop()

	auths := []*Auth{{ID: "auth-a"}, {ID: "auth-b"}, {ID: "auth-c"}}
	opts := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"metadata":{"user_id":"user_xxx_account__session_ac980658-63bd-4fb3-97ba-8da64cb1e344"}}`),
	}

	first, err := selector.Pick(context.Background(), "claude", "claude-3", opts, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	for i := 0; i < 5; i++ {
		got, err := selector.Pick(context.Background(), "claude", "claude-3", opts, auths)
		if err != nil {
			t.Fatalf("Pick() #%d error = %v", i, err)
		}
		if got.ID != first.ID {
			t.Fatalf("Pick() #%d auth.ID = %q, want %q", i, got.ID, first.ID)
		}
	}
}

func TestSessionAffinitySelector_NoSessionFallsBack(t *testing.T) {
	t.Parallel()

	selector := NewSessionAffinitySelector(&FillFirstSelector{})
	defer selector.Stop()

	auths := []*Auth{{ID: "b"}, {ID: "a"}, {ID: "c"}}
	got, err := selector.Pick(context.Background(), "claude", "claude-3", cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"model":"claude-3"}`),
	}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID != "a" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "a")
	}
}

func TestSessionAffinitySelector_FallbackHashInheritsFirstTurnBinding(t *testing.T) {
	t.Parallel()

	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: &RoundRobinSelector{},
		TTL:      time.Minute,
	})
	defer selector.Stop()

	auths := []*Auth{{ID: "auth-a"}, {ID: "auth-b"}}
	firstTurn := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"Hello"}]}`),
	}
	secondTurn := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi!"},{"role":"user","content":"Continue"}]}`),
	}

	first, err := selector.Pick(context.Background(), "openai", "gpt-5.4", firstTurn, auths)
	if err != nil {
		t.Fatalf("Pick() first turn error = %v", err)
	}
	second, err := selector.Pick(context.Background(), "openai", "gpt-5.4", secondTurn, auths)
	if err != nil {
		t.Fatalf("Pick() second turn error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("Pick() second turn auth.ID = %q, want %q", second.ID, first.ID)
	}
}

func TestSessionAffinitySelector_CrossProviderAndModelIsolation(t *testing.T) {
	t.Parallel()

	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: &RoundRobinSelector{},
		TTL:      time.Minute,
	})
	defer selector.Stop()

	opts := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"metadata":{"user_id":"user_xxx_account__session_cross-provider-test"}}`),
	}

	gotClaude, err := selector.Pick(context.Background(), "claude", "claude-3", opts, []*Auth{{ID: "auth-claude"}})
	if err != nil {
		t.Fatalf("Pick() claude error = %v", err)
	}
	gotGemini, err := selector.Pick(context.Background(), "gemini", "gemini-2.5-pro", opts, []*Auth{{ID: "auth-gemini"}})
	if err != nil {
		t.Fatalf("Pick() gemini error = %v", err)
	}
	gotGeminiFlash, err := selector.Pick(context.Background(), "gemini", "gemini-2.5-flash", opts, []*Auth{{ID: "auth-gemini-flash"}})
	if err != nil {
		t.Fatalf("Pick() gemini flash error = %v", err)
	}

	if gotClaude.ID != "auth-claude" || gotGemini.ID != "auth-gemini" || gotGeminiFlash.ID != "auth-gemini-flash" {
		t.Fatalf("unexpected picks: claude=%q gemini=%q gemini-flash=%q", gotClaude.ID, gotGemini.ID, gotGeminiFlash.ID)
	}
}

func TestSessionAffinitySelector_RebindsWhenCachedAuthUnavailable(t *testing.T) {
	t.Parallel()

	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: &RoundRobinSelector{},
		TTL:      time.Minute,
	})
	defer selector.Stop()

	opts := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"metadata":{"user_id":"user_xxx_account__session_failover-test"}}`),
	}
	auths := []*Auth{{ID: "auth-a"}, {ID: "auth-b"}}

	first, err := selector.Pick(context.Background(), "claude", "claude-3", opts, auths)
	if err != nil {
		t.Fatalf("Pick() first error = %v", err)
	}

	var remaining []*Auth
	for _, auth := range auths {
		if auth.ID != first.ID {
			remaining = append(remaining, auth)
		}
	}

	second, err := selector.Pick(context.Background(), "claude", "claude-3", opts, remaining)
	if err != nil {
		t.Fatalf("Pick() rebound error = %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("Pick() rebound auth.ID = %q, want different from %q", second.ID, first.ID)
	}
}

func TestSessionAffinitySelector_CodexWebsocketRebindsFromHTTPBindingToWebsocketAuth(t *testing.T) {
	t.Parallel()

	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: &RoundRobinSelector{},
		TTL:      time.Minute,
	})
	defer selector.Stop()

	opts := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"metadata":{"user_id":"user_xxx_account__session_codex-ws-sticky"}}`),
	}
	auths := []*Auth{
		{ID: "codex-http", Provider: "codex"},
		{ID: "codex-ws", Provider: "codex", Attributes: map[string]string{"websockets": "true"}},
	}

	first, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("Pick() initial error = %v", err)
	}
	if first.ID != "codex-http" {
		t.Fatalf("Pick() initial auth.ID = %q, want %q", first.ID, "codex-http")
	}

	wsCtx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())
	second, err := selector.Pick(wsCtx, "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("Pick() websocket error = %v", err)
	}
	if second.ID != "codex-ws" {
		t.Fatalf("Pick() websocket auth.ID = %q, want %q", second.ID, "codex-ws")
	}

	third, err := selector.Pick(wsCtx, "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("Pick() websocket repeat error = %v", err)
	}
	if third.ID != "codex-ws" {
		t.Fatalf("Pick() websocket repeat auth.ID = %q, want %q", third.ID, "codex-ws")
	}
}

func TestSessionAffinitySelector_CodexWebsocketFirstBindingUsesWebsocketSubset(t *testing.T) {
	t.Parallel()

	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: &RoundRobinSelector{},
		TTL:      time.Minute,
	})
	defer selector.Stop()

	wsCtx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())
	opts := cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"metadata":{"user_id":"user_xxx_account__session_codex-ws-first"}}`),
	}
	auths := []*Auth{
		{ID: "codex-http", Provider: "codex"},
		{ID: "codex-ws-a", Provider: "codex", Attributes: map[string]string{"websockets": "true"}},
		{ID: "codex-ws-b", Provider: "codex", Attributes: map[string]string{"websockets": "true"}},
	}

	got, err := selector.Pick(wsCtx, "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID == "codex-http" {
		t.Fatalf("Pick() auth.ID = %q, want websocket-capable auth", got.ID)
	}
}

func TestExtractSessionID_UsesHeaderWhenPresent(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	headers.Set("X-Session-ID", "session-123")
	got := ExtractSessionID(headers, []byte(`{"model":"x"}`), nil)
	if got != "header:session-123" {
		t.Fatalf("ExtractSessionID() = %q, want %q", got, "header:session-123")
	}
}

func TestExtractSessionID_OpenAIResponsesFormat(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"instructions":"You are Codex",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)
	got := ExtractSessionID(nil, payload, nil)
	if !strings.HasPrefix(got, "msg:") {
		t.Fatalf("ExtractSessionID() = %q, want msg:*", got)
	}
}

func TestSessionCache_GetAndRefresh(t *testing.T) {
	t.Parallel()

	cache := NewSessionCache(100 * time.Millisecond)
	defer cache.Stop()

	cache.Set("session1", "auth1")
	if got, ok := cache.GetAndRefresh("session1"); !ok || got != "auth1" {
		t.Fatalf("GetAndRefresh() = %q, %v, want auth1, true", got, ok)
	}
	time.Sleep(60 * time.Millisecond)
	if got, ok := cache.GetAndRefresh("session1"); !ok || got != "auth1" {
		t.Fatalf("GetAndRefresh() after refresh = %q, %v, want auth1, true", got, ok)
	}
	time.Sleep(60 * time.Millisecond)
	if got, ok := cache.GetAndRefresh("session1"); !ok || got != "auth1" {
		t.Fatalf("GetAndRefresh() after second refresh = %q, %v, want auth1, true", got, ok)
	}
	time.Sleep(110 * time.Millisecond)
	if got, ok := cache.GetAndRefresh("session1"); ok {
		t.Fatalf("GetAndRefresh() after expiry = %q, %v, want empty, false", got, ok)
	}
}
