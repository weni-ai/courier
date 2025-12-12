package utils

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// stubRoundTripper allows us to control responses and errors per attempt
type stubRoundTripper struct {
	mu             sync.Mutex
	attempts       int
	responses      []stubOutcome
	seenIdemKeys   []string
	seenCloseFlags []bool
	seenBodies     []string
}

type stubOutcome struct {
	status int
	body   string
	err    error
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	idx := s.attempts
	s.attempts++
	s.mu.Unlock()

	key := req.Header.Get("Idempotency-Key")
	bodyBytes, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()

	s.mu.Lock()
	s.seenIdemKeys = append(s.seenIdemKeys, key)
	s.seenCloseFlags = append(s.seenCloseFlags, req.Close)
	s.seenBodies = append(s.seenBodies, string(bodyBytes))
	s.mu.Unlock()

	if idx >= len(s.responses) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("OK")),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	}

	out := s.responses[idx]
	if out.err != nil {
		return nil, out.err
	}
	return &http.Response{
		StatusCode: out.status,
		Body:       io.NopCloser(strings.NewReader(out.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}, nil
}

func setupStubClient(t *testing.T, rt http.RoundTripper) func() {
	t.Helper()
	orig := GetHTTPClient()
	origTransport := orig.Transport
	origTimeout := orig.Timeout

	orig.Transport = rt
	orig.Timeout = 10 * time.Second

	return func() {
		orig.Transport = origTransport
		orig.Timeout = origTimeout
	}
}

func newReq(t *testing.T, method, url, body, idempotencyKey string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return req
}

func TestRetryOnConnResetThenSuccess(t *testing.T) {
	stub := &stubRoundTripper{responses: []stubOutcome{
		{err: syscall.ECONNRESET},
		{status: http.StatusOK, body: `{"ok":true}`},
	}}
	restore := setupStubClient(t, stub)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := newReq(t, http.MethodPost, "https://example.org/api", `{"a":1}`, "idem-123")
	rr, err := MakeHTTPRequestWithRetry(ctx, req, 3, 5*time.Millisecond, "idem-123")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if rr == nil || rr.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %+v", rr)
	}
	if stub.attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", stub.attempts)
	}
	if len(stub.seenIdemKeys) != 2 || stub.seenIdemKeys[0] != "idem-123" || stub.seenIdemKeys[1] != "idem-123" {
		t.Fatalf("idempotency key not propagated across attempts: %+v", stub.seenIdemKeys)
	}
	if len(stub.seenCloseFlags) != 2 || !stub.seenCloseFlags[0] || !stub.seenCloseFlags[1] {
		t.Fatalf("expected req.Close=true on all attempts, got: %+v", stub.seenCloseFlags)
	}
	if stub.seenBodies[0] != `{"a":1}` || stub.seenBodies[1] != `{"a":1}` {
		t.Fatalf("request body not reused correctly: %+v", stub.seenBodies)
	}
}

func TestNoRetryOn400(t *testing.T) {
	stub := &stubRoundTripper{responses: []stubOutcome{
		{status: http.StatusBadRequest, body: `{"err":"bad"}`},
	}}
	restore := setupStubClient(t, stub)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := newReq(t, http.MethodPost, "https://example.org/api", `{"a":1}`, "idem-400")
	_, err := MakeHTTPRequestWithRetry(ctx, req, 3, 1*time.Millisecond, "idem-400")
	if err == nil {
		t.Fatalf("expected error on 400, got nil")
	}
	if stub.attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", stub.attempts)
	}
}

func TestRetryOn503ThenSuccess(t *testing.T) {
	stub := &stubRoundTripper{responses: []stubOutcome{
		{status: http.StatusServiceUnavailable, body: `"busy"`},
		{status: http.StatusOK, body: `"ok"`},
	}}
	restore := setupStubClient(t, stub)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := newReq(t, http.MethodPost, "https://example.org/api", `{"x":2}`, "idem-503")
	rr, err := MakeHTTPRequestWithRetry(ctx, req, 5, 2*time.Millisecond, "idem-503")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if rr.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d", rr.StatusCode)
	}
	if stub.attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", stub.attempts)
	}
}

func TestAttemptsOneNoRetry(t *testing.T) {
	stub := &stubRoundTripper{responses: []stubOutcome{
		{err: syscall.ECONNRESET},
		{status: http.StatusOK, body: `"ok"`},
	}}
	restore := setupStubClient(t, stub)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := newReq(t, http.MethodPost, "https://example.org/api", `"z"`, "idem-1")
	_, err := MakeHTTPRequestWithRetry(ctx, req, 1, 1*time.Millisecond, "idem-1")
	if err == nil {
		t.Fatalf("expected error when only 1 attempt and first fails")
	}
	if stub.attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", stub.attempts)
	}
}

func TestRetryOnConnResetMultipleThenSuccess(t *testing.T) {
	stub := &stubRoundTripper{responses: []stubOutcome{
		{err: syscall.ECONNRESET},
		{err: syscall.ECONNRESET},
		{status: http.StatusOK, body: `{"ok":true}`},
	}}
	restore := setupStubClient(t, stub)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := newReq(t, http.MethodPost, "https://example.org/api", `{"a":1}`, "idem-2resets")
	rr, err := MakeHTTPRequestWithRetry(ctx, req, 5, 5*time.Millisecond, "idem-2resets")
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if rr == nil || rr.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %+v", rr)
	}
	if stub.attempts != 3 {
		t.Fatalf("expected 3 attempts (2 resets then success), got %d", stub.attempts)
	}
	if len(stub.seenIdemKeys) != 3 || stub.seenIdemKeys[0] != "idem-2resets" || stub.seenIdemKeys[1] != "idem-2resets" || stub.seenIdemKeys[2] != "idem-2resets" {
		t.Fatalf("idempotency key not propagated across attempts: %+v", stub.seenIdemKeys)
	}
	if len(stub.seenCloseFlags) != 3 || !stub.seenCloseFlags[0] || !stub.seenCloseFlags[1] || !stub.seenCloseFlags[2] {
		t.Fatalf("expected req.Close=true on all attempts, got: %+v", stub.seenCloseFlags)
	}
	if stub.seenBodies[0] != `{"a":1}` || stub.seenBodies[1] != `{"a":1}` || stub.seenBodies[2] != `{"a":1}` {
		t.Fatalf("request body not reused correctly: %+v", stub.seenBodies)
	}
}
