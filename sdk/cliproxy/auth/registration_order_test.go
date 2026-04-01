package auth

import (
	"context"
	"testing"
	"time"
)

type registrationOrderStore struct {
	items  []*Auth
	savedC chan *Auth
}

func (s *registrationOrderStore) List(context.Context) ([]*Auth, error) {
	out := make([]*Auth, 0, len(s.items))
	for _, auth := range s.items {
		out = append(out, auth.Clone())
	}
	return out, nil
}

func (s *registrationOrderStore) Save(_ context.Context, auth *Auth) (string, error) {
	if s.savedC != nil {
		select {
		case s.savedC <- auth.Clone():
		default:
		}
	}
	return "", nil
}

func (s *registrationOrderStore) Delete(context.Context, string) error { return nil }

func TestManagerUpdate_PreservesFirstRegisteredAt(t *testing.T) {
	t.Parallel()

	mgr := NewManager(nil, nil, nil)
	older := time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	if _, err := mgr.Register(context.Background(), &Auth{
		ID:        "alpha.json",
		Provider:  "codex",
		CreatedAt: older,
		Metadata: map[string]any{
			"type":                       "codex",
			FirstRegisteredAtMetadataKey: older.Format(time.RFC3339Nano),
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	if _, err := mgr.Update(context.Background(), &Auth{
		ID:       "alpha.json",
		Provider: "codex",
		Metadata: map[string]any{
			"type": "codex",
		},
	}); err != nil {
		t.Fatalf("update auth: %v", err)
	}

	updated, ok := mgr.GetByID("alpha.json")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	registeredAt, ok := FirstRegisteredAt(updated)
	if !ok {
		t.Fatalf("expected first registered time to be present")
	}
	if !registeredAt.Equal(older) {
		t.Fatalf("first registered time = %s, want %s", registeredAt, older)
	}
}

func TestManagerLoad_BackfillsFirstRegisteredAtAndPersists(t *testing.T) {
	t.Parallel()

	fallback := time.Date(2026, 3, 31, 23, 0, 0, 0, time.UTC)
	store := &registrationOrderStore{
		items: []*Auth{
			{
				ID:        "legacy.json",
				Provider:  "codex",
				CreatedAt: fallback,
				Metadata: map[string]any{
					"type": "codex",
				},
			},
		},
		savedC: make(chan *Auth, 1),
	}
	mgr := NewManager(store, nil, nil)

	if err := mgr.Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	loaded, ok := mgr.GetByID("legacy.json")
	if !ok || loaded == nil {
		t.Fatalf("expected loaded auth to be present")
	}
	registeredAt, ok := FirstRegisteredAt(loaded)
	if !ok {
		t.Fatalf("expected first registered time to be backfilled")
	}
	if !registeredAt.Equal(fallback) {
		t.Fatalf("first registered time = %s, want %s", registeredAt, fallback)
	}

	select {
	case saved := <-store.savedC:
		registeredAt, ok := FirstRegisteredAt(saved)
		if !ok || !registeredAt.Equal(fallback) {
			t.Fatalf("persisted auth first registered time = %v, want %s", registeredAt, fallback)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("expected Load() to persist backfilled first registered time")
	}
}
