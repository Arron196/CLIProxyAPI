package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestAuthAutoRefreshWorkersFromConfig(t *testing.T) {
	t.Parallel()

	if got := authAutoRefreshWorkersFromConfig(nil); got != 0 {
		t.Fatalf("authAutoRefreshWorkersFromConfig(nil) = %d, want 0", got)
	}

	cfg := &config.Config{AuthAutoRefreshWorkers: 24}
	if got := authAutoRefreshWorkersFromConfig(cfg); got != 24 {
		t.Fatalf("authAutoRefreshWorkersFromConfig(cfg) = %d, want 24", got)
	}
}

func TestShouldRestartCoreAutoRefresh(t *testing.T) {
	t.Parallel()

	if shouldRestartCoreAutoRefresh(16, 16) {
		t.Fatalf("shouldRestartCoreAutoRefresh(16, 16) = true, want false")
	}
	if !shouldRestartCoreAutoRefresh(16, 24) {
		t.Fatalf("shouldRestartCoreAutoRefresh(16, 24) = false, want true")
	}
}
