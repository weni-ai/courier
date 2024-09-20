package handlers

import (
	"encoding/json"
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

func TestSendWebhooks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "success"}`))
	}))

	jsonBody, err := json.Marshal(moTemplatesPayload{})
	assert.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "https://foo.bar/webhook", strings.NewReader(string(jsonBody)))

	err = SendWebhooks(req, ts.URL, "", true)
	assert.NoError(t, err)
}
