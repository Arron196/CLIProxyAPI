package audit

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type contextKey struct{}

// Event captures a single execution attempt for external analysis.
type Event struct {
	Timestamp      time.Time   `json:"timestamp"`
	RequestID      string      `json:"request_id,omitempty"`
	Provider       string      `json:"provider"`
	RequestedModel string      `json:"requested_model,omitempty"`
	UpstreamModel  string      `json:"upstream_model,omitempty"`
	SourceFormat   string      `json:"source_format,omitempty"`
	Stream         bool        `json:"stream"`
	Success        bool        `json:"success"`
	StatusCode     int         `json:"status_code,omitempty"`
	ErrorMessage   string      `json:"error_message,omitempty"`
	AuthID         string      `json:"auth_id,omitempty"`
	AuthLabel      string      `json:"auth_label,omitempty"`
	AuthFile       string      `json:"auth_file,omitempty"`
	AuthPath       string      `json:"auth_path,omitempty"`
	ClientRemote   string      `json:"client_remote,omitempty"`
	RequestHeaders http.Header `json:"request_headers,omitempty"`
	ClientRequest  []byte      `json:"client_request,omitempty"`
	ClientResponse []byte      `json:"client_response,omitempty"`
}

// State stores request-attempt audit data for one downstream request.
type State struct {
	maxBodyBytes int

	mu sync.Mutex

	timestamp      time.Time
	requestID      string
	sourceFormat   string
	stream         bool
	requestedModel string
	clientRemote   string
	requestHeaders http.Header
	clientRequest  []byte

	provider      string
	upstreamModel string
	authID        string
	authLabel     string
	authFile      string
	authPath      string
	clientResp    []byte
}

// ResultInfo captures the subset of execution result data needed by the audit package.
type ResultInfo struct {
	Provider     string
	Model        string
	Success      bool
	StatusCode   int
	ErrorMessage string
	AuthID       string
}

// WithRequest installs request audit state into the context.
func WithRequest(ctx context.Context, opts coreexecutor.Options, req coreexecutor.Request, maxBodyBytes int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	state := &State{
		maxBodyBytes:   normalizeMaxBodyBytes(maxBodyBytes),
		timestamp:      time.Now().UTC(),
		sourceFormat:   opts.SourceFormat.String(),
		stream:         opts.Stream,
		requestedModel: strings.TrimSpace(req.Model),
		requestHeaders: cloneHeader(opts.Headers),
		clientRequest:  compactAndTruncateCopy(opts.OriginalRequest, maxBodyBytes),
	}
	if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil {
		state.requestID = strings.TrimSpace(ginCtx.GetString("REQUEST_ID"))
		if state.requestID == "" && ginCtx.Request != nil {
			state.requestID = strings.TrimSpace(ginCtx.Request.Header.Get("X-Request-Id"))
			state.clientRemote = strings.TrimSpace(ginCtx.ClientIP())
			if len(state.requestHeaders) == 0 {
				state.requestHeaders = cloneHeader(ginCtx.Request.Header)
			}
		}
	}
	return context.WithValue(ctx, contextKey{}, state)
}

// FromContext returns the audit state stored on the context.
func FromContext(ctx context.Context) *State {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(contextKey{}).(*State)
	return state
}

// SetAttempt updates the selected auth and upstream model for the current attempt.
func SetAttempt(ctx context.Context, provider, upstreamModel, authID, authLabel, authFile, authPath string) {
	state := FromContext(ctx)
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.provider = strings.TrimSpace(provider)
	state.upstreamModel = strings.TrimSpace(upstreamModel)
	state.authID = strings.TrimSpace(authID)
	state.authLabel = strings.TrimSpace(authLabel)
	state.authFile = strings.TrimSpace(authFile)
	state.authPath = strings.TrimSpace(authPath)
}

// SetClientResponse stores a non-streaming downstream response body.
func SetClientResponse(ctx context.Context, payload []byte) {
	state := FromContext(ctx)
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.clientResp = compactAndTruncateCopy(payload, state.maxBodyBytes)
}

// AppendClientResponse appends a streaming downstream response chunk.
func AppendClientResponse(ctx context.Context, payload []byte) {
	state := FromContext(ctx)
	if state == nil || len(payload) == 0 {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.clientResp) >= state.maxBodyBytes {
		return
	}
	remain := state.maxBodyBytes - len(state.clientResp)
	if remain <= 0 {
		return
	}
	if len(payload) > remain {
		payload = payload[:remain]
	}
	state.clientResp = append(state.clientResp, payload...)
}

// Snapshot converts request state and execution result into an immutable audit event.
func Snapshot(ctx context.Context, result ResultInfo) *Event {
	state := FromContext(ctx)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	event := &Event{
		Timestamp:      state.timestamp,
		RequestID:      state.requestID,
		Provider:       firstNonEmpty(result.Provider, state.provider),
		RequestedModel: firstNonEmpty(result.Model, state.requestedModel),
		UpstreamModel:  state.upstreamModel,
		SourceFormat:   state.sourceFormat,
		Stream:         state.stream,
		Success:        result.Success,
		AuthID:         firstNonEmpty(result.AuthID, state.authID),
		AuthLabel:      state.authLabel,
		AuthFile:       state.authFile,
		AuthPath:       state.authPath,
		ClientRemote:   state.clientRemote,
		RequestHeaders: cloneHeader(state.requestHeaders),
		ClientRequest:  append([]byte(nil), state.clientRequest...),
		ClientResponse: append([]byte(nil), state.clientResp...),
		StatusCode:     result.StatusCode,
		ErrorMessage:   strings.TrimSpace(result.ErrorMessage),
	}
	if event.StatusCode == 0 && result.Success {
		event.StatusCode = http.StatusOK
	}
	return event
}

func normalizeMaxBodyBytes(maxBodyBytes int) int {
	if maxBodyBytes <= 0 {
		return 256 * 1024
	}
	return maxBodyBytes
}

func truncateCopy(payload []byte, maxBodyBytes int) []byte {
	if len(payload) == 0 {
		return nil
	}
	maxBodyBytes = normalizeMaxBodyBytes(maxBodyBytes)
	if len(payload) > maxBodyBytes {
		payload = payload[:maxBodyBytes]
	}
	return append([]byte(nil), payload...)
}

func compactAndTruncateCopy(payload []byte, maxBodyBytes int) []byte {
	if len(payload) == 0 {
		return nil
	}
	return truncateCopy(compactJSONPayload(payload), maxBodyBytes)
}

func cloneHeader(src http.Header) http.Header {
	if len(src) == 0 {
		return nil
	}
	dst := make(http.Header, len(src))
	for key, values := range src {
		copied := make([]string, len(values))
		copy(copied, values)
		dst[key] = copied
	}
	return dst
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
