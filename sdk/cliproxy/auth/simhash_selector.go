package auth

import (
	"context"
	"math/bits"
	"strings"
	"sync"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

// SimHashSelector routes requests to the nearest available auth by request SimHash.
type SimHashSelector struct {
	mu      sync.Mutex
	cursors map[string]int
	maxKeys int
}

func (s *SimHashSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	now := time.Now()
	available, err := getAvailableAuths(auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)
	if len(available) == 1 {
		return available[0], nil
	}

	cold := make([]*Auth, 0, len(available))
	for _, candidate := range available {
		if candidate != nil && !candidate.HasLastRequestSimHash {
			cold = append(cold, candidate)
		}
	}
	if len(cold) > 0 {
		return s.pickColdStart(provider, model, cold), nil
	}

	requestHash, ok := requestSimHashFromMetadata(opts.Metadata)
	if !ok {
		return s.pickColdStart(provider, model, available), nil
	}
	best := available[0]
	bestDistance := bits.OnesCount64(best.LastRequestSimHash ^ requestHash)
	for _, candidate := range available[1:] {
		distance := bits.OnesCount64(candidate.LastRequestSimHash ^ requestHash)
		if distance < bestDistance || (distance == bestDistance && candidate.ID < best.ID) {
			best = candidate
			bestDistance = distance
		}
	}
	return best, nil
}

func (s *SimHashSelector) pickColdStart(provider, model string, auths []*Auth) *Auth {
	if len(auths) == 0 {
		return nil
	}
	if len(auths) == 1 {
		return auths[0]
	}
	key := strings.ToLower(strings.TrimSpace(provider)) + ":" + canonicalModelKey(model)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cursors == nil {
		s.cursors = make(map[string]int)
	}
	limit := s.maxKeys
	if limit <= 0 {
		limit = 4096
	}
	if _, ok := s.cursors[key]; !ok && len(s.cursors) >= limit {
		s.cursors = make(map[string]int)
	}
	index := s.cursors[key]
	if index >= 2_147_483_640 {
		index = 0
	}
	s.cursors[key] = index + 1
	return auths[index%len(auths)]
}
