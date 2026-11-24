package weniwebchat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/urns"
	"github.com/stretchr/testify/require"
)

type recordedHit struct {
	body string
	auth string
	idem string
}

func newScriptServer(t *testing.T, codes []int, bodies []string) (*httptest.Server, *[]recordedHit) {
	t.Helper()
	hits := make([]recordedHit, 0)
	var n int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		hits = append(hits, recordedHit{
			body: string(raw),
			auth: r.Header.Get("Authorization"),
			idem: r.Header.Get("Idempotency-Key"),
		})
		code := http.StatusAccepted
		body := `{"result":"ok"}`
		if n < len(codes) {
			code = codes[n]
		}
		if n < len(bodies) && bodies[n] != "" {
			body = bodies[n]
		}
		n++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func setupHumanSupport(t *testing.T, token string, server *httptest.Server) (*handler, *courier.MockBackend, courier.Channel) {
	t.Helper()
	mb := courier.NewMockBackend()
	channel := courier.NewMockChannel(channelUUID, "WWC", "250788383383", "", map[string]interface{}{})
	channel.SetConfig(courier.ConfigBaseURL, server.URL)
	mb.AddChannel(channel)
	h := newHandler().(*handler)
	s := NewTestServer(mb)
	s.Config().WwcHumanSupportIngressToken = token
	require.NoError(t, h.Initialize(s))
	return h, mb, channel
}

func newSupportMsg(mb *courier.MockBackend, channel courier.Channel, metadata string) courier.Msg {
	msg := mb.NewOutgoingMsg(channel, courier.NewMsgID(10), urns.URN("ext:123"), "hello", false, nil, "", 0, "", "")
	msg.WithUUID(courier.NewMsgUUID())
	if metadata != "" {
		msg.WithMetadata(json.RawMessage(metadata))
	}
	return msg
}

func TestHumanSupportControl(t *testing.T) {
	previous := timestamp
	timestamp = "1616700878"
	t.Cleanup(func() { timestamp = previous })

	t.Run("started", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Len(t, *hits, 1)
		require.Equal(t, "Bearer test-token", (*hits)[0].auth)
		require.JSONEq(t, `{
			"type":"human_support_started",
			"to":"123",
			"from":"250788383383",
			"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c",
			"event_id":"evt-1",
			"occurred_at_ms":1730000000123
		}`, (*hits)[0].body)
		require.NotContains(t, (*hits)[0].body, `"message"`)
		require.NotContains(t, status.Logs()[0].Request, "test-token")
	})

	t.Run("replay", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, []string{`{"message":"human support started","result":"replay"}`})
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Len(t, *hits, 1)
	})

	t.Run("ended copies occurred_at_ms", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_ended","event_id":"evt-2","occurred_at_ms":1730000000456}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Contains(t, (*hits)[0].body, `"occurred_at_ms":1730000000456`)
	})

	t.Run("state open", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_state","event_id":"evt-3","open":true}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Contains(t, (*hits)[0].body, `"open":true`)
		require.NotContains(t, (*hits)[0].body, `"message"`)
	})

	t.Run("empty token", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgFailed, status.Status())
		require.Empty(t, *hits)
	})

	invalid := []string{
		`{"action_type":"human_support_started","event_id":"` + strings.Repeat("e", 129) + `","occurred_at_ms":1730000000123}`,
		`{"action_type":"human_support_started","occurred_at_ms":1730000000123}`,
		`{"action_type":"human_support_started","event_id":"","occurred_at_ms":1730000000123}`,
		`{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":0}`,
		`{"action_type":"human_support_state","event_id":"evt-1","open":false}`,
		`{"action_type":"human_support_state","event_id":"evt-1"}`,
	}
	for _, metadata := range invalid {
		metadata := metadata
		t.Run("invalid "+metadata, func(t *testing.T) {
			server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
			h, mb, channel := setupHumanSupport(t, "test-token", server)
			msg := newSupportMsg(mb, channel, metadata)
			status, err := h.SendMsg(context.Background(), msg)
			require.NoError(t, err)
			require.Equal(t, courier.MsgFailed, status.Status())
			require.Empty(t, *hits)
		})
	}

	for _, code := range []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusConflict} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			server, hits := newScriptServer(t, []int{code}, nil)
			h, mb, channel := setupHumanSupport(t, "test-token", server)
			msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
			status, err := h.SendMsg(context.Background(), msg)
			require.NoError(t, err)
			require.Equal(t, courier.MsgFailed, status.Status())
			require.Len(t, *hits, 1)
		})
	}

	t.Run("forbidden", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusForbidden}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgErrored, status.Status())
		require.Len(t, *hits, 1)
	})

	t.Run("start not found is one try", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusNotFound}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgErrored, status.Status())
		require.Len(t, *hits, 1)
	})

	t.Run("server error then success", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusInternalServerError, http.StatusInternalServerError, http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := h.SendMsg(ctx, msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Len(t, *hits, 3)
		require.Equal(t, "evt-1", (*hits)[0].idem)
		require.Equal(t, "evt-1", (*hits)[1].idem)
		require.Equal(t, "evt-1", (*hits)[2].idem)
	})

	t.Run("server error exhausted", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := h.SendMsg(ctx, msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgErrored, status.Status())
		require.Len(t, *hits, 3)
	})

	t.Run("cancelled context", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"human_support_started","event_id":"evt-1","occurred_at_ms":1730000000123}`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		status, err := h.SendMsg(ctx, msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgErrored, status.Status())
		require.LessOrEqual(t, len(*hits), 3)
	})
}

func TestHumanSupportStatus(t *testing.T) {
	t.Run("read without ids", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"message_status","event_id":"evt-r","status":"read"}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.JSONEq(t, `{
			"type":"message_status",
			"to":"123",
			"from":"250788383383",
			"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c",
			"event_id":"evt-r",
			"status":"read"
		}`, (*hits)[0].body)
		require.NotContains(t, (*hits)[0].body, "message_ids")
		require.NotContains(t, (*hits)[0].body, `"message"`)
		require.Equal(t, "Bearer test-token", (*hits)[0].auth)
	})

	t.Run("delivered with one id", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"message_status","event_id":"evt-d","status":"delivered","message_ids":["mid-1"]}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Contains(t, (*hits)[0].body, `"message_ids":["mid-1"]`)
	})

	invalid := []string{
		`{"action_type":"message_status","event_id":"evt-1","status":"sending"}`,
		`{"action_type":"message_status","event_id":"evt-1","status":"delivered"}`,
		`{"action_type":"message_status","event_id":"evt-1","status":"delivered","message_ids":[]}`,
		`{"action_type":"message_status","event_id":"evt-1","status":"delivered","message_ids":[1]}`,
		`{"action_type":"message_status","event_id":"` + strings.Repeat("e", 129) + `","status":"read"}`,
	}
	for _, metadata := range invalid {
		metadata := metadata
		t.Run("invalid", func(t *testing.T) {
			server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
			h, mb, channel := setupHumanSupport(t, "test-token", server)
			status, err := h.SendMsg(context.Background(), newSupportMsg(mb, channel, metadata))
			require.NoError(t, err)
			require.Equal(t, courier.MsgFailed, status.Status())
			require.Empty(t, *hits)
		})
	}

	t.Run("too many ids", func(t *testing.T) {
		ids := make([]string, 101)
		for i := range ids {
			ids[i] = "id"
		}
		raw, err := json.Marshal(map[string]interface{}{
			"action_type": "message_status",
			"event_id":    "evt-1",
			"status":      "delivered",
			"message_ids": ids,
		})
		require.NoError(t, err)
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		status, err := h.SendMsg(context.Background(), newSupportMsg(mb, channel, string(raw)))
		require.NoError(t, err)
		require.Equal(t, courier.MsgFailed, status.Status())
		require.Empty(t, *hits)
	})

	t.Run("not found then success", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusNotFound, http.StatusNotFound, http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"message_status","event_id":"evt-404","status":"read"}`)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := h.SendMsg(ctx, msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Len(t, *hits, 3)
		require.Equal(t, "evt-404", (*hits)[0].idem)
		require.Equal(t, "evt-404", (*hits)[2].idem)
	})

	t.Run("not found exhausted", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"message_status","event_id":"evt-404","status":"read"}`)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, err := h.SendMsg(ctx, msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgErrored, status.Status())
		require.Len(t, *hits, 3)
	})
}

func TestHumanSupportChat(t *testing.T) {
	previous := timestamp
	timestamp = "1616700878"
	t.Cleanup(func() { timestamp = previous })

	t.Run("ai agent", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"author":"ai_agent"}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Contains(t, (*hits)[0].body, `"author":"ai_agent"`)
		require.Empty(t, (*hits)[0].auth)
		require.NotContains(t, (*hits)[0].body, "agent_first_name")
	})

	t.Run("human agent", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"author":"human_agent","agent_first_name":"Julia"}`)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.Contains(t, (*hits)[0].body, `"author":"human_agent"`)
		require.Contains(t, (*hits)[0].body, `"agent_first_name":"Julia"`)
		require.NotContains(t, (*hits)[0].body, `"message":{"author"`)
		require.Equal(t, "Bearer test-token", (*hits)[0].auth)
		require.Equal(t, msg.UUID().String(), (*hits)[0].idem)
		require.NotContains(t, status.Logs()[0].Request, "test-token")
		require.NotContains(t, status.Logs()[0].Request, "Julia")
	})

	t.Run("human agent product", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		metadata := `{"author":"human_agent","agent_first_name":"Julia","action":"View","products":[{"product":"Products","product_retailer_info":[{"retailer_id":"p-1","name":"Item","price":"10.00","currency":"BRL","image":"https://example.com/item.jpg","description":"An item","seller_id":"seller"}]}]}`
		msg := newSupportMsg(mb, channel, metadata)
		status, err := h.SendMsg(context.Background(), msg)
		require.NoError(t, err)
		require.Equal(t, courier.MsgSent, status.Status())
		require.NotEmpty(t, *hits)
		require.Contains(t, (*hits)[0].body, `"author":"human_agent"`)
		require.Contains(t, (*hits)[0].body, `"agent_first_name":"Julia"`)
		require.Equal(t, "Bearer test-token", (*hits)[0].auth)
	})

	invalid := []string{
		`{"author":"human_agent","agent_first_name":"Julia Silva"}`,
		`{"author":"human_agent","agent_first_name":"` + strings.Repeat("a", 101) + `"}`,
		`{"author":"shopper"}`,
		`{"author":"ai_agent","agent_first_name":"Julia"}`,
	}
	for _, metadata := range invalid {
		metadata := metadata
		t.Run("invalid chat", func(t *testing.T) {
			server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
			h, mb, channel := setupHumanSupport(t, "test-token", server)
			status, err := h.SendMsg(context.Background(), newSupportMsg(mb, channel, metadata))
			require.NoError(t, err)
			require.Equal(t, courier.MsgFailed, status.Status())
			require.Empty(t, *hits)
		})
	}

	t.Run("privileged empty token", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusAccepted}, nil)
		h, mb, channel := setupHumanSupport(t, "", server)
		status, err := h.SendMsg(context.Background(), newSupportMsg(mb, channel, `{"author":"human_agent","agent_first_name":"Julia"}`))
		require.NoError(t, err)
		require.Equal(t, courier.MsgFailed, status.Status())
		require.Empty(t, *hits)
	})

	t.Run("named typing", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusOK}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		msg := newSupportMsg(mb, channel, `{"action_type":"typing_indicator","agent_first_name":"Julia"}`)
		status, err := h.SendAction(context.Background(), msg)
		require.NoError(t, err)
		require.Nil(t, status)
		require.Contains(t, (*hits)[0].body, `"type":"typing_start"`)
		require.Contains(t, (*hits)[0].body, `"agent_first_name":"Julia"`)
		require.Equal(t, "Bearer test-token", (*hits)[0].auth)
	})

	t.Run("typing stop without name", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusOK}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		_, err := h.SendAction(context.Background(), newSupportMsg(mb, channel, `{"action_type":"typing_stop"}`))
		require.NoError(t, err)
		require.Contains(t, (*hits)[0].body, `"type":"typing_stop"`)
		require.Empty(t, (*hits)[0].auth)
	})

	t.Run("typing stop with name", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusOK}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		_, err := h.SendAction(context.Background(), newSupportMsg(mb, channel, `{"action_type":"typing_stop","agent_first_name":"Julia"}`))
		require.NoError(t, err)
		require.Contains(t, (*hits)[0].body, `"type":"typing_stop"`)
		require.Equal(t, "Bearer test-token", (*hits)[0].auth)
	})

	t.Run("invalid typing name", func(t *testing.T) {
		server, hits := newScriptServer(t, []int{http.StatusOK}, nil)
		h, mb, channel := setupHumanSupport(t, "test-token", server)
		_, err := h.SendAction(context.Background(), newSupportMsg(mb, channel, `{"action_type":"typing_indicator","agent_first_name":"Julia Silva"}`))
		require.Error(t, err)
		require.Empty(t, *hits)
	})
}
