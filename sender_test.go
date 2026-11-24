package courier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTemplateLastDispatchDataSkipsInvalidMetadata(t *testing.T) {
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})

	tests := []struct {
		name     string
		metadata json.RawMessage
	}{
		{name: "missing metadata", metadata: nil},
		{name: "missing templating", metadata: json.RawMessage(`{"foo":"bar"}`)},
		{name: "missing meta template id", metadata: json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test"},"language":"por"}}`)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := NewMockOutgoingMsg(channel, "hello", tc.metadata)
			_, ok := ExtractTemplateLastDispatchData(msg)
			require.False(t, ok)
		})
	}
}

func TestExtractTemplateLastDispatchDataValid(t *testing.T) {
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	data, ok := ExtractTemplateLastDispatchData(msg)
	require.True(t, ok)
	require.Equal(t, "44019537-9afe-4898-9626-a5c724d169ef", data.TemplateUUID)
	require.Equal(t, "template_test", data.Name)
	require.Equal(t, "123456789", data.MetaTemplateID)
}

func TestQueueTemplateLastDispatchSkipsInvalidMetadata(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	msg := NewMockOutgoingMsg(channel, "hello", nil)

	queueTemplateLastDispatch(backend, msg)
	require.Len(t, backend.templateLastDispatches, 0)
}

func TestQueueTemplateLastDispatchEnqueuesValidTemplate(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	queueTemplateLastDispatch(backend, msg)
	require.Len(t, backend.templateLastDispatches, 1)
	require.Equal(t, "123456789", backend.templateLastDispatches[0].data.MetaTemplateID)
}

func TestQueueTemplateLastDispatchUsesSentOnWhenPresent(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	sentOn := time.Now().Add(-time.Minute).UTC().Round(time.Microsecond)
	msg.(*mockMsg).sentOn = &sentOn

	queueTemplateLastDispatch(backend, msg)
	require.Len(t, backend.templateLastDispatches, 1)
	require.Equal(t, sentOn, backend.templateLastDispatches[0].firedOn.UTC())
}

func TestQueueTemplateLastDispatchDoesNotFailSendPath(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	status := backend.NewMsgStatusForID(channel, msg.ID(), MsgWired)
	require.NotNil(t, status)

	queueTemplateLastDispatch(backend, msg)
	require.Equal(t, MsgWired, status.Status())
}

func TestSignAttachments(t *testing.T) {
	var signerCalls []string
	mb := &MockBackend{}
	mb.SetAttachmentURLSigner(func(url string) (string, error) {
		signerCalls = append(signerCalls, url)
		if strings.Contains(url, "broken") {
			return "", errors.New("boom")
		}
		if strings.Contains(url, ".amazonaws.com/") {
			return url + "?X-Amz-Signature=abc", nil
		}
		return url, nil
	})

	logOutput := &bytes.Buffer{}
	logger := logrus.New()
	logger.SetOutput(logOutput)

	attachments := []string{
		"image/jpeg:https://courier-media.s3.sa-east-1.amazonaws.com/media/photo.jpg",
		"audio/mp3:https://example.com/audio.mp3",
		"geo:-2.90875,-79.0117686",
		"video/mp4:https://courier-media.s3.sa-east-1.amazonaws.com/media/broken.mp4",
	}
	original := append([]string{}, attachments...)

	signed := signAttachments(context.Background(), mb, attachments, logrus.NewEntry(logger))

	assert.Equal(t, []string{
		"image/jpeg:https://courier-media.s3.sa-east-1.amazonaws.com/media/photo.jpg?X-Amz-Signature=abc",
		"audio/mp3:https://example.com/audio.mp3",
		"geo:-2.90875,-79.0117686",
		"video/mp4:https://courier-media.s3.sa-east-1.amazonaws.com/media/broken.mp4",
	}, signed)
	assert.Equal(t, original, attachments, "input attachments must not be modified")
	assert.Equal(t, []string{
		"https://courier-media.s3.sa-east-1.amazonaws.com/media/photo.jpg",
		"https://example.com/audio.mp3",
		"https://courier-media.s3.sa-east-1.amazonaws.com/media/broken.mp4",
	}, signerCalls, "geo attachments must not be signed")
	assert.Contains(t, logOutput.String(), "error presigning attachment URL")
}

func TestSignAttachmentsWithoutSigner(t *testing.T) {
	attachments := []string{"image/jpeg:https://courier-media.s3.sa-east-1.amazonaws.com/media/photo.jpg"}

	signed := signAttachments(context.Background(), &MockBackend{}, attachments, logrus.NewEntry(logrus.New()))

	assert.Equal(t, attachments, signed)
}

func TestBestEffortAction(t *testing.T) {
	wwc := NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c568c", "WWC", "250788383383", "", map[string]interface{}{})
	other := NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c568c", "WAC", "250788383383", "", map[string]interface{}{})
	cases := []struct {
		name    string
		channel Channel
		action  string
		want    bool
	}{
		{name: "typing indicator wwc", channel: wwc, action: "typing_indicator", want: true},
		{name: "typing indicator other", channel: other, action: "typing_indicator", want: true},
		{name: "typing stop wwc", channel: wwc, action: "typing_stop", want: true},
		{name: "typing stop other", channel: other, action: "typing_stop", want: false},
		{name: "human support started", channel: wwc, action: "human_support_started", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &mockMsg{
				channel:  tc.channel,
				metadata: json.RawMessage(fmt.Sprintf(`{"action_type":%q}`, tc.action)),
			}
			assert.Equal(t, tc.want, bestEffortAction(msg))
		})
	}
}
