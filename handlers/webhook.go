package handlers

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/pkg/errors"
)

func SendWebhooks(channel courier.Channel, r *http.Request, configWebhook interface{}, clog *courier.ChannelLog) error {
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

	trace, err := httpx.DoTrace(utils.GetHTTPClient(), req, nil, nil, 1024)
	if trace != nil {
		clog.HTTP(trace)
	}
	if err != nil || trace.Response.StatusCode/100 != 2 {
		return errors.Wrap(err, "status other than 200")
	}

	return nil
}
