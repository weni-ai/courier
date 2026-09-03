package facebookapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/urns"
	"github.com/sirupsen/logrus"
)

const (
	wacMessagingHandoversField   = "messaging_handovers"
	wacStandbyField              = "standby"
	wacHandoverTypeControlPassed = "control_passed"
	maxWAHandoverContextTextLen  = 32000
)

type wacConversationContext struct {
	Summary *struct {
		Text string `json:"text"`
	} `json:"summary"`
	History *struct {
		Items []wacHistoryItem `json:"items"`
	} `json:"history"`
}

type wacHistoryItem struct {
	From string `json:"from"`
	Role string `json:"role"`
	Type string `json:"type"`
	Text *struct {
		Body string `json:"body"`
	} `json:"text"`
}

type wacHandoverSender struct {
	WaID   string `json:"wa_id"`
	UserID string `json:"user_id"`
}

type wacHandoverRecipient struct {
	PhoneNumberID string `json:"phone_number_id"`
}

type wacHandoverControlPassed struct {
	PreviousOwner *struct {
		AppID      string `json:"app_id"`
		AppRole    string `json:"app_role"`
		BusinessID string `json:"business_id"`
	} `json:"previous_owner"`
	Metadata string `json:"metadata"`
}

type wacHandoverContact struct {
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
	WaID   string `json:"wa_id"`
	UserID string `json:"user_id,omitempty"`
}

type wacHandoverValue struct {
	Timestamp           string
	Type                string
	Sender              *wacHandoverSender
	ControlPassed       *wacHandoverControlPassed
	ConversationContext *wacConversationContext
	Contacts            []wacHandoverContact
}

func wacPhoneNumberID(metadata *struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}, recipient *wacHandoverRecipient) string {
	if metadata != nil && metadata.PhoneNumberID != "" {
		return metadata.PhoneNumberID
	}
	if recipient != nil && recipient.PhoneNumberID != "" {
		return recipient.PhoneNumberID
	}
	return ""
}

func renderWAHandoverContextText(ctx *wacConversationContext) (contextType string, contextText string, ok bool) {
	if ctx == nil {
		return "", "", false
	}

	if ctx.Summary != nil {
		text := strings.TrimSpace(ctx.Summary.Text)
		if text != "" {
			return courier.WAHandoverContextSummary, truncateWAHandoverContextText(text), true
		}
	}

	if ctx.History != nil && len(ctx.History.Items) > 0 {
		lines := make([]string, 0, len(ctx.History.Items))
		for _, item := range ctx.History.Items {
			role := wacHistoryItemRole(item)
			content := wacHistoryItemContent(item)
			if content == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("[%s] %s", role, content))
		}
		if len(lines) > 0 {
			return courier.WAHandoverContextHistory, truncateWAHandoverContextText(strings.Join(lines, "\n")), true
		}
	}

	return "", "", false
}

func wacHistoryItemRole(item wacHistoryItem) string {
	if role := strings.TrimSpace(item.From); role != "" {
		return role
	}
	if role := strings.TrimSpace(item.Role); role != "" {
		return role
	}
	return "user"
}

func wacHistoryItemContent(item wacHistoryItem) string {
	if item.Text != nil {
		if text := strings.TrimSpace(item.Text.Body); text != "" {
			return text
		}
	}
	if item.Type != "" && item.Type != "text" {
		return fmt.Sprintf("<%s>", item.Type)
	}
	return ""
}

func truncateWAHandoverContextText(text string) string {
	if utf8.RuneCountInString(text) <= maxWAHandoverContextTextLen {
		return text
	}

	runes := []rune(text)
	return "[truncated]" + string(runes[len(runes)-maxWAHandoverContextTextLen:])
}

func (h *handler) processMessagingHandover(
	ctx context.Context,
	channel courier.Channel,
	value wacHandoverValue,
	entryTime int64,
	r *http.Request,
) (string, error) {
	if value.Type != wacHandoverTypeControlPassed {
		return fmt.Sprintf("ignoring handover type %s", value.Type), nil
	}

	contextType, contextText, ok := renderWAHandoverContextText(value.ConversationContext)
	if !ok {
		logrus.WithField("channel_uuid", channel.UUID()).Info("control_passed without conversation context, skipping persist")
		return "control_passed without conversation context", nil
	}

	var urn urns.URN
	var err error
	if value.Sender != nil && value.Sender.WaID != "" {
		urn, err = urns.NewWhatsAppURN(value.Sender.WaID)
	} else if value.Sender != nil && value.Sender.UserID != "" {
		urn, err = urns.NewWhatsAppURN(value.Sender.UserID)
	} else {
		return "", handlers.WriteAndLogRequestError(ctx, h, channel, nil, r, errors.New("no sender identifier in handover"))
	}
	if err != nil {
		return "", handlers.WriteAndLogRequestError(ctx, h, channel, nil, r, err)
	}

	occurredOn := time.Unix(entryTime, 0).UTC()
	if value.Timestamp != "" {
		ts, parseErr := strconv.ParseInt(value.Timestamp, 10, 64)
		if parseErr != nil {
			return "", handlers.WriteAndLogRequestError(ctx, h, channel, nil, r, fmt.Errorf("invalid handover timestamp: %s", value.Timestamp))
		}
		occurredOn = time.Unix(ts, 0).UTC()
	}

	contactName := ""
	for _, contact := range value.Contacts {
		if contact.WaID == urn.Path() || contact.UserID == urn.Path() {
			contactName = contact.Profile.Name
			break
		}
	}

	event := courier.WAConversationHandoverEvent{
		ChannelUUID: channel.UUID(),
		ContactURN:  urn,
		ContactName: contactName,
		ContextType: contextType,
		ContextText: contextText,
		OccurredOn:  occurredOn,
	}

	if value.ConversationContext != nil {
		event.ContextPayload, _ = json.Marshal(value.ConversationContext)
	}

	if value.ControlPassed != nil {
		event.HandoverMetadata = value.ControlPassed.Metadata
		if value.ControlPassed.PreviousOwner != nil {
			event.PreviousOwnerAppID = value.ControlPassed.PreviousOwner.AppID
			event.PreviousOwnerAppRole = value.ControlPassed.PreviousOwner.AppRole
			event.PreviousOwnerBusinessID = value.ControlPassed.PreviousOwner.BusinessID
		}
	}

	if err := h.Backend().WriteWAConversationHandover(ctx, event); err != nil {
		return "", err
	}

	return "wa conversation handover persisted", nil
}
