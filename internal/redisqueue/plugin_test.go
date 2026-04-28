package redisqueue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func withEnabledQueue(t *testing.T) {
	t.Helper()
	wasQueueEnabled := Enabled()
	wasStatsEnabled := internalusage.StatisticsEnabled()
	SetEnabled(true)
	internalusage.SetStatisticsEnabled(true)
	t.Cleanup(func() {
		SetEnabled(false)
		SetEnabled(wasQueueEnabled)
		internalusage.SetStatisticsEnabled(wasStatsEnabled)
	})
}

func TestUsageQueuePluginEnqueuesNormalizedPayload(t *testing.T) {
	withEnabledQueue(t)
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ginCtx.Request.RemoteAddr = "203.0.113.10:4321"
	ginCtx.Writer.WriteHeader(http.StatusOK)
	internallogging.SetGinRequestID(ginCtx, "gin-id")
	ctx := context.WithValue(internallogging.WithRequestID(context.Background(), "ctx-id"), "gin", ginCtx)

	requestedAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	plugin := &usageQueuePlugin{}
	plugin.HandleUsage(ctx, coreusage.Record{
		Provider:    "openai",
		Model:       "gpt-5.5",
		APIKey:      "client-key",
		AuthIndex:   "auth-1",
		AuthType:    "oauth",
		Source:      "user@example.com",
		RequestedAt: requestedAt,
		Latency:     1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:     10,
			OutputTokens:    20,
			ReasoningTokens: 3,
			CachedTokens:    4,
		},
	})

	items := PopOldest(1)
	if len(items) != 1 {
		t.Fatalf("queued items = %d, want 1", len(items))
	}
	var payload queuedUsageDetail
	if err := json.Unmarshal(items[0], &payload); err != nil {
		t.Fatalf("failed to decode queue payload: %v", err)
	}
	if payload.Provider != "openai" || payload.Model != "gpt-5.5" || payload.AuthType != "oauth" {
		t.Fatalf("payload metadata = provider %q model %q auth_type %q", payload.Provider, payload.Model, payload.AuthType)
	}
	if payload.APIKey != "client-key" || payload.AuthIndex != "auth-1" || payload.Source != "user@example.com" {
		t.Fatalf("payload identity fields mismatch: %+v", payload)
	}
	if payload.Endpoint != "POST /v1/responses" {
		t.Fatalf("endpoint = %q, want POST /v1/responses", payload.Endpoint)
	}
	if payload.RequestID != "ctx-id" {
		t.Fatalf("request_id = %q, want ctx-id", payload.RequestID)
	}
	if payload.LatencyMs != 1500 {
		t.Fatalf("latency_ms = %d, want 1500", payload.LatencyMs)
	}
	if payload.Tokens.TotalTokens != 33 {
		t.Fatalf("total_tokens = %d, want 33", payload.Tokens.TotalTokens)
	}
	if payload.Failed {
		t.Fatalf("failed = true, want false")
	}
}

func TestQueueDisabledClearsPayloads(t *testing.T) {
	SetEnabled(true)
	Enqueue([]byte("first"))
	SetEnabled(false)
	SetEnabled(true)
	t.Cleanup(func() { SetEnabled(false) })

	if items := PopOldest(1); len(items) != 0 {
		t.Fatalf("items after disable/enable = %d, want 0", len(items))
	}
}

func TestQueuePopNewestAndCapacity(t *testing.T) {
	SetEnabled(true)
	t.Cleanup(func() { SetEnabled(false) })

	Enqueue([]byte("oldest"))
	Enqueue([]byte("middle"))
	Enqueue([]byte("newest"))
	if items := PopNewest(2); len(items) != 2 || string(items[0]) != "newest" || string(items[1]) != "middle" {
		t.Fatalf("PopNewest = %q, want newest then middle", items)
	}
	if items := PopOldest(1); len(items) != 1 || string(items[0]) != "oldest" {
		t.Fatalf("remaining PopOldest = %q, want oldest", items)
	}

	for i := 0; i < maxQueueItems+5; i++ {
		Enqueue([]byte{byte(i)})
	}
	items := PopOldest(maxQueueItems + 10)
	if len(items) != maxQueueItems {
		t.Fatalf("items after capacity enforcement = %d, want %d", len(items), maxQueueItems)
	}
	if got := int(items[0][0]); got != 5 {
		t.Fatalf("oldest retained item = %d, want 5 after drop-oldest", got)
	}
}
