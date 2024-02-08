package handlers

import (
	"fmt"
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

	headers := webhook["headers"].(map[string]interface{})
	for name, value := range headers {
		req.Header.Set(name, value.(string))
	}

	resp, err := utils.MakeHTTPRequest(req)

	if resp.StatusCode/100 != 2 {
		return err
	}

	return nil
}

func SendWebhooksInternal(r *http.Request, url string) error {
	req, _ := http.NewRequest("POST", url, r.Body)

	resp, err := utils.MakeHTTPRequest(req)

	if resp.StatusCode/100 != 2 {
		return err
	}

	return nil
}
