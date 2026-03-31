package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestGetUsageStatistics_IncludesClientIP(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	stats := internalusage.NewRequestStatistics()
	recordManagementUsageWithRemoteAddr(t, stats, "198.51.100.7:1234", coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			InputTokens:  11,
			OutputTokens: 22,
			TotalTokens:  33,
		},
	})

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	h.SetUsageStatistics(stats)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage", nil)

	h.GetUsageStatistics(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Usage          internalusage.StatisticsSnapshot `json:"usage"`
		FailedRequests int64                            `json:"failed_requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	details := payload.Usage.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	if details[0].ClientIP != "198.51.100.7" {
		t.Fatalf("client_ip = %q, want %q", details[0].ClientIP, "198.51.100.7")
	}
	if payload.FailedRequests != 0 {
		t.Fatalf("failed_requests = %d, want 0", payload.FailedRequests)
	}
}

func TestExportImportUsageStatistics_PreservesClientIP(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	sourceStats := internalusage.NewRequestStatistics()
	recordManagementUsageWithRemoteAddr(t, sourceStats, "[2001:db8::1]:443", coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 27, 9, 0, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			InputTokens:  5,
			OutputTokens: 8,
			TotalTokens:  13,
		},
	})

	exportHandler := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	exportHandler.SetUsageStatistics(sourceStats)

	exportRec := httptest.NewRecorder()
	exportCtx, _ := gin.CreateTestContext(exportRec)
	exportCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage/export", nil)
	exportHandler.ExportUsageStatistics(exportCtx)

	if exportRec.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d, body=%s", exportRec.Code, http.StatusOK, exportRec.Body.String())
	}

	targetStats := internalusage.NewRequestStatistics()
	importHandler := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	importHandler.SetUsageStatistics(targetStats)

	importRec := httptest.NewRecorder()
	importCtx, _ := gin.CreateTestContext(importRec)
	importReq := httptest.NewRequest(http.MethodPost, "/v0/management/usage/import", bytes.NewReader(exportRec.Body.Bytes()))
	importReq.Header.Set("Content-Type", "application/json")
	importCtx.Request = importReq
	importHandler.ImportUsageStatistics(importCtx)

	if importRec.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d, body=%s", importRec.Code, http.StatusOK, importRec.Body.String())
	}

	snapshot := targetStats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	if details[0].ClientIP != "2001:db8::1" {
		t.Fatalf("client_ip = %q, want %q", details[0].ClientIP, "2001:db8::1")
	}
}

func TestImportUsageStatistics_AppliesRetentionDays(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	targetStats := internalusage.NewRequestStatistics()
	handler := NewHandlerWithoutConfigFilePath(&config.Config{
		UsageStatisticsRetentionDays: 30,
	}, nil)
	handler.SetUsageStatistics(targetStats)

	now := time.Now()
	payload := usageImportPayload{
		Version: 1,
		Usage: internalusage.StatisticsSnapshot{
			APIs: map[string]internalusage.APISnapshot{
				"test-key": {
					Models: map[string]internalusage.ModelSnapshot{
						"gpt-5.4": {
							Details: []internalusage.RequestDetail{
								{
									Timestamp: now.AddDate(0, 0, -40),
									Tokens:    internalusage.TokenStats{TotalTokens: 1},
								},
								{
									Timestamp: now.AddDate(0, 0, -2),
									Tokens:    internalusage.TokenStats{TotalTokens: 2},
								},
							},
						},
					},
				},
			},
		},
	}
	body, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		t.Fatalf("marshal payload: %v", errMarshal)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v0/management/usage/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	handler.ImportUsageStatistics(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	snapshot := targetStats.Snapshot()
	if snapshot.TotalRequests != 1 {
		t.Fatalf("snapshot.TotalRequests = %d, want 1", snapshot.TotalRequests)
	}
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
}

func recordManagementUsageWithRemoteAddr(t *testing.T, stats *internalusage.RequestStatistics, remoteAddr string, record coreusage.Record) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.RemoteAddr = remoteAddr
	ginCtx.Request = req

	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	stats.Record(ctx, record)
}
