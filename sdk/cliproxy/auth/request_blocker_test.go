package auth

import (
	"net/http"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestRequestBodyHashFromOptionsStableAcrossJSONKeyOrder(t *testing.T) {
	leftOpts, left, lok := requestBodyHashFromOptions(cliproxyexecutor.Options{
		OriginalRequest: []byte(`{"b":2,"a":1}`),
	})
	rightOpts, right, rok := requestBodyHashFromOptions(cliproxyexecutor.Options{
		OriginalRequest: []byte("{\n  \"a\": 1,\n  \"b\": 2\n}"),
	})
	if !lok || !rok {
		t.Fatal("expected both hashes to be generated")
	}
	if left != right {
		t.Fatalf("hash mismatch: %q != %q", left, right)
	}
	if _, ok := requestBodyAnalysisFromMetadata(leftOpts.Metadata); !ok {
		t.Fatal("expected left options to cache canonical body analysis")
	}
	if _, ok := requestBodyAnalysisFromMetadata(rightOpts.Metadata); !ok {
		t.Fatal("expected right options to cache canonical body analysis")
	}
}

func TestBlockedRequestLRUEvictsOldest(t *testing.T) {
	lru := newBlockedRequestLRU(2)
	lru.Add("a")
	lru.Add("b")
	lru.Add("c")
	if lru.Contains("a") {
		t.Fatal("expected oldest entry to be evicted")
	}
	if !lru.Contains("b") || !lru.Contains("c") {
		t.Fatal("expected newest entries to remain")
	}
}

func TestIsBlockableInvalidRequestError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "invalid function parameters", err: &Error{HTTPStatus: http.StatusBadRequest, Message: "invalid_function_parameters"}, want: true},
		{name: "bad request invalid request error", err: &Error{HTTPStatus: http.StatusBadRequest, Message: "invalid_request_error: malformed payload"}, want: true},
		{name: "unprocessable entity", err: &Error{HTTPStatus: http.StatusUnprocessableEntity, Message: "unprocessable entity"}, want: true},
		{name: "unauthorized", err: &Error{HTTPStatus: http.StatusUnauthorized, Message: "unauthorized"}, want: false},
		{name: "quota", err: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota exceeded"}, want: false},
		{name: "timeout", err: &Error{Code: "deadline_exceeded", Message: "context deadline exceeded"}, want: false},
	}
	for _, tc := range tests {
		if got := isBlockableInvalidRequestError(tc.err); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
