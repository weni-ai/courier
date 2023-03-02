package handlers

import (
	"fmt"
	"net/http"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
)

func SendWebhooks(channel courier.Channel, r *http.Request, configWebhook interface{}) error {
	webhook, ok := configWebhook.(map[string]interface{})
	if !ok {
		return fmt.Errorf("conversion error")
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
