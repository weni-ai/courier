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

	// Block is mailbox-scoped: if any contact for this real address (or a
	// +wt- thread variant) is blocked, reject before creating a new virtual
	// contact for a fresh subject/thread.

	blocked, err := h.Backend().IsEmailMailboxBlocked(ctx, channel, payload.From)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r,
			fmt.Errorf("checking blocked mailbox: %w", err))
	}
	if blocked {
		logrus.WithField("channel_uuid", channel.UUID().String()).WithField("from", payload.From).
			Info("ignored inbound email from blocked mailbox")
		return nil, errors.New("blocked contact sending message")
	}

	urn, err := h.contactURNForInbound(ctx, channel, payload)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	previous, err := h.copyListForURN(ctx, channel, urn)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r,
			fmt.Errorf("reading email cc list: %w", err))
	}
	cc, dropped := accumulateCopyList(previous, payload, urn, channelMailboxes(channel, payload.To))
	if dropped > 0 {
		logrus.WithFields(logrus.Fields{
			"channel_uuid": channel.UUID().String(),
			"cc_dropped":   dropped,
		}).Info("dropped email cc entries")
	}

	name := payload.From
	if !sameEmailAddress(payload.From, realAddressFromURNPath(urn.Path())) {
		name = realAddressFromURNPath(urn.Path())
	}
	msg := h.Backend().NewIncomingMsg(channel, urn, inboundText(payload.From, urn, payload.Body)).WithContactName(name)

	for _, attachment := range payload.Attachments {
		msg.WithAttachment(attachment)
	}

	if messageID := normalizeMessageID(payload.MessageID); messageID != "" && len(messageID) <= maxExternalIDLength {
		msg.WithExternalID(messageID)
	}

	if metadata := buildThreadMetadata(payload, cc); metadata != nil {
		msg.WithMetadata(metadata)
	}

	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

// buildThreadMetadata returns the email-specific metadata blob to stash on
// the incoming message, or nil when the payload carries no email context.
// Subject is always stored when present so outbound replies can reuse it even
// when the inbound message is not itself a reply.
func buildThreadMetadata(payload *moPayload, cc []string) json.RawMessage {
	inReplyTo := normalizeMessageID(payload.InReplyTo)
	references := normalizeReferences(payload.References)
	subject := strings.TrimSpace(payload.Subject)
	if len(cc) == 0 {
		cc = nil
	}

	if inReplyTo == "" && len(references) == 0 && subject == "" && len(cc) == 0 {
		return nil
	}

	body, err := json.Marshal(map[string]emailThreadMetadata{
		"email": {
			InReplyTo:  inReplyTo,
			References: references,
			Subject:    subject,
			CC:         cc,
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

// contactURNForInbound picks the contact URN for an inbound email.
// A reply that cites a Message-ID we already stored on a legacy contact
// (mailto address with no +wt- tag) stays on that contact. When In-Reply-To
// itself belongs to another contact, older References are left alone and the
// URN comes from buildContactURN.
func (h *handler) contactURNForInbound(ctx context.Context, channel courier.Channel, payload *moPayload) (urns.URN, error) {
	direct := normalizeMessageID(payload.InReplyTo)
	for _, id := range replyParentIDs(payload) {
		parentURN, err := h.Backend().LookupContactURNByExternalID(ctx, channel, id)
		if err != nil {
			return urns.NilURN, fmt.Errorf("looking up contact for message %s: %w", id, err)
		}
		if parentURN == urns.NilURN {
			continue
		}
		// A +wt- URN is the central contact of that thread. A copy author
		// stays on it instead of receiving a URN of their own.
		if threadTagPattern.MatchString(parentURN.Path()) {
			return parentURN, nil
		}
		if sameEmailAddress(realAddressFromURNPath(parentURN.Path()), payload.From) {
			return parentURN, nil
		}
		// The message being answered belongs to another contact. Older
		// References must not pull the reply onto a legacy mailbox contact.
		if id == direct {
			break
		}
	}
	return buildContactURN(payload)
}

// replyParentIDs lists Message-IDs that may identify the message being
// answered: In-Reply-To first, then References from newest to oldest. The
// inbound message's own Message-ID is not included.
func replyParentIDs(payload *moPayload) []string {
	ids := make([]string, 0, 1+len(payload.References))
	seen := make(map[string]bool, 1+len(payload.References))
	add := func(raw string) {
		id := normalizeMessageID(raw)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	add(payload.InReplyTo)
	for i := len(payload.References) - 1; i >= 0; i-- {
		add(payload.References[i])
	}
	return ids
}

func sameEmailAddress(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// inboundText prefixes a copy author's address. The central contact's own
// mail, including a case-only difference, is stored unchanged.
func inboundText(from string, central urns.URN, body string) string {
	if sameEmailAddress(from, realAddressFromURNPath(central.Path())) {
		return body
	}
	return strings.TrimSpace(from) + ":\n\n" + body
}

// channelMailboxes is the channel username and, when set, the inbound To.
// Both are excluded from the copy list.
func channelMailboxes(channel courier.Channel, to string) []string {
	var out []string
	if username := strings.TrimSpace(channel.StringConfigForKey(courier.ConfigUsername, "")); username != "" {
		out = append(out, username)
	}
	if to = strings.TrimSpace(to); to != "" {
		out = append(out, to)
	}
	return out
}

// copyListForURN reads email.cc from the latest visible message of the URN.
// A missing row is an empty list. Any other error is returned.
func (h *handler) copyListForURN(ctx context.Context, channel courier.Channel, urn urns.URN) ([]string, error) {
	row, err := h.Backend().LookupLastMsgByContactURN(ctx, channel, urn)
	if err != nil {
		if errors.Is(err, courier.ErrMsgNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	meta := parseEmailThreadMetadata(row.Metadata())
	if meta == nil || len(meta.CC) == 0 {
		return nil, nil
	}
	return meta.CC, nil
}

// accumulateCopyList keeps prev, then appends new cc addresses, then from when
// from is not the central contact. dropped counts payload cc entries that were
// not appended. from is never counted.
func accumulateCopyList(prev []string, payload *moPayload, central urns.URN, mailboxes []string) ([]string, int) {
	centralReal := realAddressFromURNPath(central.Path())
	out := make([]string, 0, len(prev)+len(payload.CC)+1)
	seen := make(map[string]struct{}, len(prev)+len(payload.CC)+1)
	dropped := 0

	appendAddr := func(raw string, countDrop bool) {
		addr := normalizeMailbox(raw)
		if addr == "" || excludedMailbox(addr, centralReal, mailboxes) || alreadySeen(seen, addr) {
			if countDrop {
				dropped++
			}
			return
		}
		seen[strings.ToLower(addr)] = struct{}{}
		out = append(out, addr)
	}

	for _, addr := range prev {
		appendAddr(addr, false)
	}
	for _, addr := range payload.CC {
		appendAddr(addr, true)
	}
	if !sameEmailAddress(payload.From, centralReal) {
		appendAddr(payload.From, false)
	}
	if len(out) == 0 {
		return nil, dropped
	}
	return out, dropped
}

func normalizeMailbox(raw string) string {
	addr := realAddressFromURNPath(strings.TrimSpace(raw))
	if !validMailbox(addr) {
		return ""
	}
	return addr
}

func validMailbox(addr string) bool {
	if addr == "" || strings.ContainsAny(addr, " \t<>") {
		return false
	}
	at := strings.IndexByte(addr, '@')
	if at <= 0 || at != strings.LastIndexByte(addr, '@') || at == len(addr)-1 {
		return false
	}
	return true
}

func excludedMailbox(addr, central string, mailboxes []string) bool {
	if sameEmailAddress(addr, central) {
		return true
	}
	for _, mailbox := range mailboxes {
		if sameEmailAddress(addr, realAddressFromURNPath(strings.TrimSpace(mailbox))) {
			return true
		}
	}
	return false
}

func alreadySeen(seen map[string]struct{}, addr string) bool {
	_, ok := seen[strings.ToLower(addr)]
	return ok
}

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
	CC          []string            `json:"cc,omitempty"`
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
	CC         []string `json:"cc,omitempty"`
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

	cc, ccErr := h.copyListForURN(ctx, msg.Channel(), msg.URN())
	if ccErr != nil {
		logrus.WithFields(logrus.Fields{
			"channel_uuid": msg.Channel().UUID().String(),
			"msg_id":       msg.ID(),
		}).WithError(ccErr).Warn("email cc lookup failed")
		return status, nil
	}

	payload := moPayload{
		UUID:        msg.UUID(),
		From:        senderEmail,
		To:          realAddressFromURNPath(msg.URN().Path()),
		CC:          cc,
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
		if emailMeta := buildOutboundThreadMetadata(inReplyTo, references, subject, cc); emailMeta != nil {
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
func buildOutboundThreadMetadata(inReplyTo string, references []string, subject string, cc []string) json.RawMessage {
	inReplyTo = normalizeMessageID(inReplyTo)
	references = normalizeReferences(references)
	subject = strings.TrimSpace(subject)
	if len(cc) == 0 {
		cc = nil
	}

	if inReplyTo == "" && len(references) == 0 && subject == "" && len(cc) == 0 {
		return nil
	}

	body, err := json.Marshal(map[string]emailThreadMetadata{
		"email": {
			InReplyTo:  inReplyTo,
			References: references,
			Subject:    subject,
			CC:         cc,
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
