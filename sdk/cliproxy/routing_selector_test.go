package cliproxy

import (
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestSelectorFromRoutingConfig_DefaultsToRoundRobin(t *testing.T) {
	t.Parallel()

	selector := selectorFromRoutingConfig(&config.Config{})
	if _, ok := selector.(*coreauth.RoundRobinSelector); !ok {
		t.Fatalf("selector type = %T, want *RoundRobinSelector", selector)
	}
}

func TestSelectorFromRoutingConfig_FillFirst(t *testing.T) {
	t.Parallel()

	selector := selectorFromRoutingConfig(&config.Config{
		Routing: internalconfig.RoutingConfig{Strategy: "fill-first"},
	})
	if _, ok := selector.(*coreauth.FillFirstSelector); !ok {
		t.Fatalf("selector type = %T, want *FillFirstSelector", selector)
	}
}

func TestSelectorFromRoutingConfig_SessionAffinityWrapsFallback(t *testing.T) {
	t.Parallel()

	selector := selectorFromRoutingConfig(&config.Config{
		Routing: internalconfig.RoutingConfig{
			Strategy:           "fill-first",
			SessionAffinity:    true,
			SessionAffinityTTL: "90m",
		},
	})
	wrapped, ok := selector.(*coreauth.SessionAffinitySelector)
	if !ok {
		t.Fatalf("selector type = %T, want *SessionAffinitySelector", selector)
	}
	defer wrapped.Stop()
}

func TestSelectorFromRoutingConfig_LegacyClaudeCodeAliasEnablesSessionAffinity(t *testing.T) {
	t.Parallel()

	selector := selectorFromRoutingConfig(&config.Config{
		Routing: internalconfig.RoutingConfig{
			ClaudeCodeSessionAffinity: true,
		},
	})
	wrapped, ok := selector.(*coreauth.SessionAffinitySelector)
	if !ok {
		t.Fatalf("selector type = %T, want *SessionAffinitySelector", selector)
	}
	defer wrapped.Stop()
}

func TestRoutingStateFromConfig_NormalizesValues(t *testing.T) {
	t.Parallel()

	state := routingStateFromConfig(&config.Config{
		Routing: internalconfig.RoutingConfig{
			Strategy:           "FF",
			SessionAffinityTTL: " 1h ",
		},
	})
	if state.strategy != "fill-first" {
		t.Fatalf("state.strategy = %q, want %q", state.strategy, "fill-first")
	}
	if state.sessionTTL != "1h" {
		t.Fatalf("state.sessionTTL = %q, want %q", state.sessionTTL, "1h")
	}
	if state.sessionAffinity {
		t.Fatalf("state.sessionAffinity = true, want false")
	}
}

func TestNormalizeRoutingStrategy_Default(t *testing.T) {
	t.Parallel()

	if got := normalizeRoutingStrategy("unknown"); got != "round-robin" {
		t.Fatalf("normalizeRoutingStrategy() = %q, want %q", got, "round-robin")
	}
	if got := normalizeRoutingStrategy(""); got != "round-robin" {
		t.Fatalf("normalizeRoutingStrategy() empty = %q, want %q", got, "round-robin")
	}
}

func TestSelectorFromRoutingConfig_InvalidTTLUsesDefault(t *testing.T) {
	t.Parallel()

	selector := selectorFromRoutingConfig(&config.Config{
		Routing: internalconfig.RoutingConfig{
			SessionAffinity:    true,
			SessionAffinityTTL: "bad",
		},
	})
	wrapped, ok := selector.(*coreauth.SessionAffinitySelector)
	if !ok {
		t.Fatalf("selector type = %T, want *SessionAffinitySelector", selector)
	}
	defer wrapped.Stop()
	_ = time.Hour
}
