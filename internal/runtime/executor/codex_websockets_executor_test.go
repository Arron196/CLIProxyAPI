package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestBuildCodexWebsocketRequestBodyPreservesPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"gpt-5-codex","previous_response_id":"resp-1","input":[{"type":"message","id":"msg-1"}]}`)

	wsReqBody := buildCodexWebsocketRequestBody(body)

	if got := gjson.GetBytes(wsReqBody, "type").String(); got != "response.create" {
		t.Fatalf("type = %s, want response.create", got)
	}
	if got := gjson.GetBytes(wsReqBody, "previous_response_id").String(); got != "resp-1" {
		t.Fatalf("previous_response_id = %s, want resp-1", got)
	}
	if gjson.GetBytes(wsReqBody, "input.0.id").String() != "msg-1" {
		t.Fatalf("input item id mismatch")
	}
	if got := gjson.GetBytes(wsReqBody, "type").String(); got == "response.append" {
		t.Fatalf("unexpected websocket request type: %s", got)
	}
}

func TestApplyCodexWebsocketHeadersDefaultsToCurrentResponsesBeta(t *testing.T) {
	headers := applyCodexWebsocketHeaders(context.Background(), http.Header{}, nil, "", nil)

	if got := headers.Get("OpenAI-Beta"); got != codexResponsesWebsocketBetaHeaderValue {
		t.Fatalf("OpenAI-Beta = %s, want %s", got, codexResponsesWebsocketBetaHeaderValue)
	}
	if got := headers.Get("User-Agent"); got != codexUserAgent {
		t.Fatalf("User-Agent = %s, want %s", got, codexUserAgent)
	}
	if got := headers.Get("Version"); got != "" {
		t.Fatalf("Version = %q, want empty", got)
	}
	if got := headers.Get("x-codex-beta-features"); got != "" {
		t.Fatalf("x-codex-beta-features = %q, want empty", got)
	}
	if got := headers.Get("X-Codex-Turn-Metadata"); got != "" {
		t.Fatalf("X-Codex-Turn-Metadata = %q, want empty", got)
	}
	if got := headers.Get("X-Client-Request-Id"); got != "" {
		t.Fatalf("X-Client-Request-Id = %q, want empty", got)
	}
}

func TestApplyCodexWebsocketHeadersPassesThroughClientIdentityHeaders(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	ctx := contextWithGinHeaders(map[string]string{
		"Originator":            "Codex Desktop",
		"Version":               "0.115.0-alpha.27",
		"X-Codex-Turn-Metadata": `{"turn_id":"turn-1"}`,
		"X-Client-Request-Id":   "019d2233-e240-7162-992d-38df0a2a0e0d",
	})

	headers := applyCodexWebsocketHeaders(ctx, http.Header{}, auth, "", nil)

	if got := headers.Get("Originator"); got != "Codex Desktop" {
		t.Fatalf("Originator = %s, want %s", got, "Codex Desktop")
	}
	if got := headers.Get("Version"); got != "0.115.0-alpha.27" {
		t.Fatalf("Version = %s, want %s", got, "0.115.0-alpha.27")
	}
	if got := headers.Get("X-Codex-Turn-Metadata"); got != `{"turn_id":"turn-1"}` {
		t.Fatalf("X-Codex-Turn-Metadata = %s, want %s", got, `{"turn_id":"turn-1"}`)
	}
	if got := headers.Get("X-Client-Request-Id"); got != "019d2233-e240-7162-992d-38df0a2a0e0d" {
		t.Fatalf("X-Client-Request-Id = %s, want %s", got, "019d2233-e240-7162-992d-38df0a2a0e0d")
	}
}

func TestApplyCodexWebsocketHeadersUsesConfigDefaultsForOAuth(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "my-codex-client/1.0",
			BetaFeatures: "feature-a,feature-b",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}

	headers := applyCodexWebsocketHeaders(context.Background(), http.Header{}, auth, "", cfg)

	if got := headers.Get("User-Agent"); got != "my-codex-client/1.0" {
		t.Fatalf("User-Agent = %s, want %s", got, "my-codex-client/1.0")
	}
	if got := headers.Get("x-codex-beta-features"); got != "feature-a,feature-b" {
		t.Fatalf("x-codex-beta-features = %s, want %s", got, "feature-a,feature-b")
	}
	if got := headers.Get("OpenAI-Beta"); got != codexResponsesWebsocketBetaHeaderValue {
		t.Fatalf("OpenAI-Beta = %s, want %s", got, codexResponsesWebsocketBetaHeaderValue)
	}
}

func TestApplyCodexWebsocketHeadersPrefersExistingHeadersOverClientAndConfig(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	ctx := contextWithGinHeaders(map[string]string{
		"User-Agent":            "client-ua",
		"X-Codex-Beta-Features": "client-beta",
	})
	headers := http.Header{}
	headers.Set("User-Agent", "existing-ua")
	headers.Set("X-Codex-Beta-Features", "existing-beta")

	got := applyCodexWebsocketHeaders(ctx, headers, auth, "", cfg)

	if gotVal := got.Get("User-Agent"); gotVal != "existing-ua" {
		t.Fatalf("User-Agent = %s, want %s", gotVal, "existing-ua")
	}
	if gotVal := got.Get("x-codex-beta-features"); gotVal != "existing-beta" {
		t.Fatalf("x-codex-beta-features = %s, want %s", gotVal, "existing-beta")
	}
}

func TestApplyCodexWebsocketHeadersConfigUserAgentOverridesClientHeader(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	ctx := contextWithGinHeaders(map[string]string{
		"User-Agent":            "client-ua",
		"X-Codex-Beta-Features": "client-beta",
	})

	headers := applyCodexWebsocketHeaders(ctx, http.Header{}, auth, "", cfg)

	if got := headers.Get("User-Agent"); got != "config-ua" {
		t.Fatalf("User-Agent = %s, want %s", got, "config-ua")
	}
	if got := headers.Get("x-codex-beta-features"); got != "client-beta" {
		t.Fatalf("x-codex-beta-features = %s, want %s", got, "client-beta")
	}
}

func TestApplyCodexWebsocketHeadersIgnoresConfigForAPIKeyAuth(t *testing.T) {
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"api_key": "sk-test"},
	}

	headers := applyCodexWebsocketHeaders(context.Background(), http.Header{}, auth, "sk-test", cfg)

	if got := headers.Get("User-Agent"); got != codexUserAgent {
		t.Fatalf("User-Agent = %s, want %s", got, codexUserAgent)
	}
	if got := headers.Get("x-codex-beta-features"); got != "" {
		t.Fatalf("x-codex-beta-features = %q, want empty", got)
	}
}

func TestApplyCodexHeadersUsesConfigUserAgentForOAuth(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:    "config-ua",
			BetaFeatures: "config-beta",
		},
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	req = req.WithContext(contextWithGinHeaders(map[string]string{
		"User-Agent": "client-ua",
	}))

	applyCodexHeaders(req, auth, "oauth-token", true, cfg)

	if got := req.Header.Get("User-Agent"); got != "config-ua" {
		t.Fatalf("User-Agent = %s, want %s", got, "config-ua")
	}
	if got := req.Header.Get("x-codex-beta-features"); got != "" {
		t.Fatalf("x-codex-beta-features = %q, want empty", got)
	}
}

func TestApplyCodexHeadersSkipsGeneratedSessionIDForNonMacUserAgent(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req = req.WithContext(contextWithGinHeaders(map[string]string{
		"User-Agent": "codex-cli/0.1 (Linux; x64)",
	}))

	applyCodexHeaders(req, nil, "oauth-token", true, &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent: "codex-cli/0.1 (Linux; x64)",
		},
	})

	if got := req.Header.Get("Session_id"); got != "" {
		t.Fatalf("Session_id = %q, want empty for non-Mac user agent", got)
	}
}

func TestApplyCodexWebsocketHeadersSkipsGeneratedSessionIDForNonMacUserAgent(t *testing.T) {
	ctx := contextWithGinHeaders(map[string]string{
		"User-Agent": "codex-cli/0.1 (Linux; x64)",
	})
	headers := applyCodexWebsocketHeaders(ctx, http.Header{}, nil, "", nil)

	if got := headers.Get("Session_id"); got != "" {
		t.Fatalf("Session_id = %q, want empty for non-Mac user agent", got)
	}
}

func TestCodexAutoExecutorExecuteStream_WebsocketStripsPrefixedModelFromOutboundRequest(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	reqPathCh := make(chan string, 1)
	reqBodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read websocket request: %v", err)
			return
		}
		if msgType != websocket.TextMessage {
			t.Errorf("message type = %d, want %d", msgType, websocket.TextMessage)
			return
		}
		reqPathCh <- r.URL.Path
		reqBodyCh <- payload

		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_ws"}}`)); err != nil {
			t.Errorf("write websocket created event: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(codexCompletedEventJSON("resp_ws", "gpt-5.4", "ok-ws"))); err != nil {
			t.Errorf("write websocket completed event: %v", err)
		}
	}))
	defer server.Close()

	auth := newCodexTestAuth(server.URL, "ws-key")
	auth.Prefix = "team"
	auth.Attributes["websockets"] = "true"

	executor := NewCodexAutoExecutor(&config.Config{})
	ctx, cancel := context.WithTimeout(cliproxyexecutor.WithDownstreamWebsocket(context.Background()), 5*time.Second)
	defer cancel()

	result, err := executor.ExecuteStream(
		ctx,
		auth,
		cliproxyexecutor.Request{
			Model: "gpt-5.4",
			Payload: []byte(`{
				"model":"team/gpt-5.4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
				"stream":true
			}`),
		},
		cliproxyexecutor.Options{
			Stream:       true,
			SourceFormat: sdktranslator.FromString("openai-response"),
			Metadata: map[string]any{
				cliproxyexecutor.RequestedModelMetadataKey: "team/gpt-5.4",
			},
		},
	)
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
	}

	select {
	case path := <-reqPathCh:
		if path != "/responses" {
			t.Fatalf("websocket path = %q, want %q", path, "/responses")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket request path")
	}

	select {
	case payload := <-reqBodyCh:
		if got := gjson.GetBytes(payload, "type").String(); got != "response.create" {
			t.Fatalf("websocket request type = %q, want %q", got, "response.create")
		}
		if got := gjson.GetBytes(payload, "model").String(); got != "gpt-5.4" {
			t.Fatalf("websocket request model = %q, want %q", got, "gpt-5.4")
		}
		if got := gjson.GetBytes(payload, "model").String(); got == "team/gpt-5.4" {
			t.Fatalf("websocket request leaked prefixed model: %s", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket request body")
	}
}

func TestApplyCodexHeadersPassesThroughClientIdentityHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"email": "user@example.com"},
	}
	req = req.WithContext(contextWithGinHeaders(map[string]string{
		"Originator":            "Codex Desktop",
		"Version":               "0.115.0-alpha.27",
		"X-Codex-Turn-Metadata": `{"turn_id":"turn-1"}`,
		"X-Client-Request-Id":   "019d2233-e240-7162-992d-38df0a2a0e0d",
	}))

	applyCodexHeaders(req, auth, "oauth-token", true, nil)

	if got := req.Header.Get("Originator"); got != "Codex Desktop" {
		t.Fatalf("Originator = %s, want %s", got, "Codex Desktop")
	}
	if got := req.Header.Get("Version"); got != "0.115.0-alpha.27" {
		t.Fatalf("Version = %s, want %s", got, "0.115.0-alpha.27")
	}
	if got := req.Header.Get("X-Codex-Turn-Metadata"); got != `{"turn_id":"turn-1"}` {
		t.Fatalf("X-Codex-Turn-Metadata = %s, want %s", got, `{"turn_id":"turn-1"}`)
	}
	if got := req.Header.Get("X-Client-Request-Id"); got != "019d2233-e240-7162-992d-38df0a2a0e0d" {
		t.Fatalf("X-Client-Request-Id = %s, want %s", got, "019d2233-e240-7162-992d-38df0a2a0e0d")
	}
}

func TestApplyCodexHeadersPassesThroughBetaFeaturesHeader(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req = req.WithContext(contextWithGinHeaders(map[string]string{
		"X-Codex-Beta-Features": "tool-streaming,v2",
	}))

	applyCodexHeaders(req, nil, "oauth-token", true, nil)

	if got := req.Header.Get("X-Codex-Beta-Features"); got != "tool-streaming,v2" {
		t.Fatalf("X-Codex-Beta-Features = %q, want %q", got, "tool-streaming,v2")
	}
}

func TestApplyCodexHeadersDoesNotInjectClientOnlyHeadersByDefault(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	applyCodexHeaders(req, nil, "oauth-token", true, nil)

	if got := req.Header.Get("Version"); got != "" {
		t.Fatalf("Version = %q, want empty", got)
	}
	if got := req.Header.Get("X-Codex-Turn-Metadata"); got != "" {
		t.Fatalf("X-Codex-Turn-Metadata = %q, want empty", got)
	}
	if got := req.Header.Get("X-Client-Request-Id"); got != "" {
		t.Fatalf("X-Client-Request-Id = %q, want empty", got)
	}
}

func contextWithGinHeaders(headers map[string]string) context.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	ginCtx.Request.Header = make(http.Header, len(headers))
	for key, value := range headers {
		ginCtx.Request.Header.Set(key, value)
	}
	return context.WithValue(context.Background(), "gin", ginCtx)
}

func TestNewProxyAwareWebsocketDialerDirectDisablesProxy(t *testing.T) {
	t.Parallel()

	dialer := newProxyAwareWebsocketDialer(
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "direct"},
	)

	if dialer.Proxy != nil {
		t.Fatal("expected websocket proxy function to be nil for direct mode")
	}
}

func TestReadCodexWebsocketMessageReturnsWhenReadChannelClosed(t *testing.T) {
	t.Parallel()

	sess := &codexWebsocketSession{}
	conn := &websocket.Conn{}
	readCh := make(chan codexWebsocketRead)
	close(readCh)

	_, _, err := readCodexWebsocketMessage(context.Background(), sess, conn, readCh)
	if err == nil {
		t.Fatal("expected error when session read channel is closed")
	}
	if !strings.Contains(err.Error(), "session read channel closed") {
		t.Fatalf("error = %v, want contains session read channel closed", err)
	}
}

func TestEnsureUpstreamConnReconnectsWhenAuthChanges(t *testing.T) {
	var (
		mu             sync.Mutex
		authorizations []string
	)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		authorizations = append(authorizations, strings.TrimSpace(r.Header.Get("Authorization")))
		mu.Unlock()

		go func() {
			defer func() {
				_ = conn.Close()
			}()
			for {
				if _, _, errRead := conn.ReadMessage(); errRead != nil {
					return
				}
			}
		}()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	executor := NewCodexWebsocketsExecutor(&config.Config{})
	sess := executor.getOrCreateSession("test-session")
	if sess == nil {
		t.Fatal("expected session to be created")
	}

	auth1 := &cliproxyauth.Auth{ID: "auth-1"}
	headers1 := http.Header{}
	headers1.Set("Authorization", "Bearer token-1")
	conn1, _, errDial1 := executor.ensureUpstreamConn(context.Background(), auth1, sess, auth1.ID, wsURL, headers1)
	if errDial1 != nil {
		t.Fatalf("first ensureUpstreamConn failed: %v", errDial1)
	}
	if conn1 == nil {
		t.Fatal("first ensureUpstreamConn returned nil connection")
	}

	auth2 := &cliproxyauth.Auth{ID: "auth-2"}
	headers2 := http.Header{}
	headers2.Set("Authorization", "Bearer token-2")
	conn2, _, errDial2 := executor.ensureUpstreamConn(context.Background(), auth2, sess, auth2.ID, wsURL, headers2)
	if errDial2 != nil {
		t.Fatalf("second ensureUpstreamConn failed: %v", errDial2)
	}
	if conn2 == nil {
		t.Fatal("second ensureUpstreamConn returned nil connection")
	}
	if conn1 == conn2 {
		t.Fatal("expected auth change to force upstream reconnect")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		count := len(authorizations)
		mu.Unlock()
		if count >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), authorizations...)
	mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("handshake count = %d, want at least 2", len(got))
	}
	if got[0] != "Bearer token-1" {
		t.Fatalf("first Authorization = %q, want %q", got[0], "Bearer token-1")
	}
	if got[1] != "Bearer token-2" {
		t.Fatalf("second Authorization = %q, want %q", got[1], "Bearer token-2")
	}

	executor.closeExecutionSession(sess, "test_done")
}

func TestCloseExecutionSessionUnblocksActiveRead(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverConnCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConnCh <- conn
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, errDial := websocket.DefaultDialer.Dial(wsURL, nil)
	if errDial != nil {
		t.Fatalf("dial websocket: %v", errDial)
	}
	defer func() { _ = clientConn.Close() }()

	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server websocket connection")
	}

	sess := &codexWebsocketSession{
		sessionID:  "session-close",
		conn:       serverConn,
		readerConn: serverConn,
	}
	readCh := make(chan codexWebsocketRead, 4)
	sess.setActive(readCh)

	executor := &CodexWebsocketsExecutor{
		CodexExecutor: &CodexExecutor{},
		sessions: map[string]*codexWebsocketSession{
			"session-close": sess,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	readErrCh := make(chan error, 1)
	go func() {
		_, _, err := readCodexWebsocketMessage(ctx, sess, serverConn, readCh)
		readErrCh <- err
	}()

	executor.CloseExecutionSession("session-close")

	select {
	case err := <-readErrCh:
		if err == nil {
			t.Fatal("expected read error after closing execution session")
		}
		errText := err.Error()
		if !strings.Contains(errText, "execution session closed") && !strings.Contains(errText, "session read channel closed") {
			t.Fatalf("error = %v, want fast-fail error from session close path", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read did not fail fast after closeExecutionSession")
	}
}

func TestEnsureUpstreamConnAuthSwitchRebuildsWebsocketConn(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	authHeaderCh := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		authHeaderCh <- strings.TrimSpace(r.Header.Get("Authorization"))
		for {
			_, _, errRead := conn.ReadMessage()
			if errRead != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	executor := NewCodexWebsocketsExecutor(&config.Config{})
	sess := &codexWebsocketSession{sessionID: "session-auth-switch"}

	headers1 := http.Header{}
	headers1.Set("Authorization", "Bearer token-1")
	conn1, _, errDial1 := executor.ensureUpstreamConn(context.Background(), nil, sess, "auth-1", wsURL, headers1)
	if errDial1 != nil {
		t.Fatalf("ensureUpstreamConn auth-1 error: %v", errDial1)
	}
	if conn1 == nil {
		t.Fatal("ensureUpstreamConn auth-1 returned nil conn")
	}

	headers2 := http.Header{}
	headers2.Set("Authorization", "Bearer token-2")
	conn2, _, errDial2 := executor.ensureUpstreamConn(context.Background(), nil, sess, "auth-2", wsURL, headers2)
	if errDial2 != nil {
		t.Fatalf("ensureUpstreamConn auth-2 error: %v", errDial2)
	}
	if conn2 == nil {
		t.Fatal("ensureUpstreamConn auth-2 returned nil conn")
	}
	if conn2 == conn1 {
		t.Fatal("expected new websocket conn after auth switch")
	}

	defer executor.invalidateUpstreamConn(sess, conn2, "test_done", nil)

	var got1, got2 string
	select {
	case got1 = <-authHeaderCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first websocket handshake")
	}
	select {
	case got2 = <-authHeaderCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second websocket handshake")
	}
	if got1 != "Bearer token-1" {
		t.Fatalf("first Authorization = %q, want %q", got1, "Bearer token-1")
	}
	if got2 != "Bearer token-2" {
		t.Fatalf("second Authorization = %q, want %q", got2, "Bearer token-2")
	}
	if got1 == got2 {
		t.Fatal("expected different Authorization headers after auth switch")
	}
}
