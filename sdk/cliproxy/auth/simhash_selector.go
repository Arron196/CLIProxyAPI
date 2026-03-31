package auth

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/bits"
	"strconv"
	"sync"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

const (
	defaultSimHashPoolSize            = 10
	defaultSimHashAdmitCooldownSecond = 1
	simHashAdmissionEpoch             = 10 * time.Minute
)

type simHashPoolState struct {
	members             map[string]struct{}
	everFilled          bool
	lastAdmittedAt      time.Time
	preferredOutsiderID string
}

// SimHashSelector routes requests to the nearest available auth by request SimHash.
type SimHashSelector struct {
	mu            sync.Mutex
	pool          simHashPoolState
	cursors       map[string]int
	maxKeys       int
	poolSize      int
	admitCooldown time.Duration
}

func NewSimHashSelector(cfg internalconfig.RoutingSimHashConfig) *SimHashSelector {
	selector := &SimHashSelector{}
	selector.SetConfig(cfg)
	return selector
}

func (s *SimHashSelector) SetConfig(cfg internalconfig.RoutingSimHashConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.poolSize = cfg.PoolSize
	if s.poolSize <= 0 {
		s.poolSize = defaultSimHashPoolSize
	}
	cooldownSeconds := cfg.AdmitCooldownSeconds
	if cooldownSeconds <= 0 {
		cooldownSeconds = defaultSimHashAdmitCooldownSecond
	}
	s.admitCooldown = time.Duration(cooldownSeconds) * time.Second
	if s.pool.members == nil {
		s.pool.members = make(map[string]struct{})
	}
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

	s.mu.Lock()
	if s.pool.members == nil {
		s.pool.members = make(map[string]struct{})
	}
	s.prunePoolLocked(auths, now)
	poolMembers, outsiders := s.partitionAvailableLocked(available)

	if len(s.pool.members) < s.effectivePoolSizeLocked() && len(outsiders) > 0 {
		admitted := s.pickAdmissionCandidateLocked(now, outsiders)
		if admitted != nil {
			s.pool.members[admitted.ID] = struct{}{}
			if s.pool.preferredOutsiderID == admitted.ID {
				s.pool.preferredOutsiderID = ""
			}
			s.pool.lastAdmittedAt = now
			if len(s.pool.members) >= s.effectivePoolSizeLocked() {
				s.pool.everFilled = true
			}
			s.mu.Unlock()
			return admitted, nil
		}
	}

	if len(poolMembers) == 0 && len(outsiders) > 0 && (!s.pool.everFilled || now.Sub(s.pool.lastAdmittedAt) >= s.admitCooldownLocked()) {
		admitted := s.pickAdmissionCandidateLocked(now, outsiders)
		if admitted != nil {
			s.pool.members[admitted.ID] = struct{}{}
			if s.pool.preferredOutsiderID == admitted.ID {
				s.pool.preferredOutsiderID = ""
			}
			s.pool.lastAdmittedAt = now
			s.mu.Unlock()
			return admitted, nil
		}
	}

	candidates := poolMembers
	s.mu.Unlock()
	if len(candidates) == 0 {
		return nil, &Error{Code: "auth_not_found", Message: "no auth available in simhash pool"}
	}
	return s.pickBestCandidate(model, opts, candidates), nil
}

func (s *SimHashSelector) pickBestCandidate(model string, opts cliproxyexecutor.Options, auths []*Auth) *Auth {
	if len(auths) == 0 {
		return nil
	}
	if len(auths) == 1 {
		return auths[0]
	}
	warm := make([]*Auth, 0, len(auths))
	cold := make([]*Auth, 0, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		if auth.HasLastRequestSimHash {
			warm = append(warm, auth)
		} else {
			cold = append(cold, auth)
		}
	}
	if len(warm) == 0 {
		s.mu.Lock()
		selected := s.pickRoundRobinLocked("simhash:cold", cold)
		s.mu.Unlock()
		return selected
	}
	requestHash, ok := requestSimHashFromMetadata(opts.Metadata)
	if !ok {
		s.mu.Lock()
		selected := s.pickRoundRobinLocked("simhash:warm-nohash", warm)
		s.mu.Unlock()
		return selected
	}
	best := warm[0]
	bestDistance := bits.OnesCount64(best.LastRequestSimHash ^ requestHash)
	for _, candidate := range warm[1:] {
		distance := bits.OnesCount64(candidate.LastRequestSimHash ^ requestHash)
		if distance < bestDistance || (distance == bestDistance && candidate.ID < best.ID) {
			best = candidate
			bestDistance = distance
		}
	}
	return best
}

func (s *SimHashSelector) prunePoolLocked(allAuths []*Auth, now time.Time) {
	if len(s.pool.members) == 0 {
		return
	}
	globallyAvailableIDs := make(map[string]struct{}, len(allAuths))
	for _, auth := range allAuths {
		if auth == nil {
			continue
		}
		if blocked, _, _ := isAuthBlockedForModel(auth, "", now); blocked {
			continue
		}
		globallyAvailableIDs[auth.ID] = struct{}{}
	}
	for authID := range s.pool.members {
		if _, ok := globallyAvailableIDs[authID]; !ok {
			delete(s.pool.members, authID)
		}
	}
	if s.pool.preferredOutsiderID != "" {
		if _, ok := globallyAvailableIDs[s.pool.preferredOutsiderID]; !ok {
			s.pool.preferredOutsiderID = ""
		}
	}
}

func (s *SimHashSelector) partitionAvailableLocked(available []*Auth) ([]*Auth, []*Auth) {
	members := make([]*Auth, 0, len(available))
	outsiders := make([]*Auth, 0, len(available))
	for _, auth := range available {
		if auth == nil {
			continue
		}
		if _, ok := s.pool.members[auth.ID]; ok {
			members = append(members, auth)
		} else {
			outsiders = append(outsiders, auth)
		}
	}
	return members, outsiders
}

func (s *SimHashSelector) pickAdmissionCandidateLocked(now time.Time, auths []*Auth) *Auth {
	if len(auths) == 0 {
		s.pool.preferredOutsiderID = ""
		return nil
	}
	if len(auths) == 1 {
		s.pool.preferredOutsiderID = auths[0].ID
		return auths[0]
	}
	if preferred := s.findAuthByID(auths, s.pool.preferredOutsiderID); preferred != nil {
		return preferred
	}
	epoch := admissionEpoch(now)
	best := auths[0]
	bestScore := admissionOrderScore(epoch, best)
	for _, candidate := range auths[1:] {
		score := admissionOrderScore(epoch, candidate)
		if score < bestScore || (score == bestScore && candidate.ID < best.ID) {
			best = candidate
			bestScore = score
		}
	}
	s.pool.preferredOutsiderID = best.ID
	return best
}

func (s *SimHashSelector) findAuthByID(auths []*Auth, authID string) *Auth {
	if authID == "" {
		return nil
	}
	for _, auth := range auths {
		if auth != nil && auth.ID == authID {
			return auth
		}
	}
	return nil
}

func (s *SimHashSelector) pickRoundRobinLocked(key string, auths []*Auth) *Auth {
	if len(auths) == 0 {
		return nil
	}
	if len(auths) == 1 {
		return auths[0]
	}
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

func admissionEpoch(now time.Time) int64 {
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC().Unix() / int64(simHashAdmissionEpoch/time.Second)
}

func admissionOrderScore(epoch int64, auth *Auth) uint64 {
	if auth == nil {
		return 0
	}
	h := fnv.New64a()
	var buf [24]byte
	epochBytes := strconv.AppendInt(buf[:0], epoch, 10)
	_, _ = h.Write(epochBytes)
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(auth.ID))
	return h.Sum64()
}

func (s *SimHashSelector) effectivePoolSizeLocked() int {
	if s.poolSize <= 0 {
		return defaultSimHashPoolSize
	}
	return s.poolSize
}

func (s *SimHashSelector) admitCooldownLocked() time.Duration {
	if s.admitCooldown <= 0 {
		return time.Duration(defaultSimHashAdmitCooldownSecond) * time.Second
	}
	return s.admitCooldown
}

func (s *SimHashSelector) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("SimHashSelector(pool=%d,members=%d,everFilled=%v)", s.effectivePoolSizeLocked(), len(s.pool.members), s.pool.everFilled)
}
