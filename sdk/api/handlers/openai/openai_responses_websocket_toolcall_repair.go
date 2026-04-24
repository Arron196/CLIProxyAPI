package openai

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const websocketToolPairStateMaxEntries = 256

var defaultWebsocketToolPairStates = newWebsocketToolPairStateRegistry()
var defaultWebsocketToolPairRefs = newWebsocketToolPairRefCounter()

type websocketToolPairState struct {
	mu          sync.RWMutex
	outputs     map[string]json.RawMessage
	outputOrder []string
	calls       map[string]json.RawMessage
	callOrder   []string
}

type websocketToolPairStateRegistry struct {
	mu     sync.Mutex
	states map[string]*websocketToolPairState
}

type websocketToolPairRefCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newWebsocketToolPairState() *websocketToolPairState {
	return &websocketToolPairState{
		outputs: make(map[string]json.RawMessage),
		calls:   make(map[string]json.RawMessage),
	}
}

func newWebsocketToolPairStateRegistry() *websocketToolPairStateRegistry {
	return &websocketToolPairStateRegistry{states: make(map[string]*websocketToolPairState)}
}

func newWebsocketToolPairRefCounter() *websocketToolPairRefCounter {
	return &websocketToolPairRefCounter{counts: make(map[string]int)}
}

func (r *websocketToolPairStateRegistry) getOrCreate(sessionKey string) *websocketToolPairState {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || r == nil {
		return newWebsocketToolPairState()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if state, ok := r.states[sessionKey]; ok && state != nil {
		return state
	}
	state := newWebsocketToolPairState()
	r.states[sessionKey] = state
	return state
}

func (r *websocketToolPairStateRegistry) delete(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.states, sessionKey)
}

func (c *websocketToolPairRefCounter) acquire(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[sessionKey]++
}

func (c *websocketToolPairRefCounter) release(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	count := c.counts[sessionKey]
	if count <= 1 {
		delete(c.counts, sessionKey)
		return true
	}
	c.counts[sessionKey] = count - 1
	return false
}

func (s *websocketToolPairState) recordOutput(callID string, item json.RawMessage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recordWebsocketToolPairItem(s.outputs, &s.outputOrder, callID, item)
}

func (s *websocketToolPairState) recordCall(callID string, item json.RawMessage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recordWebsocketToolPairItem(s.calls, &s.callOrder, callID, item)
}

func (s *websocketToolPairState) getOutput(callID string) (json.RawMessage, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.outputs[strings.TrimSpace(callID)]
	if !ok || len(item) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), item...), true
}

func (s *websocketToolPairState) getCall(callID string) (json.RawMessage, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.calls[strings.TrimSpace(callID)]
	if !ok || len(item) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), item...), true
}

func recordWebsocketToolPairItem(store map[string]json.RawMessage, order *[]string, callID string, item json.RawMessage) {
	callID = strings.TrimSpace(callID)
	if callID == "" || store == nil || order == nil || len(item) == 0 {
		return
	}
	if _, exists := store[callID]; !exists {
		*order = append(*order, callID)
	}
	store[callID] = append(json.RawMessage(nil), item...)
	for len(*order) > websocketToolPairStateMaxEntries {
		evict := (*order)[0]
		*order = (*order)[1:]
		delete(store, evict)
	}
}

func websocketDownstreamSessionKey(req *http.Request) string {
	if req == nil {
		return ""
	}
	if requestID := strings.TrimSpace(req.Header.Get("X-Client-Request-Id")); requestID != "" {
		return requestID
	}
	if raw := strings.TrimSpace(req.Header.Get("X-Codex-Turn-Metadata")); raw != "" {
		if sessionID := strings.TrimSpace(gjson.Get(raw, "session_id").String()); sessionID != "" {
			return sessionID
		}
	}
	if sessionID := strings.TrimSpace(req.Header.Get("Session_id")); sessionID != "" {
		return sessionID
	}
	return ""
}

func acquireResponsesWebsocketToolPairState(sessionKey string) *websocketToolPairState {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return newWebsocketToolPairState()
	}
	defaultWebsocketToolPairRefs.acquire(sessionKey)
	return defaultWebsocketToolPairStates.getOrCreate(sessionKey)
}

func releaseResponsesWebsocketToolPairState(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	if !defaultWebsocketToolPairRefs.release(sessionKey) {
		return
	}
	defaultWebsocketToolPairStates.delete(sessionKey)
}

func repairResponsesWebsocketToolCalls(state *websocketToolPairState, payload []byte) []byte {
	if state == nil || len(payload) == 0 {
		return payload
	}

	input := gjson.GetBytes(payload, "input")
	if !input.Exists() || !input.IsArray() {
		return payload
	}
	if !shouldRepairResponsesWebsocketToolCalls(input.Raw) {
		return payload
	}

	allowOrphanOutputs := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()) != ""
	updatedRaw, errRepair := repairResponsesToolCallsArray(state, input.Raw, allowOrphanOutputs)
	if errRepair != nil || updatedRaw == "" || updatedRaw == input.Raw {
		return payload
	}

	updated, errSet := sjson.SetRawBytes(payload, "input", []byte(updatedRaw))
	if errSet != nil {
		return payload
	}
	return updated
}

func shouldRepairResponsesWebsocketToolCalls(inputRaw string) bool {
	inputRaw = strings.TrimSpace(inputRaw)
	if inputRaw == "" {
		return false
	}
	return strings.Contains(inputRaw, "function_call") || strings.Contains(inputRaw, "custom_tool_call")
}

func repairResponsesToolCallsArray(state *websocketToolPairState, rawArray string, allowOrphanOutputs bool) (string, error) {
	rawArray = strings.TrimSpace(rawArray)
	if rawArray == "" {
		return "[]", nil
	}

	var items []json.RawMessage
	if errUnmarshal := json.Unmarshal([]byte(rawArray), &items); errUnmarshal != nil {
		return "", errUnmarshal
	}

	outputPresent := make(map[string]json.RawMessage, len(items))
	callPresent := make(map[string]json.RawMessage, len(items))
	for _, item := range items {
		if len(item) == 0 {
			continue
		}
		callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
		if callID == "" {
			continue
		}
		switch strings.TrimSpace(gjson.GetBytes(item, "type").String()) {
		case "function_call", "custom_tool_call":
			if _, exists := callPresent[callID]; !exists {
				callPresent[callID] = append(json.RawMessage(nil), item...)
			}
		case "function_call_output", "custom_tool_call_output":
			if _, exists := outputPresent[callID]; !exists {
				outputPresent[callID] = append(json.RawMessage(nil), item...)
			}
		}
	}

	filtered := make([]json.RawMessage, 0, len(items)+2)
	insertedCalls := make(map[string]struct{}, len(items))
	insertedOutputs := make(map[string]struct{}, len(items))
	for _, item := range items {
		if len(item) == 0 {
			continue
		}

		itemType := strings.TrimSpace(gjson.GetBytes(item, "type").String())
		callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
		switch itemType {
		case "function_call", "custom_tool_call":
			if callID == "" {
				continue
			}
			if _, exists := outputPresent[callID]; exists {
				filtered = append(filtered, item)
				continue
			}
			if _, already := insertedOutputs[callID]; already {
				filtered = append(filtered, item)
				continue
			}
			if cachedOutput, ok := state.getOutput(callID); ok {
				filtered = append(filtered, item)
				filtered = append(filtered, cachedOutput)
				insertedOutputs[callID] = struct{}{}
			}
		case "function_call_output", "custom_tool_call_output":
			if callID == "" {
				continue
			}
			if allowOrphanOutputs {
				filtered = append(filtered, item)
				continue
			}
			if _, exists := callPresent[callID]; exists {
				filtered = append(filtered, item)
				continue
			}
			if cachedCall, ok := state.getCall(callID); ok {
				if _, already := insertedCalls[callID]; !already {
					filtered = append(filtered, cachedCall)
					insertedCalls[callID] = struct{}{}
				}
				filtered = append(filtered, item)
			}
		default:
			filtered = append(filtered, item)
		}
	}

	for _, item := range filtered {
		if len(item) == 0 {
			continue
		}
		callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
		if callID == "" {
			continue
		}
		switch strings.TrimSpace(gjson.GetBytes(item, "type").String()) {
		case "function_call", "custom_tool_call":
			state.recordCall(callID, item)
		case "function_call_output", "custom_tool_call_output":
			state.recordOutput(callID, item)
		}
	}

	out, errMarshal := json.Marshal(filtered)
	if errMarshal != nil {
		return "", errMarshal
	}
	return string(out), nil
}

func recordResponsesWebsocketToolCallsFromPayload(state *websocketToolPairState, payload []byte) {
	if state == nil || len(payload) == 0 {
		return
	}

	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	switch eventType {
	case "response.completed":
		output := gjson.GetBytes(payload, "response.output")
		if !output.Exists() || !output.IsArray() {
			return
		}
		for _, item := range output.Array() {
			switch strings.TrimSpace(item.Get("type").String()) {
			case "function_call", "custom_tool_call":
			default:
				continue
			}
			callID := strings.TrimSpace(item.Get("call_id").String())
			if callID == "" {
				continue
			}
			state.recordCall(callID, json.RawMessage(item.Raw))
		}
	case "response.output_item.added", "response.output_item.done":
		item := gjson.GetBytes(payload, "item")
		if !item.Exists() || !item.IsObject() {
			return
		}
		switch strings.TrimSpace(item.Get("type").String()) {
		case "function_call", "custom_tool_call":
		default:
			return
		}
		callID := strings.TrimSpace(item.Get("call_id").String())
		if callID == "" {
			return
		}
		state.recordCall(callID, json.RawMessage(item.Raw))
	}
}
