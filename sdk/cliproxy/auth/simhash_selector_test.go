package auth

import (
	"context"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestSimHashSelectorPrefersColdStartAuthsUntilPoolFills(t *testing.T) {
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
	if first == nil || second == nil || first.ID == second.ID {
		t.Fatalf("expected cold-start to admit distinct auths, got %#v %#v", first, second)
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
			ID:                    "a",
			Provider:              "codex",
			Status:                StatusActive,
			HasLastRequestSimHash: true,
			LastRequestSimHash:    0,
			ModelStates: map[string]*ModelState{
				"gpt-5.4": {
					Status:         StatusError,
					Unavailable:    true,
					NextRetryAfter: now.Add(30 * time.Minute),
				},
			},
		},
		{
			ID:                    "b",
			Provider:              "codex",
			Status:                StatusActive,
			HasLastRequestSimHash: true,
			LastRequestSimHash:    7,
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
	if err != nil {
		t.Fatalf("pick error = %v, want existing pool member c to continue serving", err)
	}
	if selected == nil || selected.ID != "c" {
		t.Fatalf("selected = %#v, want existing pool member c", selected)
	}
	if _, ok := selector.pool.members["d"]; ok {
		t.Fatalf("expected outsider d to stay out of pool during cooldown, pool=%v", selector.pool.members)
	}
}

func TestSimHashSelectorAdmissionOrderChangesAcrossEpochs(t *testing.T) {
	auths := []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive},
		{ID: "b", Provider: "codex", Status: StatusActive},
		{ID: "c", Provider: "codex", Status: StatusActive},
	}
	firstEpochPick := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 10}).pickAdmissionCandidateLocked(time.Unix(0, 0), auths)
	secondEpochPick := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 10}).pickAdmissionCandidateLocked(time.Unix(int64((10*time.Minute)/time.Second), 0), auths)
	if firstEpochPick == nil || secondEpochPick == nil {
		t.Fatal("expected admission candidates")
	}
}

func TestSimHashSelectorKeepsPreferredOutsiderUntilItChangesState(t *testing.T) {
	selector := NewSimHashSelector(internalconfig.RoutingSimHashConfig{PoolSize: 1, AdmitCooldownSeconds: 3600})
	now := time.Unix(0, 0)
	outsiders := []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive},
		{ID: "b", Provider: "codex", Status: StatusActive},
		{ID: "c", Provider: "codex", Status: StatusActive},
	}

	first := selector.pickAdmissionCandidateLocked(now, outsiders)
	if first == nil {
		t.Fatal("expected first candidate")
	}
	second := selector.pickAdmissionCandidateLocked(now.Add(5*time.Minute), outsiders)
	if second == nil {
		t.Fatal("expected second candidate")
	}
	if second.ID != first.ID {
		t.Fatalf("preferred outsider drifted from %q to %q", first.ID, second.ID)
	}

	remaining := make([]*Auth, 0, len(outsiders)-1)
	for _, auth := range outsiders {
		if auth.ID != first.ID {
			remaining = append(remaining, auth)
		}
	}
	next := selector.pickAdmissionCandidateLocked(now.Add(6*time.Minute), remaining)
	if next == nil {
		t.Fatal("expected next candidate after removing preferred outsider")
	}
	if next.ID == first.ID {
		t.Fatalf("expected preferred outsider to advance after removal, still got %q", next.ID)
	}
}
