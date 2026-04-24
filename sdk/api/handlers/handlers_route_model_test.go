package handlers

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type routeModelCaptureExecutor struct {
	mu        sync.Mutex
	authIDs   []string
	reqModels []string
}

func (e *routeModelCaptureExecutor) Identifier() string { return "codex" }

func (e *routeModelCaptureExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *routeModelCaptureExecutor) ExecuteStream(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	_ = ctx
	_ = opts
	authID := ""
	if auth != nil {
		authID = auth.ID
	}
	e.mu.Lock()
	e.authIDs = append(e.authIDs, authID)
	e.reqModels = append(e.reqModels, req.Model)
	e.mu.Unlock()

	ch := make(chan coreexecutor.StreamChunk, 1)
	ch <- coreexecutor.StreamChunk{Payload: []byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")}
	close(ch)
	return &coreexecutor.StreamResult{Headers: http.Header{"X-Auth": {authID}}, Chunks: ch}, nil
}

func (e *routeModelCaptureExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *routeModelCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *routeModelCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "HttpRequest not implemented", HTTPStatus: http.StatusNotImplemented}
}

func (e *routeModelCaptureExecutor) snapshot() ([]string, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	authIDs := append([]string(nil), e.authIDs...)
	reqModels := append([]string(nil), e.reqModels...)
	return authIDs, reqModels
}

func TestExecuteStreamWithAuthRouteModel_UsesRouteModelForAuthSelection(t *testing.T) {
	t.Parallel()

	executor := &routeModelCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	imageAuth := &coreauth.Auth{ID: "image-auth", Provider: "codex"}
	if _, err := manager.Register(context.Background(), imageAuth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	textAuth := &coreauth.Auth{ID: "text-auth", Provider: "codex"}
	if _, err := manager.Register(context.Background(), textAuth); err != nil {
		t.Fatalf("register text auth: %v", err)
	}

	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(imageAuth.ID, "codex", []*registry.ModelInfo{
		{ID: "gpt-image-2", Created: time.Now().Unix()},
	})
	modelRegistry.RegisterClient(textAuth.ID, "codex", []*registry.ModelInfo{
		{ID: "gpt-5.4-mini", Created: time.Now().Unix()},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(imageAuth.ID)
		modelRegistry.UnregisterClient(textAuth.ID)
	})
	manager.RefreshSchedulerEntry(imageAuth.ID)
	manager.RefreshSchedulerEntry(textAuth.ID)

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)

	dataChan, _, errChan := handler.ExecuteStreamWithAuthRouteModel(
		context.Background(),
		"openai-response",
		"gpt-5.4-mini",
		"gpt-image-2",
		[]byte(`{"model":"gpt-5.4-mini"}`),
		"",
	)
	if dataChan == nil {
		t.Fatalf("dataChan = nil")
	}
	for range dataChan {
	}
	if errMsg, ok := <-errChan; ok && errMsg != nil {
		t.Fatalf("ExecuteStreamWithAuthRouteModel() error = %v", errMsg)
	}

	authIDs, reqModels := executor.snapshot()
	if len(authIDs) != 1 || authIDs[0] != "image-auth" {
		t.Fatalf("selected auths = %v, want [image-auth]", authIDs)
	}
	if len(reqModels) != 1 || reqModels[0] != "gpt-5.4-mini" {
		t.Fatalf("request models = %v, want [gpt-5.4-mini]", reqModels)
	}
}
