package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/buger/jsonparser"
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
// the incoming message, or nil when the payload carries no email context.
// Subject is always stored when present so outbound replies can reuse it even
// when the inbound message is not itself a reply.
func buildThreadMetadata(payload *moPayload) json.RawMessage {
	inReplyTo := normalizeMessageID(payload.InReplyTo)
	references := normalizeReferences(payload.References)
	subject := strings.TrimSpace(payload.Subject)

	if inReplyTo == "" && len(references) == 0 && subject == "" {
		return nil
	}

	body, err := json.Marshal(map[string]emailThreadMetadata{
		"email": {
			InReplyTo:  inReplyTo,
			References: references,
			Subject:    subject,
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

	inReplyTo, references, subject := h.buildReplyContext(ctx, msg)

	payload := moPayload{
		UUID:        msg.UUID(),
		From:        senderEmail,
		To:          msg.URN().Path(),
		Subject:     subject,
		Body:        msg.Text(),
		Attachments: attachments,
		ChannelUUID: msg.Channel().UUID(),
		InReplyTo:   inReplyTo,
		References:  references,
	}

	// disable HTML escaping so RFC 5322 IDs ("<id@host>") keep their literal
	// angle brackets in the outbound payload instead of leaking \u003c/\u003e
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))

	rr, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	if err == nil {
		status.SetStatus(courier.MsgWired)

		// the proxy returns the RFC Message-ID it actually used to send the
		// email; persist it so the next turn can reference this message as
		// the parent of the thread
		if returnedID, jpErr := jsonparser.GetString(rr.Body, "message_id"); jpErr == nil {
			if normalized := normalizeMessageID(returnedID); normalized != "" && len(normalized) <= maxExternalIDLength {
				status.SetExternalID(normalized)
			}
		}
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

// rePrefixPattern matches a leading "Re:" (any case, optional surrounding
// whitespace) so we can avoid producing "Re: Re: ..." chains on long threads.
var rePrefixPattern = regexp.MustCompile(`(?i)^\s*re\s*:\s*`)

// buildReplyContext produces the RFC 5322 threading fields for an outbound
// reply. When no parent can be resolved it returns empty values and the
// subject derived from the message body, preserving the legacy "new
// conversation" behaviour.
//
// When a parent is found, its accumulated References chain and original subject
// are carried forward — falling back gracefully to a single-entry chain if the
// parent row can't be loaded from the store.
func (h *handler) buildReplyContext(ctx context.Context, msg courier.Msg) (inReplyTo string, references []string, subject string) {
	subject = subjectFromMsg(msg)
	inReplyTo = h.resolveParentExternalID(ctx, msg)
	if inReplyTo == "" {
		return "", nil, subject
	}

	references = make([]string, 0, 2)
	if parent, err := h.Backend().LookupMsgByExternalID(ctx, msg.Channel(), inReplyTo); err == nil && parent != nil {
		if meta := parseEmailThreadMetadata(parent.Metadata()); meta != nil {
			references = append(references, meta.References...)
			if meta.Subject != "" {
				subject = meta.Subject
			}
		}
	}
	references = append(references, inReplyTo)
	references = normalizeReferences(references)

	if !rePrefixPattern.MatchString(subject) {
		subject = "Re: " + subject
	}
	subject = trunkString(subject, 56)
	return inReplyTo, references, subject
}

// resolveParentExternalID picks the Message-ID of the message being replied to.
// Mailroom sets response_to_external_id when a flow session is active; when it
// does not (e.g. ticket/chats without flow), we fall back to response_to_id and
// then to the latest message on the channel for this contact that has an
// external_id.
func (h *handler) resolveParentExternalID(ctx context.Context, msg courier.Msg) string {
	if id := normalizeMessageID(msg.ResponseToExternalID()); id != "" {
		return id
	}

	if msg.ResponseToID() != courier.NilMsgID {
		if parent, err := h.Backend().LookupMsgByID(ctx, msg.ResponseToID()); err == nil && parent != nil {
			if id := normalizeMessageID(parent.ExternalID()); id != "" {
				return id
			}
		}
	}

	if parent, err := h.Backend().LookupLastMsgWithExternalID(ctx, msg.Channel(), msg.URN()); err == nil && parent != nil {
		if id := normalizeMessageID(parent.ExternalID()); id != "" {
			return id
		}
	}

	return ""
}

// parseEmailThreadMetadata extracts the email-specific block previously written
// by buildThreadMetadata on the inbound parent. Returns nil when the metadata
// is empty or doesn't carry an "email" key.
func parseEmailThreadMetadata(raw json.RawMessage) *emailThreadMetadata {
	if len(raw) == 0 {
		return nil
	}
	var wrapper struct {
		Email *emailThreadMetadata `json:"email"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	return wrapper.Email
}
