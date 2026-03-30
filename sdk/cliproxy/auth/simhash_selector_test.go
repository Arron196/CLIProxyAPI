package auth

import (
	"context"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestSimHashSelectorPrefersColdStartAuthsRoundRobin(t *testing.T) {
	selector := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 10})
	auths := []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive},
		{ID: "b", Provider: "codex", Status: StatusActive},
	}
	opts := cliproxyexecutor.Options{}

	first, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("first pick error: %v", err)
	}
	second, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("second pick error: %v", err)
	}
	if first.ID != "a" || second.ID != "b" {
		t.Fatalf("cold-start order = %q, %q; want a, b", first.ID, second.ID)
	}
}

func TestSimHashSelectorChoosesNearestAvailableAuth(t *testing.T) {
	selector := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 2})
	auths := []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: 0},
		{ID: "b", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: ^uint64(0)},
	}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.RequestSimHashMetadataKey: uint64(1)}}

	_, _ = selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, auths)
	_, _ = selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, auths)
	selected, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("pick error: %v", err)
	}
	if selected.ID != "a" {
		t.Fatalf("selected %q, want a", selected.ID)
	}
}

func TestSimHashSelectorSkipsUnavailableAuths(t *testing.T) {
	now := time.Now()
	selector := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 2, AdmitCooldownSeconds: 60})
	auths := []*Auth{
		{
			ID:                   "a",
			Provider:             "codex",
			Status:               StatusActive,
			HasLastRequestSimHash: true,
			LastRequestSimHash:   0,
			ModelStates: map[string]*ModelState{
				"gpt-5.4": {
					Status:         StatusError,
					Unavailable:    true,
					NextRetryAfter: now.Add(30 * time.Minute),
				},
			},
		},
		{
			ID:                   "b",
			Provider:             "codex",
			Status:               StatusActive,
			HasLastRequestSimHash: true,
			LastRequestSimHash:   7,
		},
	}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.RequestSimHashMetadataKey: uint64(0)}}

	_, _ = selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive},
		{ID: "b", Provider: "codex", Status: StatusActive},
	})
	selected, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("pick error: %v", err)
	}
	if selected.ID != "b" {
		t.Fatalf("selected %q, want b", selected.ID)
	}
}

func TestSimHashSelectorUsesStableTieBreak(t *testing.T) {
	selector := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 2})
	auths := []*Auth{
		{ID: "b", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: 0},
		{ID: "a", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: 3},
	}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.RequestSimHashMetadataKey: uint64(1)}}

	_, _ = selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, auths)
	_, _ = selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, auths)
	selected, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("pick error: %v", err)
	}
	if selected.ID != "a" {
		t.Fatalf("selected %q, want a on tie-break", selected.ID)
	}
}

func TestSimHashSelectorPoolOnlyAdmitsOneNewAuthAfterFilled(t *testing.T) {
	selector := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 2, AdmitCooldownSeconds: 3600})
	auths := []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive},
		{ID: "b", Provider: "codex", Status: StatusActive},
	}
	first, _ := selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, auths)
	second, _ := selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, auths)
	if first == nil || second == nil || first.ID == second.ID {
		t.Fatalf("expected cold start to admit both auths, got %#v %#v", first, second)
	}

	fullAuths := []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: 1},
		{ID: "c", Provider: "codex", Status: StatusActive},
	}
	selected, err := selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, fullAuths)
	if err != nil {
		t.Fatalf("pick error: %v", err)
	}
	if selected.ID != "c" {
		t.Fatalf("selected %q, want newly admitted c", selected.ID)
	}

	blockedAuths := []*Auth{
		{ID: "c", Provider: "codex", Status: StatusActive},
		{ID: "d", Provider: "codex", Status: StatusActive},
	}
	selected, err = selector.Pick(context.Background(), "codex", "gpt-5.4", cliproxyexecutor.Options{}, blockedAuths)
	if err == nil {
		t.Fatalf("expected pool admission cooldown to block outsider admission, got %v", selected)
	}
}
