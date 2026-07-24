package email

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
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
	"github.com/sirupsen/logrus"
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

	urn, err := buildContactURN(payload)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	msg := h.Backend().NewIncomingMsg(channel, urn, payload.Body).WithContactName(payload.From)

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

// threadTagPattern matches the synthetic sub-address tag we inject into the
// local part of an address to segregate conversations ("+wt-<8 hex chars>"
// right before the "@"). It is scoped to our own prefix so a real "+" already
// present in the contact's address (e.g. "person+work@gmail.com") is left
// untouched.
var threadTagPattern = regexp.MustCompile(`\+wt-[0-9a-f]{8}(@|$)`)

// buildContactURN derives the URN used to identify the contact for an inbound
// message. Each new conversation (a message that isn't a reply to anything we
// recognize) gets its own synthetic address derived from the real one, so
// Temba creates a distinct contact per subject/thread even though the sender's
// mailbox is the same. Replies within a thread resolve back to the same
// synthetic address — and therefore the same contact — because the tag is
// deterministically derived from the thread's root Message-ID.
//
// Messages that carry no threading headers at all (message_id/in_reply_to/
// references all empty) fall back to the real address, preserving the legacy
// "one contact per mailbox" behaviour.
func buildContactURN(payload *moPayload) (urns.URN, error) {
	address := payload.From
	if anchor := threadAnchor(payload); anchor != "" {
		address = withThreadTag(address, anchor)
	}
	return urns.NewURNFromParts(urns.EmailScheme, address, "", "")
}

// threadAnchor returns the Message-ID that identifies the root of the
// conversation an inbound message belongs to: the oldest entry in References
// when present (RFC 5322 orders References oldest-first), otherwise
// In-Reply-To, otherwise the message's own Message-ID — meaning a message
// with no reply headers anchors a brand new thread. Returns "" when the
// payload carries no threading information at all.
func threadAnchor(payload *moPayload) string {
	if len(payload.References) > 0 {
		if root := normalizeMessageID(payload.References[0]); root != "" {
			return root
		}
	}
	if id := normalizeMessageID(payload.InReplyTo); id != "" {
		return id
	}
	if id := normalizeMessageID(payload.MessageID); id != "" {
		return id
	}
	return ""
}

// withThreadTag inserts a short, deterministic tag derived from the thread
// anchor into the local part of the address, immediately before the "@".
func withThreadTag(address, anchor string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return address
	}
	local, domain := address[:at], address[at:]
	return fmt.Sprintf("%s+wt-%s%s", local, threadHash(anchor), domain)
}

// threadHash returns a short, stable, URL/email-safe fingerprint of the
// thread anchor. It doesn't need to be cryptographically strong — only
// deterministic and low-collision for the same mailbox.
func threadHash(anchor string) string {
	sum := sha1.Sum([]byte(anchor))
	return hex.EncodeToString(sum[:])[:8]
}

// realAddressFromURNPath strips a thread tag we may have injected into the
// local part of the contact's URN, returning the actual deliverable mailbox
// address. Addresses without our tag (legacy contacts, or payloads with no
// threading info) are returned unchanged.
func realAddressFromURNPath(path string) string {
	return threadTagPattern.ReplaceAllString(path, "$1")
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
		To:          realAddressFromURNPath(msg.URN().Path()),
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

		// stash the thread context we just sent on this outbound message so a
		// subsequent send that resolves us as parent can reuse subject and
		// extend References — without this, the next turn falls back to the
		// body-derived subject and starts a new thread in most clients
		if emailMeta := buildOutboundThreadMetadata(inReplyTo, references, subject); emailMeta != nil {
			if merged := mergeEmailMetadata(msg.Metadata(), emailMeta); merged != nil {
				status.SetMetadata(merged)
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
// conversation" behaviour (no "Re:" prefix).
//
// When a parent is found, its accumulated References chain and original subject
// are carried forward — falling back gracefully to a single-entry chain if the
// parent row can't be loaded from the store. The "Re:" prefix is only applied
// when we reuse a parent subject; brand-new sends keep the body-derived subject
// as-is.
func (h *handler) buildReplyContext(ctx context.Context, msg courier.Msg) (inReplyTo string, references []string, subject string) {
	subject = subjectFromMsg(msg)
	inReplyTo = h.resolveParentExternalID(ctx, msg)
	if inReplyTo == "" {
		return "", nil, subject
	}

	references = make([]string, 0, 2)
	reusedParentSubject := false
	if parent, err := h.Backend().LookupMsgByExternalID(ctx, msg.Channel(), inReplyTo); err == nil && parent != nil {
		if meta := parseEmailThreadMetadata(parent.Metadata()); meta != nil {
			references = append(references, meta.References...)
			if meta.Subject != "" {
				subject = meta.Subject
				reusedParentSubject = true
			}
		}
	}
	references = append(references, inReplyTo)
	references = normalizeReferences(references)

	if reusedParentSubject && !rePrefixPattern.MatchString(subject) {
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
	log := logrus.WithFields(logrus.Fields{
		"channel_uuid": msg.Channel().UUID().String(),
		"msg_id":       msg.ID(),
		"urn":          msg.URN().Identity(),
	})

	if id := normalizeMessageID(msg.ResponseToExternalID()); id != "" {
		log.WithField("in_reply_to", id).WithField("source", "response_to_external_id").
			Info("resolved parent Message-ID")
		return id
	}

	if msg.ResponseToID() != courier.NilMsgID {
		parent, err := h.Backend().LookupMsgByID(ctx, msg.ResponseToID())
		if err != nil {
			log.WithError(err).WithField("response_to_id", msg.ResponseToID()).
				Warn("LookupMsgByID failed while resolving parent")
		} else if parent != nil {
			if id := normalizeMessageID(parent.ExternalID()); id != "" {
				log.WithField("in_reply_to", id).WithField("source", "response_to_id").
					WithField("response_to_id", msg.ResponseToID()).
					Info("resolved parent Message-ID")
				return id
			}
			log.WithField("response_to_id", msg.ResponseToID()).
				Warn("parent found by ResponseToID but has empty external_id")
		}
	}

	parent, err := h.Backend().LookupLastMsgWithExternalID(ctx, msg.Channel(), msg.URN())
	if err != nil {
		log.WithError(err).Warn("LookupLastMsgWithExternalID failed while resolving parent")
	} else if parent != nil {
		if id := normalizeMessageID(parent.ExternalID()); id != "" {
			log.WithField("in_reply_to", id).WithField("source", "last_msg_for_contact").
				Info("resolved parent Message-ID")
			return id
		}
		log.Warn("last message for contact has empty external_id")
	}

	log.Info("no parent Message-ID resolved for outbound reply")
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

// buildOutboundThreadMetadata returns the email-specific metadata blob to stash
// on an outbound message after a successful send, mirroring what
// buildThreadMetadata writes for inbound. Subject is always stored when present
// so a subsequent outbound that resolves this message as parent can reuse it
// even when this send was not itself a reply.
func buildOutboundThreadMetadata(inReplyTo string, references []string, subject string) json.RawMessage {
	inReplyTo = normalizeMessageID(inReplyTo)
	references = normalizeReferences(references)
	subject = strings.TrimSpace(subject)

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

// mergeEmailMetadata merges keys from emailBlock into existing message
// metadata, preserving unrelated keys (ticketer_id, chats_msg_uuid, etc.).
// emailBlock is expected to be a well-formed JSON object (as produced by
// buildOutboundThreadMetadata). Returns nil when emailBlock is empty.
func mergeEmailMetadata(existing, emailBlock json.RawMessage) json.RawMessage {
	if len(emailBlock) == 0 {
		return nil
	}
	var src map[string]json.RawMessage
	if err := json.Unmarshal(emailBlock, &src); err != nil || len(src) == 0 {
		return nil
	}
	dst := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &dst); err != nil {
			return emailBlock
		}
	}
	for k, v := range src {
		dst[k] = v
	}
	out, err := json.Marshal(dst)
	if err != nil {
		return emailBlock
	}
	return out
}
