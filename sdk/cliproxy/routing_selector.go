package cliproxy

import (
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type routingSelectorState struct {
	strategy        string
	sessionAffinity bool
	sessionTTL      string
}

func selectorFromRoutingConfig(cfg *config.Config) coreauth.Selector {
	state := routingStateFromConfig(cfg)

	var selector coreauth.Selector
	switch state.strategy {
	case "fill-first":
		selector = &coreauth.FillFirstSelector{}
	default:
		selector = &coreauth.RoundRobinSelector{}
	}

	if state.sessionAffinity {
		ttl := time.Hour
		if parsed, err := time.ParseDuration(state.sessionTTL); err == nil && parsed > 0 {
			ttl = parsed
		}
		selector = coreauth.NewSessionAffinitySelectorWithConfig(coreauth.SessionAffinityConfig{
			Fallback: selector,
			TTL:      ttl,
		})
	}
	return selector
}

func routingStateFromConfig(cfg *config.Config) routingSelectorState {
	if cfg == nil {
		return routingSelectorState{strategy: "round-robin"}
	}
	return routingSelectorState{
		strategy:        normalizeRoutingStrategy(cfg.Routing.Strategy),
		sessionAffinity: cfg.Routing.ClaudeCodeSessionAffinity || cfg.Routing.SessionAffinity,
		sessionTTL:      strings.TrimSpace(cfg.Routing.SessionAffinityTTL),
	}
}

func normalizeRoutingStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "fill-first", "fillfirst", "ff":
		return "fill-first"
	default:
		return "round-robin"
	}
}
