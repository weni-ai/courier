package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))
	defer ts.Close()

	jsonBody, err := json.Marshal(moTemplatesPayload{})
	assert.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", strings.NewReader(string(jsonBody)))

	err = SendWebhooks(req, ts.URL, "", true)
	assert.NoError(t, err)
}

func TestSendWebhooksRestoresBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))
	defer ts.Close()

	originalBody := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{}}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", strings.NewReader(string(originalBody)))

	err := SendWebhooks(req, ts.URL, "", true)
	assert.NoError(t, err)

	restored, err := io.ReadAll(req.Body)
	assert.NoError(t, err)
	assert.Equal(t, originalBody, restored)
}
