package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

const (
	defaultQueueSize      = 256
	defaultTimeoutSeconds = 5
)

// Hook asynchronously emits request audit events to an external endpoint.
type Hook struct {
	enabled      bool
	maxBodyBytes int
	providers    map[string]struct{}
	client       *http.Client
	endpoint     string
	queue        chan *Event
	stopOnce     sync.Once
}

// NewHook creates a new audit hook from config.
func NewHook(cfg *internalconfig.RequestAuditConfig) *Hook {
	if cfg == nil || !cfg.Enable || strings.TrimSpace(cfg.Endpoint) == "" {
		return nil
	}
	client, endpoint, err := newHTTPClient(strings.TrimSpace(cfg.Endpoint), cfg.TimeoutSeconds)
	if err != nil {
		log.Warnf("request audit disabled: %v", err)
		return nil
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	hook := &Hook{
		enabled:      true,
		maxBodyBytes: normalizeMaxBodyBytes(cfg.MaxBodyBytes),
		providers:    normalizeProviders(cfg.Providers),
		client:       client,
		endpoint:     endpoint,
		queue:        make(chan *Event, queueSize),
	}
	go hook.loop()
	return hook
}

// Enabled reports whether the hook is active.
func (h *Hook) Enabled() bool {
	return h != nil && h.enabled
}

// MaxBodyBytes returns the configured truncation limit for context capture.
func (h *Hook) MaxBodyBytes() int {
	if h == nil {
		return normalizeMaxBodyBytes(0)
	}
	return h.maxBodyBytes
}

// Emit queues a completed execution attempt for delivery.
func (h *Hook) Emit(ctx context.Context, result ResultInfo) {
	if !h.Enabled() {
		return
	}
	if len(h.providers) > 0 {
		if _, ok := h.providers[strings.ToLower(strings.TrimSpace(result.Provider))]; !ok {
			return
		}
	}
	event := Snapshot(ctx, result)
	if event == nil {
		return
	}
	select {
	case h.queue <- event:
	default:
		log.Warn("request audit queue full; dropping event")
	}
}

func (h *Hook) loop() {
	for event := range h.queue {
		if event == nil {
			continue
		}
		if err := h.send(event); err != nil {
			log.Warnf("request audit send failed: %v", err)
		}
	}
}

func (h *Hook) send(event *Event) error {
	payload, err := json.Marshal(h.prepareEvent(event))
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, h.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("request audit hook returned status %d", resp.StatusCode)
	}
	return nil
}

func (h *Hook) prepareEvent(event *Event) *Event {
	if event == nil {
		return nil
	}
	prepared := *event
	prepared.ClientRequest = compactJSONPayload(prepared.ClientRequest)
	prepared.ClientResponse = compactJSONPayload(prepared.ClientResponse)
	return &prepared
}

func newHTTPClient(rawEndpoint string, timeoutSeconds int) (*http.Client, string, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultTimeoutSeconds
	}
	timeout := time.Duration(timeoutSeconds) * time.Second

	if strings.HasPrefix(rawEndpoint, "unix://") {
		socketPath := strings.TrimPrefix(rawEndpoint, "unix://")
		tr := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		}
		return &http.Client{Transport: tr, Timeout: timeout}, "http://unix/", nil
	}

	parsed, err := url.Parse(rawEndpoint)
	if err != nil {
		return nil, "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", fmt.Errorf("unsupported request audit endpoint scheme: %s", parsed.Scheme)
	}
	return &http.Client{Timeout: timeout}, parsed.String(), nil
}

func normalizeProviders(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
			result[trimmed] = struct{}{}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
