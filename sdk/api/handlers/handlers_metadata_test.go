package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestExecutionMetadata_OmitsIdempotencyKeyWhenHeaderMissing(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)

	meta := requestExecutionMetadata(context.WithValue(context.Background(), "gin", ctx))
	if len(meta) != 0 {
		t.Fatalf("requestExecutionMetadata() = %#v, want empty metadata", meta)
	}
}

func TestRequestExecutionMetadata_ForwardsIdempotencyKeyWhenPresent(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/v1/responses", nil)
	req.Header.Set("Idempotency-Key", "abc-123")
	ctx.Request = req

	meta := requestExecutionMetadata(context.WithValue(context.Background(), "gin", ctx))
	if got := meta[idempotencyKeyMetadataKey]; got != "abc-123" {
		t.Fatalf("requestExecutionMetadata() idempotency key = %#v, want %q", got, "abc-123")
	}
}
