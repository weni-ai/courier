package rapidpro

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/urns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testChannelUUID(t *testing.T) courier.ChannelUUID {
	t.Helper()
	channelUUID, err := courier.NewChannelUUID("dbc126ed-66bc-4e28-b67b-81dc3327c95d")
	require.NoError(t, err)
	return channelUUID
}

func TestBuildConversionEventsURL(t *testing.T) {
	url, err := buildConversionEventsURL("https://example.com/conversion/", "secret-token")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/conversion/?token=secret-token", url)
}

func TestBuildConversationStartedPayload(t *testing.T) {
	urn, _ := urns.NewURNFromParts(urns.WhatsAppScheme, "5511999999999", "", "")
	event := courier.CtwaEvent{
		CtwaClid:    "clid-123",
		ContactUrn:  urn,
		ChannelUUID: testChannelUUID(t),
		MessageID:   "wamid.test",
		Referral: courier.CtwaReferralSource{
			SourceID:   "source-1",
			SourceType: "ad",
		},
	}

	payload := buildConversationStartedPayload(event, "ad")
	assert.Equal(t, courier.ConversationStartedEventType, payload.EventType)
	assert.Equal(t, event.ChannelUUID.String(), payload.ChannelUUID)
	assert.Equal(t, urn.String(), payload.ContactUrn)
	assert.Equal(t, "wamid.test", payload.Payload["message_id"])
	assert.Equal(t, "source-1", payload.Payload["source_id"])
	assert.Equal(t, "ad", payload.Payload["source_type"])
}

func TestSendConversationStartedEvent(t *testing.T) {
	var receivedBody map[string]interface{}
	var receivedToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.URL.Query().Get("token")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urn, _ := urns.NewURNFromParts(urns.WhatsAppScheme, "5511999999999", "", "")
	channelUUID := testChannelUUID(t)
	event := courier.CtwaEvent{
		CtwaClid:    "clid-123",
		ContactUrn:  urn,
		ChannelUUID: channelUUID,
		MessageID:   "wamid.test",
		Referral: courier.CtwaReferralSource{
			SourceID:   "source-1",
			SourceType: "ad",
		},
	}

	b := &backend{
		config: &courier.Config{
			ConversionEventsURL:   server.URL + "/conversion/",
			ConversionEventsToken: "courier-token",
		},
	}

	err := b.sendConversationStartedEvent(event, "ad")
	require.NoError(t, err)
	assert.Equal(t, "courier-token", receivedToken)
	assert.Equal(t, courier.ConversationStartedEventType, receivedBody["event_type"])
	assert.Equal(t, channelUUID.String(), receivedBody["channel_uuid"])
}

func TestQueueConversationStartedEventAsync(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urn, _ := urns.NewURNFromParts(urns.WhatsAppScheme, "5511999999999", "", "")
	event := courier.CtwaEvent{
		CtwaClid:    "clid-123",
		ContactUrn:  urn,
		ChannelUUID: testChannelUUID(t),
		MessageID:   "wamid.test",
		Referral: courier.CtwaReferralSource{
			SourceID:   "source-1",
			SourceType: "ad",
		},
	}

	b := &backend{
		config: &courier.Config{
			ConversionEventsURL:   server.URL + "/conversion/",
			ConversionEventsToken: "courier-token",
		},
	}

	b.queueConversationStartedEvent(event, "ad")

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for conversation_started HTTP request")
	}
}

func TestConversationStartedEventsDisabledWithoutConfig(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urn, _ := urns.NewURNFromParts(urns.WhatsAppScheme, "5511999999999", "", "")
	event := courier.CtwaEvent{
		ContactUrn:  urn,
		ChannelUUID: testChannelUUID(t),
		Referral: courier.CtwaReferralSource{
			SourceID:   "source-1",
			SourceType: "ad",
		},
	}

	b := &backend{config: &courier.Config{}}
	b.queueConversationStartedEvent(event, "ad")
	time.Sleep(100 * time.Millisecond)
	assert.False(t, called)
}

func TestFlushConversationStartedFile(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urn, _ := urns.NewURNFromParts(urns.WhatsAppScheme, "5511999999999", "", "")
	event := courier.CtwaEvent{
		ContactUrn:  urn,
		ChannelUUID: testChannelUUID(t),
		Referral: courier.CtwaReferralSource{
			SourceID:   "source-1",
			SourceType: "ad",
		},
	}

	body, err := json.Marshal(event)
	require.NoError(t, err)

	b := &backend{
		config: &courier.Config{
			ConversionEventsURL:   server.URL + "/conversion/",
			ConversionEventsToken: "courier-token",
		},
	}

	err = b.flushConversationStartedFile("test.json", body)
	require.NoError(t, err)
	assert.True(t, received)
}
