package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestSimHashSelectorPrefersColdStartAuthsRoundRobin(t *testing.T) {
	selector := &SimHashSelector{}
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
	selector := &SimHashSelector{}
	auths := []*Auth{
		{ID: "a", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: 0},
		{ID: "b", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: ^uint64(0)},
	}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.RequestSimHashMetadataKey: uint64(1)}}

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
	selector := &SimHashSelector{}
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

	selected, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("pick error: %v", err)
	}
	if selected.ID != "b" {
		t.Fatalf("selected %q, want b", selected.ID)
	}
}

func TestSimHashSelectorUsesStableTieBreak(t *testing.T) {
	selector := &SimHashSelector{}
	auths := []*Auth{
		{ID: "b", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: 0},
		{ID: "a", Provider: "codex", Status: StatusActive, HasLastRequestSimHash: true, LastRequestSimHash: 3},
	}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.RequestSimHashMetadataKey: uint64(1)}}

	selected, err := selector.Pick(context.Background(), "codex", "gpt-5.4", opts, auths)
	if err != nil {
		t.Fatalf("pick error: %v", err)
	}
	if selected.ID != "a" {
		t.Fatalf("selected %q, want a on tie-break", selected.ID)
	}
}
