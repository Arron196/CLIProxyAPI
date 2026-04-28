package util

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestOpenAICompatibilityHelpersSkipDisabledEntries(t *testing.T) {
	cfg := &config.Config{
		OpenAICompatibility: []config.OpenAICompatibility{
			{
				Name:     "disabled",
				Disabled: true,
				Models: []config.OpenAICompatibilityModel{
					{Name: "upstream-disabled", Alias: "alias-disabled"},
				},
			},
			{
				Name: "enabled",
				Models: []config.OpenAICompatibilityModel{
					{Name: "upstream-enabled", Alias: "alias-enabled"},
				},
			},
		},
	}

	if IsOpenAICompatibilityAlias("alias-disabled", cfg) {
		t.Fatal("disabled OpenAI compatibility alias should not be routable")
	}
	if !IsOpenAICompatibilityAlias("alias-enabled", cfg) {
		t.Fatal("enabled OpenAI compatibility alias should be routable")
	}
	if compat, model := GetOpenAICompatibilityConfig("alias-disabled", cfg); compat != nil || model != nil {
		t.Fatalf("disabled alias resolved to compat=%v model=%v", compat, model)
	}
	if compat, model := GetOpenAICompatibilityConfig("alias-enabled", cfg); compat == nil || model == nil {
		t.Fatal("enabled alias should resolve")
	}
}
