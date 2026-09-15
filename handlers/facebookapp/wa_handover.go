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

	"github.com/buger/jsonparser"
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
	WaID        string `json:"wa_id"`
	UserID      string `json:"user_id"`
	PhoneNumber string `json:"phone_number"`
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
	PreviousOwnerRole string                   `json:"previous_owner_role"`
	NewOwnerRole      string                   `json:"new_owner_role"`
	Metadata          string                   `json:"metadata"`
	ConversationContext *wacConversationContext `json:"conversation_context"`
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

func resolveWAHandoverConversationContext(value wacHandoverValue) *wacConversationContext {
	if value.ConversationContext != nil {
		return value.ConversationContext
	}
	if value.ControlPassed != nil && value.ControlPassed.ConversationContext != nil {
		return value.ControlPassed.ConversationContext
	}
	return nil
}

func resolveWAHandoverSenderURN(sender *wacHandoverSender) (urns.URN, error) {
	if sender == nil {
		return urns.NilURN, errors.New("no sender identifier in handover")
	}

	switch {
	case sender.WaID != "":
		return urns.NewWhatsAppURN(sender.WaID)
	case sender.UserID != "":
		return urns.NewWhatsAppURN(sender.UserID)
	case sender.PhoneNumber != "":
		return urns.NewWhatsAppURN(strings.TrimPrefix(sender.PhoneNumber, "+"))
	default:
		return urns.NilURN, errors.New("no sender identifier in handover")
	}
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

func parseWAHandoverOccurredOn(value wacHandoverValue, entryTime int64) (time.Time, error) {
	occurredOn := time.Unix(entryTime, 0).UTC()
	if value.Timestamp == "" {
		return occurredOn, nil
	}

	ts, err := strconv.ParseInt(value.Timestamp, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid handover timestamp: %s", value.Timestamp)
	}

	return time.Unix(ts, 0).UTC(), nil
}

func messagingHandoverLogFields(value wacHandoverValue, occurredOn time.Time, contactURN string, contactName string, contextType string, outcome string) logrus.Fields {
	fields := logrus.Fields{
		"handover_type": value.Type,
		"occurred_on":   occurredOn,
		"outcome":       outcome,
	}

	if value.Sender != nil {
		if value.Sender.WaID != "" {
			fields["sender_wa_id"] = value.Sender.WaID
		}
		if value.Sender.UserID != "" {
			fields["sender_user_id"] = value.Sender.UserID
		}
		if value.Sender.PhoneNumber != "" {
			fields["sender_phone_number"] = value.Sender.PhoneNumber
		}
	}
	if contactURN != "" {
		fields["contact_urn"] = contactURN
	}
	if contactName != "" {
		fields["contact_name"] = contactName
	}
	if contextType != "" {
		fields["context_type"] = contextType
	}
	conversationContext := resolveWAHandoverConversationContext(value)
	if conversationContext != nil && conversationContext.History != nil {
		fields["history_item_count"] = len(conversationContext.History.Items)
	}
	if value.ControlPassed != nil {
		if value.ControlPassed.Metadata != "" {
			fields["handover_metadata"] = value.ControlPassed.Metadata
		}
		if value.ControlPassed.PreviousOwnerRole != "" {
			fields["previous_owner_role"] = value.ControlPassed.PreviousOwnerRole
		}
		if value.ControlPassed.NewOwnerRole != "" {
			fields["new_owner_role"] = value.ControlPassed.NewOwnerRole
		}
		if value.ControlPassed.PreviousOwner != nil {
			fields["previous_owner_app_id"] = value.ControlPassed.PreviousOwner.AppID
			fields["previous_owner_app_role"] = value.ControlPassed.PreviousOwner.AppRole
			fields["previous_owner_business_id"] = value.ControlPassed.PreviousOwner.BusinessID
		}
	}

	return fields
}

func logMessagingHandoverReceived(channel courier.Channel, fields logrus.Fields) {
	log := logrus.WithField("channel_uuid", channel.UUID())
	for key, value := range fields {
		log = log.WithField(key, value)
	}
	log.Info("wa conversation handover received")
}

func printRawMessagingHandoverChange(r *http.Request, entryIdx, changeIdx int) {
	body, err := handlers.ReadBody(r, 1000000)
	if err != nil {
		return
	}

	changeJSON, _, _, err := jsonparser.Get(body, "entry", fmt.Sprintf("[%d]", entryIdx), "changes", fmt.Sprintf("[%d]", changeIdx))
	if err != nil {
		fmt.Println("[messaging_handovers] webhook payload:", string(body))
		return
	}

	fmt.Println("[messaging_handovers] webhook payload:", string(changeJSON))
}

func (h *handler) processMessagingHandover(
	ctx context.Context,
	channel courier.Channel,
	value wacHandoverValue,
	entryTime int64,
	r *http.Request,
) (string, error) {
	occurredOn, err := parseWAHandoverOccurredOn(value, entryTime)
	if err != nil {
		return "", handlers.WriteAndLogRequestError(ctx, h, channel, nil, r, err)
	}

	if value.Type != wacHandoverTypeControlPassed {
		logMessagingHandoverReceived(channel, messagingHandoverLogFields(value, occurredOn, "", "", "", "ignored_type"))
		return fmt.Sprintf("ignoring handover type %s", value.Type), nil
	}

	contextType, contextText, ok := renderWAHandoverContextText(resolveWAHandoverConversationContext(value))
	if !ok {
		logMessagingHandoverReceived(channel, messagingHandoverLogFields(value, occurredOn, "", "", "", "skipped_no_context"))
		return "control_passed without conversation context", nil
	}

	urn, err := resolveWAHandoverSenderURN(value.Sender)
	if err != nil {
		return "", handlers.WriteAndLogRequestError(ctx, h, channel, nil, r, err)
	}

	contactName := ""
	for _, contact := range value.Contacts {
		if contact.WaID == urn.Path() || contact.UserID == urn.Path() {
			contactName = contact.Profile.Name
			break
		}
	}

	conversationContext := resolveWAHandoverConversationContext(value)

	event := courier.WAConversationHandoverEvent{
		ChannelUUID: channel.UUID(),
		ContactURN:  urn,
		ContactName: contactName,
		ContextType: contextType,
		ContextText: contextText,
		OccurredOn:  occurredOn,
	}

	if conversationContext != nil {
		event.ContextPayload, _ = json.Marshal(conversationContext)
	}

	if value.ControlPassed != nil {
		event.HandoverMetadata = value.ControlPassed.Metadata
		if value.ControlPassed.PreviousOwner != nil {
			event.PreviousOwnerAppID = value.ControlPassed.PreviousOwner.AppID
			event.PreviousOwnerAppRole = value.ControlPassed.PreviousOwner.AppRole
			event.PreviousOwnerBusinessID = value.ControlPassed.PreviousOwner.BusinessID
		} else if value.ControlPassed.PreviousOwnerRole != "" {
			event.PreviousOwnerAppRole = value.ControlPassed.PreviousOwnerRole
		}
	}

	logMessagingHandoverReceived(channel, messagingHandoverLogFields(value, occurredOn, string(urn.Identity()), contactName, contextType, "persisted"))

	if err := h.Backend().WriteWAConversationHandover(ctx, event); err != nil {
		return "", err
	}

	return "wa conversation handover persisted", nil
}
