package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/nyaruka/courier/utils"
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
		method = methodVal.(string)
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
				Message                 string `json:"message,omitempty"`
				FlowID                  string `json:"flow_id,omitempty"`
				OldStatus               string `json:"old_status,omitempty"`
				NewStatus               string `json:"new_status,omitempty"`
			} `json:"value,omitempty"`
		} `json:"changes"`
	} `json:"entry"`
}

func SendWebhooks(r *http.Request, url_ string, tokenFlows string, isIntegrations bool) error {
	moTemplatesPayload := &moTemplatesPayload{}

	if isIntegrations {
		url_ = url_ + "/api/v1/webhook/facebook/api/notification/"
	} else {
		url_ = url_ + "/whatsapp_flows/"
		parsedURL, err := url.Parse(url_)
		if err != nil {
			return err
		}

		params := url.Values{}
		params.Add("token", tokenFlows)
		parsedURL.RawQuery = params.Encode()
		url_ = parsedURL.String()
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	defer r.Body.Close()

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
