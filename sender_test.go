package courier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

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
