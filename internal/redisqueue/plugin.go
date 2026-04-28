package redisqueue

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func init() {
	coreusage.RegisterPlugin(&usageQueuePlugin{})
}

type usageQueuePlugin struct{}

type queuedUsageDetail struct {
	internalusage.RequestDetail
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Endpoint  string `json:"endpoint"`
	AuthType  string `json:"auth_type"`
	APIKey    string `json:"api_key"`
	RequestID string `json:"request_id"`
}

func (p *usageQueuePlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	if !Enabled() || !internalusage.StatisticsEnabled() {
		return
	}

	timestamp := record.RequestedAt
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	provider := strings.TrimSpace(record.Provider)
	if provider == "" {
		provider = "unknown"
	}
	model := strings.TrimSpace(record.Model)
	if model == "" {
		model = "unknown"
	}
	authType := strings.TrimSpace(record.AuthType)
	if authType == "" {
		authType = "unknown"
	}

	failed := record.Failed
	if !failed {
		failed = !resolveSuccess(ctx)
	}
	detail := queuedUsageDetail{
		RequestDetail: internalusage.RequestDetail{
			Timestamp: timestamp,
			LatencyMs: normaliseLatency(record.Latency),
			Source:    record.Source,
			ClientIP:  resolveClientIP(ctx),
			AuthIndex: record.AuthIndex,
			Tokens:    normaliseDetail(record.Detail),
			Failed:    failed,
		},
		Provider:  provider,
		Model:     model,
		Endpoint:  resolveEndpoint(ctx),
		AuthType:  authType,
		APIKey:    record.APIKey,
		RequestID: resolveRequestID(ctx),
	}

	payload, err := json.Marshal(detail)
	if err != nil {
		return
	}
	Enqueue(payload)
}

func normaliseDetail(detail coreusage.Detail) internalusage.TokenStats {
	tokens := internalusage.TokenStats{
		InputTokens:     detail.InputTokens,
		OutputTokens:    detail.OutputTokens,
		ReasoningTokens: detail.ReasoningTokens,
		CachedTokens:    detail.CachedTokens,
		TotalTokens:     detail.TotalTokens,
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = detail.InputTokens + detail.OutputTokens + detail.ReasoningTokens
	}
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = detail.InputTokens + detail.OutputTokens + detail.ReasoningTokens + detail.CachedTokens
	}
	return tokens
}

func normaliseLatency(latency time.Duration) int64 {
	if latency <= 0 {
		return 0
	}
	return latency.Milliseconds()
}

func resolveSuccess(ctx context.Context) bool {
	ginCtx := ginContext(ctx)
	if ginCtx == nil {
		return true
	}
	status := ginCtx.Writer.Status()
	if status == 0 {
		return true
	}
	return status < http.StatusBadRequest
}

func resolveClientIP(ctx context.Context) string {
	ginCtx := ginContext(ctx)
	if ginCtx == nil || ginCtx.Request == nil {
		return ""
	}
	return internallogging.ResolveClientIP(ginCtx)
}

func resolveEndpoint(ctx context.Context) string {
	ginCtx := ginContext(ctx)
	if ginCtx == nil || ginCtx.Request == nil {
		return ""
	}
	path := ginCtx.FullPath()
	if path == "" {
		path = ginCtx.Request.URL.Path
	}
	method := ginCtx.Request.Method
	if method == "" {
		return path
	}
	if path == "" {
		return method
	}
	return method + " " + path
}

func resolveRequestID(ctx context.Context) string {
	if requestID := internallogging.GetRequestID(ctx); requestID != "" {
		return requestID
	}
	return internallogging.GetGinRequestID(ginContext(ctx))
}

func ginContext(ctx context.Context) *gin.Context {
	if ctx == nil {
		return nil
	}
	ginCtx, _ := ctx.Value("gin").(*gin.Context)
	return ginCtx
}
