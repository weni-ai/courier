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

	method := webhook["method"].(string)
	if method == "" {
		method = "POST"
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
					WabaBanState []string `json:"waba_ban_state"`
					WabaBanDate  string   `json:"waba_ban_date"`
				} `json:"ban_info"`
				CurrentLimit                 string `json:"current_limit"`
				Decision                     string `json:"decision"`
				DisplayPhoneNumber           string `json:"display_phone_number"`
				Event                        string `json:"event"`
				MaxDailyConversationPerPhone int    `json:"max_daily_conversation_per_phone"`
				MaxPhoneNumbersPerBusiness   int    `json:"max_phone_numbers_per_business"`
				MaxPhoneNumbersPerWaba       int    `json:"max_phone_numbers_per_waba"`
				Reason                       string `json:"reason"`
				RequestedVerifiedName        string `json:"requested_verified_name"`
				RestrictionInfo              []struct {
					RestrictionType string `json:"restriction_type"`
					Expiration      string `json:"expiration"`
				} `json:"restriction_info"`
				MessageTemplateID       int    `json:"message_template_id"`
				MessageTemplateName     string `json:"message_template_name"`
				MessageTemplateLanguage string `json:"message_template_language"`
				Message                 string `json:"message"`
				FlowID                  string `json:"flow_id"`
				OldStatus               string `json:"old_status"`
				NewStatus               string `json:"new_status"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

func SendWebhooks(r *http.Request, url string, isIntegrations bool) error {
	moTemplatesPayload := &moTemplatesPayload{}

	if isIntegrations {
		url = url + "/api/v1/webhook/facebook/api/notification/"
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
	req, _ := http.NewRequest("POST", url, requestBody)
	req.Header.Set("Content-Type", "application/json")
	resp, err := utils.MakeHTTPRequest(req)

	if resp.StatusCode/100 != 2 {
		return err
	}

	return nil
}
