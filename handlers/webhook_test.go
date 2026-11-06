package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendWebhooksExternal_ValidRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", nil)
	webhookConfig := map[string]interface{}{
		"url":     ts.URL,
		"headers": map[string]string{"Content-Type": "application/json"},
		"method":  "POST",
	}

	err := SendWebhooksExternal(req, webhookConfig)
	assert.NoError(t, err)
}

func TestSendWebhooksExternal_NoHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", nil)
	webhookConfig := map[string]interface{}{
		"url":    ts.URL,
		"method": "POST",
	}

	err := SendWebhooksExternal(req, webhookConfig)
	assert.NoError(t, err)
}

func TestSendWebhooksExternal_NoMethod(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", nil)
	webhookConfig := map[string]interface{}{
		"url": ts.URL,
	}

	err := SendWebhooksExternal(req, webhookConfig)
	assert.NoError(t, err)
}

func TestSendWebhooks(t *testing.T) {
	var gotPath, gotToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.URL.Query().Get("token")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))
	defer ts.Close()

	jsonBody, err := json.Marshal(moTemplatesPayload{})
	assert.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", strings.NewReader(string(jsonBody)))

	err = SendWebhooks(req, ts.URL, "flows-token")
	assert.NoError(t, err)
	assert.Equal(t, "/whatsapp_flows/", gotPath)
	assert.Equal(t, "flows-token", gotToken)
}

func TestSendWebhooksRestoresBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))
	defer ts.Close()

	originalBody := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{}}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", strings.NewReader(string(originalBody)))

	err := SendWebhooks(req, ts.URL, "")
	assert.NoError(t, err)

	restored, err := io.ReadAll(req.Body)
	assert.NoError(t, err)
	assert.Equal(t, originalBody, restored)
}

const (
	testIntegrationsToken = "test-integrations-token"
	testTemplateWebhook   = `{"object":"whatsapp_business_account","entry":[{"id":"waba-1","time":1,"changes":[{"field":"message_template_status_update","value":{"event":"APPROVED","message_template_id":42,"message_template_name":"welcome"}}]}]}`
)

type recordedIntegrationsRequest struct {
	path           string
	authorization  string
	contentType    string
	idempotencyKey string
	body           []byte
}

type integrationsStub struct {
	mu       sync.Mutex
	requests []recordedIntegrationsRequest
	statuses []int
	server   *httptest.Server
}

func newIntegrationsStub(t *testing.T, statuses ...int) *integrationsStub {
	t.Helper()
	stub := &integrationsStub{statuses: statuses}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		attempt := len(stub.requests)
		stub.requests = append(stub.requests, recordedIntegrationsRequest{
			path:           r.URL.Path,
			authorization:  r.Header.Get("Authorization"),
			contentType:    r.Header.Get("Content-Type"),
			idempotencyKey: r.Header.Get("Idempotency-Key"),
			body:           body,
		})
		status := http.StatusOK
		if attempt < len(stub.statuses) {
			status = stub.statuses[attempt]
		} else if len(stub.statuses) > 0 {
			status = stub.statuses[len(stub.statuses)-1]
		}
		stub.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *integrationsStub) recorded() []recordedIntegrationsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedIntegrationsRequest(nil), s.requests...)
}

func newTemplateWebhookRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "https://courier.test/c/wac/receive", strings.NewReader(testTemplateWebhook))
}

func expectedForwardBody(t *testing.T) []byte {
	t.Helper()
	payload := &moTemplatesPayload{}
	require.NoError(t, json.Unmarshal([]byte(testTemplateWebhook), payload))
	buf := &bytes.Buffer{}
	require.NoError(t, json.NewEncoder(buf).Encode(payload))
	return buf.Bytes()
}

func assertTokenNotLogged(t *testing.T, hook *LogCaptureHook) {
	t.Helper()
	for _, line := range hook.Lines(t) {
		assert.NotContains(t, line, testIntegrationsToken)
	}
}

func lastFailureEntry(t *testing.T, hook *LogCaptureHook) *logrus.Entry {
	t.Helper()
	for _, entry := range hook.Entries() {
		if entry.Data["event"] == integrationsForwardFailedEvent {
			return entry
		}
	}
	t.Fatalf("no %s log entry", integrationsForwardFailedEvent)
	return nil
}

func TestForwardToIntegrationsSendsBearerTokenAndIdempotencyKey(t *testing.T) {
	stub := newIntegrationsStub(t, http.StatusOK)
	req := newTemplateWebhookRequest()

	err := ForwardToIntegrations(context.Background(), req, stub.server.URL, testIntegrationsToken)
	require.NoError(t, err)

	forwardBody := expectedForwardBody(t)
	bodyHash := sha256.Sum256(forwardBody)

	requests := stub.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, integrationsWebhookPath, requests[0].path)
	assert.Equal(t, "Bearer "+testIntegrationsToken, requests[0].authorization)
	assert.Equal(t, "application/json", requests[0].contentType)
	assert.Equal(t, hex.EncodeToString(bodyHash[:]), requests[0].idempotencyKey)
	assert.Equal(t, forwardBody, requests[0].body)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, testTemplateWebhook, string(restored))
}

func TestForwardToIntegrationsFailsClosedWithoutToken(t *testing.T) {
	hook := CaptureLogs(t)
	stub := newIntegrationsStub(t, http.StatusOK)

	err := ForwardToIntegrations(context.Background(), newTemplateWebhookRequest(), stub.server.URL, "")

	assert.ErrorIs(t, err, errIntegrationsTokenNotConfigured)
	assert.Empty(t, stub.recorded())
	entry := lastFailureEntry(t, hook)
	assert.Equal(t, logrus.ErrorLevel, entry.Level)
	assert.Equal(t, integrationsForwardReasonTokenNotConfigured, entry.Data["reason"])
	assert.Equal(t, []string{"waba-1"}, entry.Data["waba_ids"])
}

func TestForwardToIntegrationsRetriesTransientFailure(t *testing.T) {
	stub := newIntegrationsStub(t, http.StatusServiceUnavailable, http.StatusOK)

	err := ForwardToIntegrations(context.Background(), newTemplateWebhookRequest(), stub.server.URL, testIntegrationsToken)
	require.NoError(t, err)

	requests := stub.recorded()
	require.Len(t, requests, 2)
	assert.Equal(t, requests[0].authorization, requests[1].authorization)
	assert.Equal(t, "Bearer "+testIntegrationsToken, requests[1].authorization)
	assert.NotEmpty(t, requests[0].idempotencyKey)
	assert.Equal(t, requests[0].idempotencyKey, requests[1].idempotencyKey)
}

func TestForwardToIntegrationsStopsAfterMaxAttempts(t *testing.T) {
	hook := CaptureLogs(t)
	stub := newIntegrationsStub(t, http.StatusServiceUnavailable)

	err := ForwardToIntegrations(context.Background(), newTemplateWebhookRequest(), stub.server.URL, testIntegrationsToken)

	require.Error(t, err)
	assert.Len(t, stub.recorded(), integrationsForwardAttempts)
	entry := lastFailureEntry(t, hook)
	assert.Equal(t, logrus.ErrorLevel, entry.Level)
	assert.Equal(t, integrationsForwardReasonTransientExhausted, entry.Data["reason"])
	assert.Equal(t, http.StatusServiceUnavailable, entry.Data["status_code"])
	assert.Equal(t, integrationsForwardAttempts, entry.Data["attempts"])
	assertTokenNotLogged(t, hook)
}

func TestForwardToIntegrationsDoesNotRetryNonTransientStatus(t *testing.T) {
	cases := []struct {
		status     int
		wantReason string
	}{
		{http.StatusUnauthorized, integrationsForwardReasonUnauthorized},
		{http.StatusBadRequest, integrationsForwardReasonRejected},
		{http.StatusInternalServerError, integrationsForwardReasonRejected},
	}

	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			hook := CaptureLogs(t)
			stub := newIntegrationsStub(t, c.status)

			err := ForwardToIntegrations(context.Background(), newTemplateWebhookRequest(), stub.server.URL, testIntegrationsToken)

			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("status_code=%d", c.status))
			assert.NotContains(t, err.Error(), testIntegrationsToken)
			assert.Len(t, stub.recorded(), 1)
			entry := lastFailureEntry(t, hook)
			assert.Equal(t, logrus.ErrorLevel, entry.Level)
			assert.Equal(t, c.wantReason, entry.Data["reason"])
			assert.Equal(t, c.status, entry.Data["status_code"])
			assertTokenNotLogged(t, hook)
		})
	}
}

func TestForwardToIntegrationsEndsWithinDeadline(t *testing.T) {
	originalTimeout := integrationsForwardTimeout
	integrationsForwardTimeout = 200 * time.Millisecond
	defer func() { integrationsForwardTimeout = originalTimeout }()

	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	defer close(release)

	start := time.Now()
	err := ForwardToIntegrations(context.Background(), newTemplateWebhookRequest(), ts.URL, testIntegrationsToken)

	require.Error(t, err)
	assert.Less(t, time.Since(start), integrationsForwardTimeout+300*time.Millisecond)
}
