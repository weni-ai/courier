package rapidpro

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
	"github.com/sirupsen/logrus"
)

type conversationStartedPayload struct {
	EventType   string                 `json:"event_type"`
	ChannelUUID string                 `json:"channel_uuid"`
	ContactUrn  string                 `json:"contact_urn"`
	Payload     map[string]interface{} `json:"payload"`
}

func buildConversationStartedPayload(event courier.CtwaEvent, sourceType string) conversationStartedPayload {
	return conversationStartedPayload{
		EventType:   courier.ConversationStartedEventType,
		ChannelUUID: event.ChannelUUID.String(),
		ContactUrn:  event.ContactUrn.String(),
		Payload: map[string]interface{}{
			"message_id":  event.MessageID,
			"source_id":   event.Referral.SourceID,
			"source_type": sourceType,
		},
	}
}

func (b *backend) conversationStartedEventsEnabled() bool {
	return b.config.ConversionEventsURL != "" && b.config.ConversionEventsToken != ""
}

func (b *backend) queueConversationStartedEvent(event courier.CtwaEvent, sourceType string) {
	if !b.conversationStartedEventsEnabled() {
		return
	}

	go func() {
		if err := b.sendConversationStartedEvent(event, sourceType); err != nil {
			logrus.WithError(err).WithField("ctwa_clid", event.CtwaClid).Error("error sending conversation_started event")
			if spoolErr := courier.WriteToSpool(b.config.SpoolDir, "conversion_events", event); spoolErr != nil {
				logrus.WithError(spoolErr).Error("error spooling conversation_started event")
			}
		}
	}()
}

func (b *backend) sendConversationStartedEvent(event courier.CtwaEvent, sourceType string) error {
	payload := buildConversationStartedPayload(event, sourceType)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	targetURL, err := buildConversionEventsURL(b.config.ConversionEventsURL, b.config.ConversionEventsToken)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	rr, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return err
	}
	if rr.Status != utils.RRStatusSuccess {
		return fmt.Errorf("unexpected status sending conversation_started event: %s", rr.Status)
	}

	return nil
}

func buildConversionEventsURL(baseURL, token string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}


func (b *backend) flushConversationStartedFile(filename string, contents []byte) error {
	event := courier.CtwaEvent{}
	err := json.Unmarshal(contents, &event)
	if err != nil {
		logrus.WithError(err).WithField("filename", filename).Error("error unmarshalling spool file, renaming")
		os.Rename(filename, fmt.Sprintf("%s.error", filename))
		return nil
	}


	sourceType, ok := courier.NormalizeCtwaSourceType(event.Referral.SourceType)
	if !ok {
		sourceType = event.Referral.SourceType
	}

	return b.sendConversationStartedEvent(event, sourceType)
}
