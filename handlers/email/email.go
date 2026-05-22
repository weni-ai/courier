package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
)

var (
	sendURL   = ""
	authToken = ""
)

// maxExternalIDLength matches the size of the msgs_msg.external_id column.
// Message-IDs longer than this are skipped instead of truncated so we don't
// silently break thread resolution on a future reply.
const maxExternalIDLength = 255

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("EM"), "Email")}
}

func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	sendURL = s.Config().EmailProxyURL + "/send"
	if sendURL == "" {
		return errors.New("EMAIL_PROXY_URL is not set")
	}
	authToken = s.Config().EmailProxyAuthToken
	if authToken == "" {
		return errors.New("EMAIL_PROXY_AUTH_TOKEN is not set")
	}
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	return nil
}

func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &moPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	if payload.From == "" {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("field 'from' required"))
	}

	if payload.Body == "" {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("field 'body' required"))
	}

	urn, err := urns.NewURNFromParts(urns.EmailScheme, payload.From, "", "")
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	msg := h.Backend().NewIncomingMsg(channel, urn, payload.Body)

	for _, attachment := range payload.Attachments {
		msg.WithAttachment(attachment)
	}

	if messageID := normalizeMessageID(payload.MessageID); messageID != "" && len(messageID) <= maxExternalIDLength {
		msg.WithExternalID(messageID)
	}

	if metadata := buildThreadMetadata(payload); metadata != nil {
		msg.WithMetadata(metadata)
	}

	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

// buildThreadMetadata returns the email-specific metadata blob to stash on
// the incoming message, or nil when the payload carries no threading info.
// The original subject only rides along when threading data is present, since
// it is exclusively consumed by the outbound "Re:" prefix logic.
func buildThreadMetadata(payload *moPayload) json.RawMessage {
	inReplyTo := normalizeMessageID(payload.InReplyTo)
	references := normalizeReferences(payload.References)

	if inReplyTo == "" && len(references) == 0 {
		return nil
	}

	body, err := json.Marshal(map[string]emailThreadMetadata{
		"email": {
			InReplyTo:  inReplyTo,
			References: references,
			Subject:    strings.TrimSpace(payload.Subject),
		},
	})
	if err != nil {
		return nil
	}
	return body
}

// normalizeMessageID trims whitespace and ensures the RFC 5322 angle brackets
// are present, so we always store and compare against the canonical
// "<id@host>" form. Returns "" when the input is blank.
func normalizeMessageID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if !strings.HasPrefix(id, "<") {
		id = "<" + id
	}
	if !strings.HasSuffix(id, ">") {
		id = id + ">"
	}
	return id
}

// normalizeReferences normalizes each entry, drops empty/duplicate values
// while preserving the original order.
func normalizeReferences(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		ref = normalizeMessageID(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type moPayload struct {
	UUID        courier.MsgUUID     `json:"uuid,omitempty"`
	From        string              `json:"from,omitempty"`
	To          string              `json:"to,omitempty"`
	Body        string              `json:"body,omitempty"`
	Subject     string              `json:"subject,omitempty"`
	Attachments []string            `json:"attachments,omitempty"`
	ChannelUUID courier.ChannelUUID `json:"channel_uuid,omitempty"`

	// RFC 5322 threading headers forwarded by the email-proxy. All optional;
	// when absent we fall back to the legacy "new message" behaviour.
	MessageID  string   `json:"message_id,omitempty"`
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`
}

// emailThreadMetadata is the email-specific block stashed under msg.metadata
// so an outgoing reply can rebuild In-Reply-To/References and the "Re:"
// subject prefix without re-fetching the parent message.
type emailThreadMetadata struct {
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`
	Subject    string   `json:"subject,omitempty"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	attachments := []string{}
	for _, attachment := range msg.Attachments() {
		_, attachmentURL := handlers.SplitAttachment(attachment)
		attachments = append(attachments, attachmentURL)
	}

	senderEmail := msg.Channel().StringConfigForKey("username", "")
	if senderEmail == "" {
		return nil, errors.New("could not send msg without a sender email")
	}

	payload := moPayload{
		UUID:        msg.UUID(),
		From:        senderEmail,
		To:          msg.URN().Path(),
		Subject:     subjectFromMsg(msg),
		Body:        msg.Text(),
		Attachments: attachments,
		ChannelUUID: msg.Channel().UUID(),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))

	rr, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	if err == nil {
		status.SetStatus(courier.MsgWired)
	}
	status.AddLog(log)
	return status, nil
}

func trunkString(str string, maxSize int) string {
	if len(str) < maxSize {
		return str
	}
	return str[:maxSize]
}

// subjectFromMsg extracts the subject from the message, it will return the first line of the
// message, and return truncated with first 56 characteres if the first line is longer than 56 characters
func subjectFromMsg(msg courier.Msg) string {
	lines := strings.Split(msg.Text(), "\n")
	if len(lines) == 0 {
		return trunkString(msg.Text(), 56)
	}
	return trunkString(lines[0], 56)
}
