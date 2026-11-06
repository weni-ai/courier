package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/nyaruka/courier/utils"
	"github.com/sirupsen/logrus"
)

func SendWebhooksExternal(r *http.Request, configWebhook interface{}) error {
	webhook, ok := configWebhook.(map[string]interface{})
	if !ok {
		return fmt.Errorf("conversion error")
	}

	// check if url is valid
	_, err := url.ParseRequestURI(webhook["url"].(string))
	if err != nil {
		return err
	}
	u, err := url.Parse(webhook["url"].(string))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid url %s", err)
	}

	method := "POST"
	methodVal, ok := webhook["method"]
	if ok && methodVal != nil {
		switch v := methodVal.(type) {
		case string:
			method = v
		default:
			fmt.Println(fmt.Errorf("invalid method %T, usind default %s", v, method))
		}
	}

	req, _ := http.NewRequest(method, webhook["url"].(string), r.Body)

	headers, ok := webhook["headers"].(map[string]interface{})
	if ok {
		for name, value := range headers {
			req.Header.Set(name, value.(string))
		}
	}

	resp, err := utils.MakeHTTPRequest(req)

	if resp.StatusCode/100 != 2 {
		return err
	}

	return nil
}

type moTemplatesPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Time    int64  `json:"time"`
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				BanInfo struct {
					WabaBanState []string `json:"waba_ban_state,omitempty"`
					WabaBanDate  string   `json:"waba_ban_date,omitempty"`
				} `json:"ban_info,omitempty"`
				CurrentLimit                 string `json:"current_limit,omitempty"`
				Decision                     string `json:"decision,omitempty"`
				DisplayPhoneNumber           string `json:"display_phone_number,omitempty"`
				Event                        string `json:"event,omitempty"`
				MaxDailyConversationPerPhone int    `json:"max_daily_conversation_per_phone,omitempty"`
				MaxPhoneNumbersPerBusiness   int    `json:"max_phone_numbers_per_business,omitempty"`
				MaxPhoneNumbersPerWaba       int    `json:"max_phone_numbers_per_waba,omitempty"`
				Reason                       string `json:"reason,omitempty"`
				RequestedVerifiedName        string `json:"requested_verified_name,omitempty"`
				RestrictionInfo              []struct {
					RestrictionType string `json:"restriction_type,omitempty"`
					Expiration      string `json:"expiration,omitempty"`
				} `json:"restriction_info,omitempty"`
				MessageTemplateID       int    `json:"message_template_id,omitempty"`
				MessageTemplateName     string `json:"message_template_name,omitempty"`
				MessageTemplateLanguage string `json:"message_template_language,omitempty"`
				Category                string `json:"category,omitempty"`
				CorrectCategory         string `json:"correct_category,omitempty"`
				Message                 string `json:"message,omitempty"`
				FlowID                  string `json:"flow_id,omitempty"`
				OldStatus               string `json:"old_status,omitempty"`
				NewStatus               string `json:"new_status,omitempty"`
				WabaInfo                struct {
					WabaID        string `json:"waba_id,omitempty"`
					AdAccountID   string `json:"ad_account_id,omitempty"`
					AdAccountName string `json:"ad_account_name,omitempty"`
				} `json:"waba_info,omitempty"`
			} `json:"value,omitempty"`
		} `json:"changes"`
	} `json:"entry"`
}

func (p *moTemplatesPayload) wabaIDs() []string {
	ids := make([]string, 0, len(p.Entry))
	for _, entry := range p.Entry {
		ids = append(ids, entry.ID)
	}
	return ids
}

const (
	integrationsWebhookPath        = "/api/v1/webhook/facebook/api/notification/"
	integrationsForwardAttempts    = 3
	integrationsForwardBaseBackoff = 500 * time.Millisecond
	integrationsForwardBodyLimit   = 1000000

	integrationsForwardFailedEvent = "integrations_webhook_forward_failed"

	integrationsForwardReasonTokenNotConfigured = "token_not_configured"
	integrationsForwardReasonUnauthorized       = "unauthorized"
	integrationsForwardReasonRejected           = "rejected"
	integrationsForwardReasonTransientExhausted = "transient_exhausted"
)

var integrationsForwardTimeout = 10 * time.Second

var errIntegrationsTokenNotConfigured = errors.New("integrations webhook token not configured")

type integrationsForwardError struct {
	reason     string
	statusCode int
	attempts   int
}

func (e *integrationsForwardError) Error() string {
	return fmt.Sprintf("integrations webhook forward failed: reason=%s status_code=%d attempts=%d", e.reason, e.statusCode, e.attempts)
}

// ForwardToIntegrations sends a WhatsApp Cloud webhook to integrations, authenticated with token.
// The caller must have verified the Meta signature of r. An empty token fails closed without
// any request. The response trace is never logged because it carries the Authorization header.
func ForwardToIntegrations(ctx context.Context, r *http.Request, baseURL, token string) error {
	body, err := ReadBody(r, integrationsForwardBodyLimit)
	if err != nil {
		return err
	}

	payload := &moTemplatesPayload{}
	if err := json.Unmarshal(body, payload); err != nil {
		return fmt.Errorf("unable to parse request JSON: %s", err)
	}

	forwardBody := &bytes.Buffer{}
	if err := json.NewEncoder(forwardBody).Encode(payload); err != nil {
		return err
	}
	bodyHash := sha256.Sum256(forwardBody.Bytes())
	idempotencyKey := hex.EncodeToString(bodyHash[:])

	failureLog := logrus.WithFields(logrus.Fields{
		"event":           integrationsForwardFailedEvent,
		"idempotency_key": idempotencyKey,
		"waba_ids":        payload.wabaIDs(),
	})

	if token == "" {
		failureLog.WithFields(logrus.Fields{
			"reason":      integrationsForwardReasonTokenNotConfigured,
			"status_code": 0,
			"attempts":    0,
		}).Error("integrations webhook forward not sent")
		return errIntegrationsTokenNotConfigured
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+integrationsWebhookPath, forwardBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	forwardCtx, cancel := context.WithTimeout(ctx, integrationsForwardTimeout)
	defer cancel()

	rr, attempts, err := utils.MakeHTTPRequestWithRetryAttempts(forwardCtx, req, integrationsForwardAttempts, integrationsForwardBaseBackoff, idempotencyKey)
	if err == nil {
		return nil
	}

	forwardErr := &integrationsForwardError{
		reason:   integrationsForwardFailureReason(rr, err),
		attempts: attempts,
	}
	if rr != nil {
		forwardErr.statusCode = rr.StatusCode
	}
	failureLog.WithError(err).WithFields(logrus.Fields{
		"reason":      forwardErr.reason,
		"status_code": forwardErr.statusCode,
		"attempts":    forwardErr.attempts,
	}).Error("integrations webhook forward failed")
	return forwardErr
}

func integrationsForwardFailureReason(rr *utils.RequestResponse, err error) string {
	if rr != nil && rr.StatusCode == http.StatusUnauthorized {
		return integrationsForwardReasonUnauthorized
	}
	if utils.IsTransientFailure(rr, err) {
		return integrationsForwardReasonTransientExhausted
	}
	return integrationsForwardReasonRejected
}

func SendWebhooks(r *http.Request, url_ string, tokenFlows string) error {
	moTemplatesPayload := &moTemplatesPayload{}

	url_ = url_ + "/whatsapp_flows/"
	parsedURL, err := url.Parse(url_)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Add("token", tokenFlows)
	parsedURL.RawQuery = params.Encode()
	url_ = parsedURL.String()

	body, err := ReadBody(r, 1000000)
	if err != nil {
		return err
	}

	// try to decode our envelope
	if err := json.Unmarshal(body, moTemplatesPayload); err != nil {
		return fmt.Errorf("unable to parse request JSON: %s", err)
	}

	requestBody := &bytes.Buffer{}
	json.NewEncoder(requestBody).Encode(moTemplatesPayload)
	req, _ := http.NewRequest("POST", url_, requestBody)
	req.Header.Set("Content-Type", "application/json")
	resp, err := utils.MakeHTTPRequest(req)

	if resp.StatusCode/100 != 2 {
		return err
	}

	return nil
}
