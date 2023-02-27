package handlers

import (
	"fmt"
	"net/http"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
)

func SendWebhooks(channel courier.Channel, r *http.Request, webhook interface{}) error {
	webhookURL, ok := webhook.(map[string]interface{})
	if !ok {
		return fmt.Errorf("conversion error")
	}
	req, err := http.NewRequest(http.MethodPost, webhookURL["url"].(string), r.Body)
	if err != nil {
		return err
	}

	resp, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return err
	}

	if resp.StatusCode/100 != 2 {
		return err
	}

	return nil
}
