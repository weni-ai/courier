package weniwebchat

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	wwcPrivilegedAttempts     = 3
	wwcPrivilegedBaseBackoff  = 500 * time.Millisecond
	wwcMaxEventIDLen          = 128
	wwcMaxAgentFirstNameRunes = 100
	wwcMaxStatusMessageIDs    = 100
	wwcMaxMessageIDLen        = 64
	authorAI                  = "ai_agent"
	authorHuman               = "human_agent"
)

var (
	errInvalidHumanSupport = errors.New("invalid human support payload")
	errMissingIngressToken = errors.New("missing WWC human support ingress token")
	authHeaderPattern      = regexp.MustCompile(`(?i)(Authorization:\s*)Bearer\s+\S+`)
	agentNamePattern       = regexp.MustCompile(`"agent_first_name"\s*:\s*"(?:\\.|[^"\\])*"`)
)

type chatAuthor struct {
	author     string
	name       string
	privileged bool
}

type controlBody struct {
	Type         string   `json:"type"`
	To           string   `json:"to"`
	From         string   `json:"from"`
	ChannelUUID  string   `json:"channel_uuid"`
	EventID      string   `json:"event_id"`
	OccurredAtMs int64    `json:"occurred_at_ms,omitempty"`
	Open         *bool    `json:"open,omitempty"`
	Status       string   `json:"status,omitempty"`
	MessageIDs   []string `json:"message_ids,omitempty"`
}

type typingBody struct {
	ChannelUUID    string `json:"channel_uuid"`
	From           string `json:"from"`
	AgentFirstName string `json:"agent_first_name,omitempty"`
	To             string `json:"to"`
	Type           string `json:"type"`
}

func acceptInboundMessageID(id string) bool {
	if id == "" {
		return false
	}
	if utf8.RuneCountInString(id) > wwcMaxMessageIDLen {
		return false
	}
	for _, r := range id {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func noteRejectedMessageID(channel courier.Channel) {
	logrus.WithFields(logrus.Fields{
		"channel_uuid":        channel.UUID().String(),
		"message_id_rejected": true,
	}).Warn("wwc inbound message_id rejected")
}

func (h *handler) ingressToken() string {
	if h.Server() == nil || h.Server().Config() == nil {
		return ""
	}
	return h.Server().Config().WwcHumanSupportIngressToken
}

func isControlAction(action courier.MsgActionType) bool {
	switch action {
	case courier.MsgActionHumanSupportStarted, courier.MsgActionHumanSupportEnded, courier.MsgActionHumanSupportState, courier.MsgActionMessageStatus:
		return true
	default:
		return false
	}
}

func chatAuthorFromMetadata(meta []byte) (chatAuthor, error) {
	var out chatAuthor
	if len(meta) == 0 {
		return out, nil
	}
	author, err := jsonparser.GetString(meta, "author")
	switch {
	case err == nil:
		if author != authorAI && author != authorHuman {
			return out, errInvalidHumanSupport
		}
		out.author = author
	case errors.Is(err, jsonparser.KeyPathNotFoundError):
	default:
		return out, errInvalidHumanSupport
	}

	name, err := jsonparser.GetString(meta, "agent_first_name")
	switch {
	case err == nil:
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			if utf8.RuneCountInString(trimmed) > wwcMaxAgentFirstNameRunes {
				return out, errInvalidHumanSupport
			}
			for _, r := range trimmed {
				if unicode.IsSpace(r) {
					return out, errInvalidHumanSupport
				}
			}
			out.name = trimmed
		}
	case errors.Is(err, jsonparser.KeyPathNotFoundError):
	default:
		return out, errInvalidHumanSupport
	}

	if out.author == authorAI && out.name != "" {
		return out, errInvalidHumanSupport
	}
	out.privileged = out.author == authorHuman || out.name != ""
	return out, nil
}

func (h *handler) sendControlEvent(ctx context.Context, msg courier.Msg, status courier.MsgStatus, sendURL string) (bool, courier.MsgStatus, error) {
	action := msg.ActionType()
	if !isControlAction(action) {
		return false, nil, nil
	}

	eventID, ok := validEventID(msg.Metadata())
	if !ok {
		status.SetStatus(courier.MsgFailed)
		return true, status, nil
	}

	body := controlBody{
		Type:        string(action),
		To:          msg.URN().Path(),
		From:        msg.Channel().Address(),
		ChannelUUID: msg.Channel().UUID().String(),
		EventID:     eventID,
	}

	switch action {
	case courier.MsgActionHumanSupportStarted, courier.MsgActionHumanSupportEnded:
		occurred, ok := positiveMillis(msg.Metadata())
		if !ok {
			status.SetStatus(courier.MsgFailed)
			return true, status, nil
		}
		body.OccurredAtMs = occurred
	case courier.MsgActionHumanSupportState:
		if !openIsTrue(msg.Metadata()) {
			status.SetStatus(courier.MsgFailed)
			return true, status, nil
		}
		open := true
		body.Open = &open
	case courier.MsgActionMessageStatus:
		state, ids, ok := messageStatusFields(msg.Metadata())
		if !ok {
			status.SetStatus(courier.MsgFailed)
			return true, status, nil
		}
		body.Status = state
		body.MessageIDs = ids
	}

	raw, err := json.Marshal(body)
	if err != nil {
		status.SetStatus(courier.MsgFailed)
		return true, status, nil
	}

	rr, outcome := h.postPrivileged(ctx, sendURL, raw, eventID, action == courier.MsgActionMessageStatus, privilegedFields(msg, eventID))
	if rr != nil {
		status.AddLog(courier.NewChannelLogFromRR("Human Support Sent", msg.Channel(), msg.ID(), rr))
	}
	status.SetStatus(outcome)
	return true, status, nil
}

func validEventID(meta []byte) (string, bool) {
	id, err := jsonparser.GetString(meta, "event_id")
	if err != nil {
		return "", false
	}
	n := utf8.RuneCountInString(id)
	if n < 1 || n > wwcMaxEventIDLen {
		return "", false
	}
	return id, true
}

func positiveMillis(meta []byte) (int64, bool) {
	n, err := jsonparser.GetInt(meta, "occurred_at_ms")
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func openIsTrue(meta []byte) bool {
	open, err := jsonparser.GetBoolean(meta, "open")
	return err == nil && open
}

func messageStatusFields(meta []byte) (string, []string, bool) {
	state, err := jsonparser.GetString(meta, "status")
	if err != nil || (state != "delivered" && state != "read") {
		return "", nil, false
	}
	ids, present, bad := messageIDs(meta)
	if bad {
		return "", nil, false
	}
	if present && len(ids) == 0 {
		return "", nil, false
	}
	if len(ids) > wwcMaxStatusMessageIDs {
		return "", nil, false
	}
	if state == "delivered" && len(ids) == 0 {
		return "", nil, false
	}
	return state, ids, true
}

func messageIDs(meta []byte) (ids []string, present bool, bad bool) {
	value, dataType, _, err := jsonparser.Get(meta, "message_ids")
	if err != nil {
		return nil, false, false
	}
	if dataType != jsonparser.Array {
		return nil, true, true
	}
	present = true
	_, err = jsonparser.ArrayEach(value, func(v []byte, vt jsonparser.ValueType, _ int, _ error) {
		if vt != jsonparser.String {
			bad = true
			return
		}
		s, parseErr := jsonparser.ParseString(v)
		if parseErr != nil {
			bad = true
			return
		}
		ids = append(ids, s)
	})
	if err != nil {
		bad = true
	}
	return ids, present, bad
}

func (h *handler) postPrivileged(ctx context.Context, sendURL string, body []byte, idempotencyKey string, retryNotFound bool, fields logrus.Fields) (*utils.RequestResponse, courier.MsgStatusValue) {
	token := h.ingressToken()
	if token == "" {
		return nil, courier.MsgFailed
	}
	if ctx.Err() != nil {
		logPrivileged(fields, nil, courier.MsgErrored)
		return nil, courier.MsgErrored
	}

	var last *utils.RequestResponse
	for attempt := 0; attempt < wwcPrivilegedAttempts; attempt++ {
		if ctx.Err() != nil {
			logPrivileged(fields, last, courier.MsgErrored)
			return redactRR(last), courier.MsgErrored
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(body))
		if err != nil {
			logPrivileged(fields, last, courier.MsgErrored)
			return redactRR(last), courier.MsgErrored
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", idempotencyKey)

		rr, reqErr := utils.MakeHTTPRequest(req)
		last = rr
		outcome, retry := classifyPrivileged(rr, reqErr, retryNotFound)
		if ctx.Err() != nil {
			logPrivileged(fields, rr, courier.MsgErrored)
			return redactRR(rr), courier.MsgErrored
		}
		if !retry || attempt == wwcPrivilegedAttempts-1 {
			logPrivileged(fields, rr, outcome)
			return redactRR(rr), outcome
		}
		if !sleepPrivilegedBackoff(ctx, attempt) {
			logPrivileged(fields, rr, courier.MsgErrored)
			return redactRR(rr), courier.MsgErrored
		}
	}
	logPrivileged(fields, last, courier.MsgErrored)
	return redactRR(last), courier.MsgErrored
}

func classifyPrivileged(rr *utils.RequestResponse, err error, retryNotFound bool) (courier.MsgStatusValue, bool) {
	if rr == nil || rr.StatusCode == 0 {
		return courier.MsgErrored, true
	}
	if rr.StatusCode >= 200 && rr.StatusCode < 300 {
		return courier.MsgSent, false
	}
	switch rr.StatusCode {
	case http.StatusUnauthorized, http.StatusBadRequest, http.StatusConflict:
		return courier.MsgFailed, false
	case http.StatusForbidden:
		return courier.MsgErrored, false
	case http.StatusNotFound:
		if retryNotFound {
			return courier.MsgErrored, true
		}
		return courier.MsgErrored, false
	case http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return courier.MsgErrored, true
	default:
		if err != nil {
			return courier.MsgErrored, true
		}
		return courier.MsgErrored, false
	}
}

func sleepPrivilegedBackoff(ctx context.Context, attempt int) bool {
	backoff := wwcPrivilegedBaseBackoff << attempt
	half := int64(backoff / 2)
	wait := time.Duration(0)
	if half > 0 {
		wait = time.Duration(half + rand.Int63n(half))
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func redactRR(rr *utils.RequestResponse) *utils.RequestResponse {
	if rr == nil {
		return nil
	}
	rr.Request = redactPrivilegedRequest(rr.Request)
	return rr
}

func redactPrivilegedRequest(dump string) string {
	dump = authHeaderPattern.ReplaceAllString(dump, "${1}Bearer [redacted]")
	return agentNamePattern.ReplaceAllString(dump, `"agent_first_name":"[redacted]"`)
}

func privilegedFields(msg courier.Msg, eventID string) logrus.Fields {
	fields := logrus.Fields{
		"channel_uuid": msg.Channel().UUID().String(),
		"msg_uuid":     msg.UUID().String(),
		"action_type":  string(msg.ActionType()),
	}
	if eventID != "" {
		fields["event_id"] = eventID
	}
	return fields
}

func logPrivileged(fields logrus.Fields, rr *utils.RequestResponse, outcome courier.MsgStatusValue) {
	if fields == nil {
		fields = logrus.Fields{}
	}
	entry := logrus.WithFields(fields).WithField("msg_status", string(outcome))
	if rr != nil {
		entry = entry.WithField("http_status", rr.StatusCode)
		if code := socketErrorCode(rr.Body); code != "" {
			entry = entry.WithField("socket_error", code)
		}
	}
	entry.Info("wwc privileged send")
}

func socketErrorCode(body []byte) string {
	code, err := jsonparser.GetString(body, "error")
	if err != nil {
		return ""
	}
	return code
}

func (h *handler) sendTyping(ctx context.Context, msg courier.Msg) error {
	baseURL := msg.Channel().StringConfigForKey(courier.ConfigBaseURL, "")
	if baseURL == "" {
		return errors.New("blank base_url")
	}
	sendURL := baseURL + "/send"

	meta, err := chatAuthorFromMetadata(msg.Metadata())
	if err != nil {
		return err
	}

	kind := "typing_start"
	if msg.ActionType() == courier.MsgActionTypingStop {
		kind = "typing_stop"
	}
	payload := typingBody{
		Type:           kind,
		To:             msg.URN().Path(),
		From:           "ai-assistant",
		ChannelUUID:    msg.Channel().UUID().String(),
		AgentFirstName: meta.name,
	}
	if !meta.privileged {
		return postTypingOnce(payload, sendURL)
	}
	if h.ingressToken() == "" {
		return errMissingIngressToken
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, outcome := h.postPrivileged(ctx, sendURL, raw, msg.UUID().String(), false, privilegedFields(msg, ""))
	if outcome != courier.MsgSent {
		return errors.New("weni webchat typing failed")
	}
	return nil
}

func postTypingOnce(payload typingBody, sendURL string) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "HTTP request failed")
	}
	req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	res, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return errors.Wrap(err, "HTTP request failed")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.Errorf("weni webchat API error (%d): %s", res.StatusCode, string(res.Body))
	}
	return nil
}
