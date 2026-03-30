package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestRequestSimHashStableAcrossKeyOrder(t *testing.T) {
	left := []byte(`{"model":"gpt-5.4","stream":true,"messages":[{"role":"user","content":"hello world"}]}`)
	right := []byte(`{"messages":[{"content":"hello world","role":"user"}],"stream":true,"model":"gpt-5.4"}`)

	lhash, lok := requestSimHash(left)
	rhash, rok := requestSimHash(right)
	if !lok || !rok {
		t.Fatalf("expected both payloads to hash successfully")
	}
	if lhash != rhash {
		t.Fatalf("hash mismatch: %d vs %d", lhash, rhash)
	}
}

func TestRequestSimHashCompactsLargeArraysAndStrings(t *testing.T) {
	a := []byte(`{"input":[1,2,3,4,5,6,7],"text":"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"}`)
	b := []byte(`{"input":[1,2,3,99,98,5,6,7],"text":"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"}`)

	_, okA := requestSimHash(a)
	_, okB := requestSimHash(b)
	if !okA || !okB {
		t.Fatalf("expected compacted payloads to hash successfully")
	}
}

func TestEnsureRequestSimHashMetadataOnlyForSimHashSelector(t *testing.T) {
	opts := cliproxyexecutor.Options{OriginalRequest: []byte(`{"model":"gpt-5.4"}`)}

	plain := ensureRequestSimHashMetadata(opts, &RoundRobinSelector{})
	if len(plain.Metadata) != 0 {
		t.Fatalf("round-robin metadata = %#v, want empty", plain.Metadata)
	}

	hashed := ensureRequestSimHashMetadata(opts, &SimHashSelector{})
	if _, ok := requestSimHashFromMetadata(hashed.Metadata); !ok {
		t.Fatalf("expected simhash metadata to be present")
	}
}

func TestMarkResultUpdatesSimHashOnFailure(t *testing.T) {
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.auths["auth-1"] = &Auth{ID: "auth-1", Provider: "codex", Status: StatusActive}

	ctx := withRequestSimHash(context.Background(), map[string]any{cliproxyexecutor.RequestSimHashMetadataKey: uint64(42)})
	manager.MarkResult(ctx, Result{
		AuthID:  "auth-1",
		Model:   "gpt-5.4",
		Success: false,
		Error:   &Error{HTTPStatus: 401, Message: "unauthorized"},
	})

	auth := manager.auths["auth-1"]
	if !auth.HasLastRequestSimHash || auth.LastRequestSimHash != 42 {
		t.Fatalf("last simhash = (%v, %d), want (true, 42)", auth.HasLastRequestSimHash, auth.LastRequestSimHash)
	}
	if !auth.Unavailable {
		t.Fatalf("auth should be unavailable after 401")
	}
	if auth.NextRetryAfter.Before(time.Now()) {
		t.Fatalf("expected future retry time after 401, got %v", auth.NextRetryAfter)
	}
}
