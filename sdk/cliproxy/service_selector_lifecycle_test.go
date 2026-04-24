package cliproxy

import (
	"context"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type serviceStoppableSelector struct {
	stops int
}

func (s *serviceStoppableSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*coreauth.Auth) (*coreauth.Auth, error) {
	if len(auths) == 0 {
		return nil, nil
	}
	return auths[0], nil
}

func (s *serviceStoppableSelector) Stop() {
	s.stops++
}

func TestServiceShutdown_StopsCurrentSelector(t *testing.T) {
	t.Parallel()

	selector := &serviceStoppableSelector{}
	manager := coreauth.NewManager(nil, selector, nil)
	service := &Service{
		coreManager: manager,
	}

	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if selector.stops != 1 {
		t.Fatalf("selector.stops = %d, want %d", selector.stops, 1)
	}
}
