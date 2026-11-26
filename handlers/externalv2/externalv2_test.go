package externalv2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/urns"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	receiveAttachment = "/c/e2/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive/"
	receiveNoParams   = "/c/e2/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive/"
)

var (
	configReceiveTemplateTest       = `{"messages":[{"urn_path":"{{.from}}","urn_auth":"{{.session_id}}","text":"{{.text}}"{{if .date}},"date":"{{.date}}"{{end}}{{if .media}},"attachments":["{{.media}}"]{{end}}}]}`
	configReceiveTemplateTest2      = `{"messages":[{"urn_path":"{{.message.from.id}}","text":"{{.message.text}}"{{if .date}},"date":"{{.date}}"{{end}},"contact_name":"{{.message.from.username}}","id":"{{.message.message_id}}"}]}`
	configMOResponseContentTypeTest = "application/json"
	configMOResponseTest            = `{"status":"received"}`
)

var testChannels = []courier.Channel{
	courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "E2", "2020", "US", map[string]interface{}{
		configReceiveTemplate:       configReceiveTemplateTest,
		configMOResponseContentType: configMOResponseContentTypeTest,
		configMOResponse:            configMOResponseTest,
	}),
}

var testChannels2 = []courier.Channel{
	courier.NewMockChannel("e7152f4f-7189-4458-a91a-747d5404b50a", "E2", "2020", "US", map[string]interface{}{
		configReceiveTemplate:       configReceiveTemplateTest2,
		configMOResponseContentType: configMOResponseContentTypeTest,
		configMOResponse:            `{"status":"Accepted"}`,
	}).WithSchemes([]string{"telegram"}),
}

var helloMsg = `{
  "update_id": 174114370,
  "message": {
	"message_id": 41,
	"from": {
		"id": 3527065,
		"first_name": "John",
		"last_name": "Doe",
		"username": "johndoe"
	},
	"chat": {
		"id": 3527065,
		"first_name": "John",
		"last_name": "Doe",
		"type": "private"
	},
	"date": 1454119029,
	"text": "Hello World"
  }
}`

var handleTestCases = []ChannelHandleTestCase{
	{
		Label: "Receive Valid Message With Attachment", URL: receiveAttachment,
		Data:   `{"from":"+2349067554729","text":"Join","media":"https://example.com/image.jpg"}`,
		Status: 200, Response: `{"status":"received"}`,
		Text: Sp("Join"), URN: Sp("tel:+2349067554729"), Attachments: []string{"https://example.com/image.jpg"},
	},
	{
		Label: "Receive Valid Message With Contact Session", URL: receiveNoParams,
		Data:   `{"from":"+2349067554729", "session_id":"1234567890","text":"Join","date":"2017-06-23T12:30:00.500Z"}`,
		Status: 200, Response: `{"status":"received"}`,
		URNAuth: Sp("1234567890"), URN: Sp("tel:+2349067554729"),
		Text: Sp("Join"), Date: Tp(time.Date(2017, 6, 23, 12, 30, 0, int(500*time.Millisecond), time.UTC)),
	},
}

var handleTestCases2 = []ChannelHandleTestCase{
	{Label: "Receive Valid Message", URL: "/c/e2/e7152f4f-7189-4458-a91a-747d5404b50a/receive/",
		Data: helloMsg, Status: 200, Response: "Accepted",
		Name: Sp("johndoe"), Text: Sp("Hello World"), URN: Sp("telegram:3527065"),
		ExternalID: Sp("41"),
	},
}

var templateTestCases = []ChannelHandleTestCase{
	{Label: "Receive Valid Template", URL: receiveNoParams, Data: `{"from":"+2349067554729","text":"Join","date":"2017-06-23T12:30:00.500Z"}`, Status: 200, Response: `{"status":"received"}`,
		Text: Sp("Join"), URN: Sp("tel:+2349067554729"), Date: Tp(time.Date(2017, 6, 23, 12, 30, 0, int(500*time.Millisecond), time.UTC))},
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), handleTestCases)
}

func TestHandler2(t *testing.T) {
	RunChannelTestCases(t, testChannels2, newHandler(), handleTestCases2)
}

func TestTemplateHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), templateTestCases)
}

func TestHandleWithUrnAuth(t *testing.T) {
	var channel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "E2", "2020", "US",
		map[string]interface{}{
			configReceiveTemplate:       `{"messages":[{"urn_path":"{{.from}}","urn_auth":"{{.session_id}}","text":"{{.text}}"}]}`,
			configMOResponseContentType: configMOResponseContentTypeTest,
			configMOResponse:            configMOResponseTest,
		}).WithSchemes([]string{"whatsapp"})

	testCases := []ChannelHandleTestCase{
		{Label: "Receive Valid Message With URN Auth", URL: receiveNoParams,
			Data:   `{"from":"2349067554729", "session_id":"1234567890","text":"Join"}`,
			Status: 200, Response: `{"status":"received"}`,
			URNAuth: Sp("1234567890"), URN: Sp("whatsapp:2349067554729"),
			Text: Sp("Join"),
		},
	}

	RunChannelTestCases(t, []courier.Channel{channel}, newHandler(), testCases)
}

func TestHandleWithUrnAuth2(t *testing.T) {
	var channel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "E2", "2020", "US",
		map[string]interface{}{
			configReceiveTemplate:       `{"messages":[{"urn_path":"{{.urn_path}}","text":"{{.text}}","urn_auth":"{{.urn_auth}}"}]}`,
			configMOResponseContentType: configMOResponseContentTypeTest,
			configMOResponse:            configMOResponseTest,
		}).WithSchemes([]string{"whatsapp"})

	testCases := []ChannelHandleTestCase{
		{Label: "Receive Valid Message With URN Auth", URL: receiveNoParams,
			Data:   `{"urn_path":"2349067554729", "urn_auth":"1234567890","text":"Join"}`,
			Status: 200, Response: `{"status":"received"}`,
			URNAuth: Sp("1234567890"), URN: Sp("whatsapp:2349067554729"),
			Text: Sp("Join"),
		},
	}

	RunChannelTestCases(t, []courier.Channel{channel}, newHandler(), testCases)
}

var sendTestCases = []ChannelSendTestCase{
	{Label: "Plain Send",
		Text: "Simple Message", URN: "tel:+250788383383",
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_001"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"Simple Message"}`,
		SendPrep:    setSendURL},
	{Label: "Plain Send with URN auth",
		Text: "Simple Message", URN: "tel:+250788383383", URNAuth: "1234567890",
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_001"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"Simple Message","session_id":"1234567890"}`,
		SendPrep:    setSendURL},
	{Label: "Unicode Send",
		Text: "☺", URN: "tel:+250788383383",
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_002"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"☺"}`,
		SendPrep:    setSendURL},
	{Label: "Error Sending",
		Text: "Error Message", URN: "tel:+250788383383",
		Status:       "E",
		ExternalID:   "",
		ResponseBody: `{"status":"error","message":"failed"}`, ResponseStatus: 401,
		SendPrep: setSendURL},
	{Label: "Send Attachment",
		Text: "My pic!", URN: "tel:+250788383383", Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_003"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"My pic!","media":[image/jpeg:https://foo.bar/image.jpg]}`,
		SendPrep:    setSendURL},
}

func TestSending(t *testing.T) {
	var defaultChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendURL:     "http://example.com/send",
			courier.ConfigSendMethod:  "POST",
			configSendTemplate:        `{"to":"{{.urn.path}}","text":"{{.text}}"{{if .attachments}},"media":{{.attachments}}{{end}}{{if .urn_auth}},"session_id":"{{.urn_auth}}"{{end}}}`,
			courier.ConfigContentType: "json",
			configMTResponseCheck:     "",
		})

	RunChannelSendTestCases(t, defaultChannel, newHandler(), sendTestCases, nil)
}

// TestCustomSending tests custom template sending
// Note: Custom templates with urlencoded content type are complex to test
// as they depend on specific server configurations

func TestSendingWithAuth(t *testing.T) {
	var authChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendURL:           "http://example.com/send",
			courier.ConfigSendMethod:        "POST",
			courier.ConfigSendAuthorization: "Bearer secret123",
			configSendTemplate:              `{"to":"{{.urn.path}}","text":"{{.text}}"}`,
			courier.ConfigContentType:       "json",
		})

	authTestCases := []ChannelSendTestCase{
		{Label: "Send with Auth",
			Text: "Auth Message", URN: "tel:+250788383383",
			Status:       "W",
			ExternalID:   "",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"to":"+250788383383","text":"Auth Message"}`,
			Headers:     map[string]string{"Authorization": "Bearer secret123"},
			SendPrep:    setSendURL},
	}

	RunChannelSendTestCases(t, authChannel, newHandler(), authTestCases, nil)
}

func TestSendingInParts(t *testing.T) {
	var partsChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendURL:       "http://example.com/send",
			courier.ConfigSendMethod:    "POST",
			configSendTemplate:          `{"to":"{{.urn.path}}","text":"{{.text}}",{{range .attachments}}"media":"{{attURL .}}"{{end}}}`,
			courier.ConfigContentType:   "json",
			configSendAttachmentInParts: true,
			configSendMediaURL:          "http://example.com/media",
		})

	partsTestCases := []ChannelSendTestCase{
		{Label: "Send Text and Media in Parts",
			Text: "Check this out", URN: "tel:+250788383383", Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
			Status:       "W",
			ExternalID:   "",
			RequestBody:  `{"to":"+250788383383","text":"Check this out","media":"https://foo.bar/image.jpg"}`,
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			SendPrep: setSendURL},
	}

	RunChannelSendTestCases(t, partsChannel, newHandler(), partsTestCases, nil)
}

func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	c.(*courier.MockChannel).SetConfig(courier.ConfigSendURL, s.URL)
}

func TestSimpleSend(t *testing.T) {
	var simpleSendChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendMethod:  "POST",
			courier.ConfigContentType: "json",
			configSendTemplate:        `{"contact": "{{.urn.path}}", "text": "{{.text}}", "urn_auth": "{{.urn_auth}}"}`,
		})

	simpleSendTestCases := []ChannelSendTestCase{
		{Label: "Simple Send",
			Text: "Simple Message", URN: "tel:+250788383383",
			Status:       "W",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"contact": "+250788383383", "text": "Simple Message", "urn_auth": "1234567890"}`,
			SendPrep:    setSendURL,
			URNAuth:     "1234567890",
		},
	}

	RunChannelSendTestCases(t, simpleSendChannel, newHandler(), simpleSendTestCases, nil)
}

func TestSendingWithCustomURL(t *testing.T) {
	var customURLChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendMethod:  "POST",
			courier.ConfigContentType: "json",
			configSendTemplate:        `{"to":"{{.urn.path}}","text":"{{.text}}","urn_auth":"{{.urn_auth}}"}`,
		})

	customURLTestCases := []ChannelSendTestCase{
		{Label: "Send with Custom URL with Query param",
			Text: "Simple Message", URN: "tel:+250788383383",
			URNAuth:      "DUMMY_URN_AUTH_VALUE",
			Status:       "W",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"to":"+250788383383","text":"Simple Message","urn_auth":"DUMMY_URN_AUTH_VALUE"}`,
			SendPrep: func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
				c.(*courier.MockChannel).SetConfig(configSendUrlTemplate, s.URL+"?custom_param={{.urn_auth}}")
			},
			URLParams: map[string]string{"custom_param": "DUMMY_URN_AUTH_VALUE"},
		},
		{Label: "Send with Custom URL",
			Text: "Simple Message", URN: "tel:+250788383383",
			URNAuth:      "DUMMY_URN_AUTH_VALUE",
			Status:       "W",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"to":"+250788383383","text":"Simple Message","urn_auth":"DUMMY_URN_AUTH_VALUE"}`,
			SendPrep:    setSendCustomURL,
			Path:        "/DUMMY_URN_AUTH_VALUE",
		},
	}

	RunChannelSendTestCases(t, customURLChannel, newHandler(), customURLTestCases, nil)
}

func setSendCustomURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	c.(*courier.MockChannel).SetConfig(configSendUrlTemplate, s.URL+"/{{.urn_auth}}")
}

// ---------------------------------------------------------------------------
// OAuth2 token refresh on the send path
// ---------------------------------------------------------------------------

const (
	partnerPath = "/send"
	mediaPath   = "/media"
	tokenPath   = "/token"

	// a unix expiry in the past (2001), so the stored token counts as expired
	pastExpiry = "1000000000"

	testURNPath = "+250788383383"
)

var (
	tokenReq   = MockedRequest{Method: "POST", Path: tokenPath, BodyContains: "grant_type"}
	partnerReq = MockedRequest{Method: "POST", Path: partnerPath, BodyContains: `"to"`}
	tokenOK    = MockedResponse{Status: 200, Body: `{"access_token":"` + fixtureAccessToken2 + `"}`}
	partnerOK  = MockedResponse{Status: 200, Body: `{"status":"success"}`}
)

func futureExpiry() string {
	return strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// newSendRefreshChannel returns a channel with the feature on and an expired token. Tests that refresh use their own
// channel: the mock backend replaces the channel config on save, so a shared channel would leak into the next case.
func newSendRefreshChannel(extra map[string]interface{}) *courier.MockChannel {
	config := map[string]interface{}{
		courier.ConfigSendMethod:        "POST",
		courier.ConfigContentType:       "json",
		configSendTemplate:              `{"to":"{{.urn.path}}","text":"{{.text}}"}`,
		courier.ConfigSendAuthorization: "Bearer " + fixtureAccessToken1,
		configTokenRefreshEnabled:       "true",
		configTokenRefreshType:          tokenRefreshTypeCustom,
		configExpiresIn:                 pastExpiry,
	}
	for k, v := range extra {
		config[k] = v
	}
	return courier.NewMockChannel(testChannelUUID, "E2", "2020", "US", config)
}

func tokenRefreshJSON(tokenURL, tokenType string, expired bool, statusCodes []int, bodyContains []string) string {
	cfg := map[string]interface{}{
		"when": map[string]interface{}{
			"match":         "any",
			"status_codes":  statusCodes,
			"body_contains": bodyContains,
			"expired":       expired,
		},
		"url":                 tokenURL,
		"token_field":         "access_token",
		"refresh_token_field": "refresh_token",
		"expires_in_field":    "expires_in",
	}
	if tokenType == tokenRefreshTypeCustom {
		cfg["body"] = "grant_type=password&client_secret=" + fixtureClientSecret
	}
	return mustJSON(cfg)
}

// prepRefresh points the channel at the test server for both the partner and the token endpoint
func prepRefresh(tokenType string, expired bool, statusCodes []int) SendPrepFunc {
	return func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
		ch := c.(*courier.MockChannel)
		ch.SetConfig(courier.ConfigSendURL, s.URL+partnerPath)
		ch.SetConfig(configSendMediaURL, s.URL+mediaPath)
		ch.SetConfig(configTokenRefreshType, tokenType)
		ch.SetConfig(configTokenRefreshConfig, tokenRefreshJSON(s.URL+tokenPath, tokenType, expired, statusCodes, nil))
	}
}

func TestSendingTokenRefreshDisabled(t *testing.T) {
	// a token URL on a closed port: had the handler called it, the send would end in E
	closedTokenURL := "http://127.0.0.1:1" + tokenPath

	variants := map[string]string{"unset": "", "false": "false", "no": "no"}
	for label, value := range variants {
		t.Run(label, func(t *testing.T) {
			channel := newSendRefreshChannel(map[string]interface{}{courier.ConfigSendAuthorization: "Basic dGVzdDp0ZXN0"})
			if value == "" {
				delete(channel.Config(), configTokenRefreshEnabled)
			} else {
				channel.SetConfig(configTokenRefreshEnabled, value)
			}

			RunChannelSendTestCases(t, channel, newHandler(), []ChannelSendTestCase{{
				Label: "refresh disabled", Text: "Hello", URN: "tel:" + testURNPath,
				Status:       "W",
				ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
				Headers: map[string]string{"Authorization": "Basic dGVzdDp0ZXN0"},
				Path:    partnerPath,
				SendPrep: func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
					ch := c.(*courier.MockChannel)
					ch.SetConfig(courier.ConfigSendURL, s.URL+partnerPath)
					ch.SetConfig(configTokenRefreshConfig, tokenRefreshJSON(closedTokenURL, tokenRefreshTypeCustom, true, []int{200}, nil))
				},
			}}, nil)
		})
	}
}

func TestSendingTokenRefreshExpired(t *testing.T) {
	for _, tokenType := range []string{tokenRefreshTypeCustom, tokenRefreshTypeRefresh} {
		t.Run(tokenType, func(t *testing.T) {
			channel := newSendRefreshChannel(map[string]interface{}{configRefreshToken: fixtureRefreshToken})

			RunChannelSendTestCases(t, channel, newHandler(), []ChannelSendTestCase{{
				Label: "expired token is refreshed before the partner call", Text: "Hello", URN: "tel:" + testURNPath,
				Status:    "W",
				Responses: map[MockedRequest]MockedResponse{tokenReq: tokenOK, partnerReq: partnerOK},
				Headers:   map[string]string{"Authorization": "Bearer " + fixtureAccessToken2},
				Path:      partnerPath,
				SendPrep:  prepRefresh(tokenType, true, nil),
			}}, nil)
		})
	}
}

func TestSendingTokenRefreshTokenFailure(t *testing.T) {
	failures := map[string]MockedResponse{
		"token 400":     {Status: 400, Body: `{"error":"invalid_grant"}`},
		"token 500":     {Status: 500, Body: `boom`},
		"no token":      {Status: 200, Body: `{"other":"x"}`},
		"invalid json":  {Status: 200, Body: `not json`},
		"empty token":   {Status: 200, Body: `{"access_token":""}`},
		"non scalar":    {Status: 200, Body: `{"access_token":["x"]}`},
		"error w/ body": {Status: 401, Body: `{"access_token":"` + fixtureAccessToken2 + `"}`},
	}

	for label, response := range failures {
		t.Run(label, func(t *testing.T) {
			channel := newSendRefreshChannel(nil)

			RunChannelSendTestCases(t, channel, newHandler(), []ChannelSendTestCase{{
				Label: "token failure never reaches the partner", Text: "Hello", URN: "tel:" + testURNPath,
				Status:    "E",
				Responses: map[MockedRequest]MockedResponse{tokenReq: response},
				// the partner was not called: the token request is the last one the server saw
				Path:     tokenPath,
				SendPrep: prepRefresh(tokenRefreshTypeCustom, true, nil),
			}}, nil)
		})
	}
}

func TestSendingTokenRefreshInvalidConfig(t *testing.T) {
	valid := tokenRefreshJSON("https://auth.example.invalid/token", tokenRefreshTypeCustom, true, nil, nil)

	tcs := []struct {
		label string
		set   map[string]interface{}
		err   string
	}{
		{"missing type", map[string]interface{}{configTokenRefreshType: ""}, "token_refresh_type must be custom or refresh"},
		{"unknown type", map[string]interface{}{configTokenRefreshType: "oauth"}, "token_refresh_type must be custom or refresh"},
		{"empty config", map[string]interface{}{configTokenRefreshConfig: ""}, "missing token_refresh_config in E2 channel config"},
		{"missing url", map[string]interface{}{configTokenRefreshConfig: `{"body":"x"}`}, "token_refresh_config.url is required"},
		{"bad match", map[string]interface{}{configTokenRefreshConfig: `{"when":{"match":"all"},"url":"https://auth.example.invalid/t","body":"x"}`}, "token_refresh_config.when.match must be any"},
		{"custom without body", map[string]interface{}{configTokenRefreshConfig: `{"url":"https://auth.example.invalid/t"}`}, "token_refresh_config.body is required when token_refresh_type is custom"},
		{"refresh without refresh_token", map[string]interface{}{configTokenRefreshType: "refresh"}, "refresh_token is required when token_refresh_type is refresh"},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			channel := newSendRefreshChannel(map[string]interface{}{configTokenRefreshConfig: valid})
			for k, v := range tc.set {
				channel.SetConfig(k, v)
			}

			RunChannelSendTestCases(t, channel, newHandler(), []ChannelSendTestCase{{
				Label: "invalid enabled config", Text: "Hello", URN: "tel:" + testURNPath,
				Error: tc.err,
				SendPrep: func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
					c.(*courier.MockChannel).SetConfig(courier.ConfigSendURL, s.URL+partnerPath)
				},
			}}, nil)
		})
	}
}

// ---- direct tests: stateful servers, counters and logs ----

type stubResponse struct {
	status int
	body   string
}

// stubServer answers the partner path from a queue (the last answer repeats) and the token path with one answer, and
// counts the calls. The harness Responses map cannot answer one request shape twice, so it cannot express a 401 that
// is followed by a 200.
type stubServer struct {
	*httptest.Server

	mu           sync.Mutex
	partner      []stubResponse
	token        stubResponse
	partnerCalls int
	tokenCalls   int
	partnerAuth  []string
}

func newStubServer(t *testing.T, partner []stubResponse, token stubResponse) *stubServer {
	stub := &stubServer{partner: partner, token: token}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		stub.mu.Lock()
		defer stub.mu.Unlock()

		answer := stub.token
		if r.URL.Path == tokenPath {
			stub.tokenCalls++
		} else {
			answer = stub.partner[min(stub.partnerCalls, len(stub.partner)-1)]
			stub.partnerCalls++
			stub.partnerAuth = append(stub.partnerAuth, r.Header.Get("Authorization"))
		}
		w.WriteHeader(answer.status)
		_, _ = w.Write([]byte(answer.body))
	}))
	t.Cleanup(stub.Close)
	return stub
}

func (s *stubServer) counts() (partnerCalls, tokenCalls int, auth []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partnerCalls, s.tokenCalls, append([]string(nil), s.partnerAuth...)
}

type directSetup struct {
	partnerURL   string
	tokenURL     string
	tokenType    string
	expired      bool
	statusCodes  []int
	bodyContains []string
	extra        map[string]interface{}
}

// channel builds a channel with a future expiry (no proactive refresh) unless extra says otherwise
func (d directSetup) channel() *courier.MockChannel {
	tokenType := d.tokenType
	if tokenType == "" {
		tokenType = tokenRefreshTypeCustom
	}
	config := map[string]interface{}{
		configExpiresIn:          futureExpiry(),
		configRefreshToken:       fixtureRefreshToken,
		courier.ConfigSendURL:    d.partnerURL + partnerPath,
		configSendMediaURL:       d.partnerURL + partnerPath,
		configTokenRefreshType:   tokenType,
		configTokenRefreshConfig: tokenRefreshJSON(d.tokenURL+tokenPath, tokenType, d.expired, d.statusCodes, d.bodyContains),
		configSendTemplate:       `{"to":"{{.urn.path}}","text":"{{.text}}"{{range .attachments}},"media":"{{attURL .}}"{{end}}}`,
	}
	for k, v := range d.extra {
		config[k] = v
	}
	return newSendRefreshChannel(config)
}

func stubSetup(stub *stubServer) directSetup {
	return directSetup{partnerURL: stub.URL, tokenURL: stub.URL}
}

func directSend(t *testing.T, mb *courier.MockBackend, backend courier.Backend, channel *courier.MockChannel, text string, attachments ...string) (courier.MsgStatus, error) {
	t.Helper()

	mb.AddChannel(channel)
	handler := newHandler()
	require.NoError(t, handler.Initialize(NewTestServer(backend)))

	msg := mb.NewOutgoingMsg(channel, courier.NewMsgID(10), urns.URN("tel:"+testURNPath), text, false, nil, "", 0, "", "")
	for _, attachment := range attachments {
		msg.WithAttachment(attachment)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return handler.SendMsg(ctx, msg)
}

func sendDirect(t *testing.T, channel *courier.MockChannel, text string, attachments ...string) (courier.MsgStatus, error) {
	t.Helper()
	mb := courier.NewMockBackend()
	return directSend(t, mb, mb, channel, text, attachments...)
}

func bearer(token string) string { return "Bearer " + token }

func TestSendingTokenRefreshReplay(t *testing.T) {
	t.Run("401 then token then 200", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{401, `{"error":"expired"}`}, {200, `{"status":"success"}`}}, stubResponse{200, tokenOK.Body})
		setup := stubSetup(stub)
		setup.statusCodes = []int{401}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgWired, status.Status())

		partnerCalls, tokenCalls, auth := stub.counts()
		assert.Equal(t, 2, partnerCalls)
		assert.Equal(t, 1, tokenCalls)
		assert.Equal(t, []string{bearer(fixtureAccessToken1), bearer(fixtureAccessToken2)}, auth)
	})

	t.Run("two consecutive 401 end errored with one token call", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{401, `{"error":"expired"}`}}, stubResponse{200, tokenOK.Body})
		setup := stubSetup(stub)
		setup.statusCodes = []int{401}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgErrored, status.Status())

		partnerCalls, tokenCalls, _ := stub.counts()
		assert.Equal(t, 2, partnerCalls)
		assert.Equal(t, 1, tokenCalls)
	})
}

func TestSendingTokenRefreshReplayBody(t *testing.T) {
	t.Run("non-2xx with the needle is replayed once", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{400, `{"error":"token_expired"}`}, {200, `{"status":"success"}`}}, stubResponse{200, tokenOK.Body})
		setup := stubSetup(stub)
		setup.bodyContains = []string{"token_expired"}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgWired, status.Status())

		partnerCalls, tokenCalls, _ := stub.counts()
		assert.Equal(t, 2, partnerCalls)
		assert.Equal(t, 1, tokenCalls)
	})

	t.Run("5xx with the needle is replayed once", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{500, `token_expired`}, {200, `{"status":"success"}`}}, stubResponse{200, tokenOK.Body})
		setup := stubSetup(stub)
		setup.statusCodes = []int{401}
		setup.bodyContains = []string{"token_expired"}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgWired, status.Status())

		partnerCalls, tokenCalls, _ := stub.counts()
		assert.Equal(t, 2, partnerCalls)
		assert.Equal(t, 1, tokenCalls)
	})

	t.Run("200 with the needle is not replayed", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{200, `{"note":"token_expired"}`}}, stubResponse{200, tokenOK.Body})
		setup := stubSetup(stub)
		setup.bodyContains = []string{"token_expired"}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgWired, status.Status())

		partnerCalls, tokenCalls, _ := stub.counts()
		assert.Equal(t, 1, partnerCalls)
		assert.Equal(t, 0, tokenCalls)
	})

	t.Run("a listed status replays whatever the body says", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{418, `short and stout`}, {200, `{"status":"success"}`}}, stubResponse{200, tokenOK.Body})
		setup := stubSetup(stub)
		setup.statusCodes = []int{418}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgWired, status.Status())

		partnerCalls, tokenCalls, _ := stub.counts()
		assert.Equal(t, 2, partnerCalls)
		assert.Equal(t, 1, tokenCalls)
	})
}

func TestSendingTokenRefreshConnectionError(t *testing.T) {
	partner := httptest.NewServer(http.NotFoundHandler())
	closedURL := partner.URL
	partner.Close()

	tokens := newStubServer(t, []stubResponse{{200, ""}}, stubResponse{200, tokenOK.Body})
	setup := directSetup{partnerURL: closedURL, tokenURL: tokens.URL, statusCodes: []int{401}, bodyContains: []string{"token_expired"}}

	status, err := sendDirect(t, setup.channel(), "Hello")
	require.NoError(t, err)
	assert.Equal(t, courier.MsgErrored, status.Status())

	_, tokenCalls, _ := tokens.counts()
	assert.Equal(t, 0, tokenCalls)
}

func TestSendingTokenRefreshReplayFailure(t *testing.T) {
	t.Run("token call fails after a match", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{401, `{"error":"expired"}`}}, stubResponse{500, `boom`})
		setup := stubSetup(stub)
		setup.statusCodes = []int{401}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgErrored, status.Status())

		partnerCalls, tokenCalls, _ := stub.counts()
		assert.Equal(t, 1, partnerCalls)
		assert.Equal(t, 1, tokenCalls)
	})

	t.Run("matching 2xx status whose refresh fails is still an error", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{200, `{"status":"success"}`}}, stubResponse{500, `boom`})
		setup := stubSetup(stub)
		setup.statusCodes = []int{200}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgErrored, status.Status())
	})

	t.Run("per part bound: 2 token calls and 2 partner calls", func(t *testing.T) {
		stub := newStubServer(t, []stubResponse{{401, `{"error":"expired"}`}}, stubResponse{200, tokenOK.Body})
		setup := stubSetup(stub)
		setup.statusCodes = []int{401}
		setup.expired = true
		setup.extra = map[string]interface{}{configExpiresIn: pastExpiry}

		status, err := sendDirect(t, setup.channel(), "Hello")
		require.NoError(t, err)
		assert.Equal(t, courier.MsgErrored, status.Status())

		partnerCalls, tokenCalls, _ := stub.counts()
		assert.Equal(t, 2, partnerCalls, "one send and one replay")
		assert.Equal(t, 2, tokenCalls, "one proactive and one after the match")
	})
}

func TestSendingTokenRefreshPartnerServerError(t *testing.T) {
	channel := newSendRefreshChannel(map[string]interface{}{configExpiresIn: futureExpiry()})

	RunChannelSendTestCases(t, channel, newHandler(), []ChannelSendTestCase{{
		Label: "partner 5xx that does not match when", Text: "Hello", URN: "tel:" + testURNPath,
		Status:    "E",
		Responses: map[MockedRequest]MockedResponse{partnerReq: {Status: 500, Body: `{"error":"boom"}`}},
		Headers:   map[string]string{"Authorization": bearer(fixtureAccessToken1)},
		// a token call after the 500 would make the token path the last one the server saw
		Path:     partnerPath,
		SendPrep: prepRefresh(tokenRefreshTypeCustom, true, []int{401}),
	}}, nil)
}

func TestSendingTokenRefreshInParts(t *testing.T) {
	channel := newSendRefreshChannel(map[string]interface{}{
		configSendAttachmentInParts: "true",
		configSendTemplate:          `{"to":"{{.urn.path}}","text":"{{.text}}",{{range .attachments}}"media":"{{attURL .}}"{{end}}}`,
	})

	RunChannelSendTestCases(t, channel, newHandler(), []ChannelSendTestCase{{
		Label: "one token call for every part", Text: "Hello", URN: "tel:" + testURNPath,
		Attachments: []string{"image/jpeg:https://foo.bar/1.jpg", "image/jpeg:https://foo.bar/2.jpg"},
		Status:      "W",
		Responses: map[MockedRequest]MockedResponse{
			tokenReq: tokenOK,
			{Method: "POST", Path: mediaPath, BodyContains: "1.jpg"}:            partnerOK,
			{Method: "POST", Path: mediaPath, BodyContains: "2.jpg"}:            partnerOK,
			{Method: "POST", Path: partnerPath, BodyContains: `"text":"Hello"`}: partnerOK,
		},
		Headers:  map[string]string{"Authorization": bearer(fixtureAccessToken2)},
		SendPrep: prepRefresh(tokenRefreshTypeCustom, true, nil),
	}}, nil)
}

func TestSendingTokenRefreshPartsReplay(t *testing.T) {
	// part 1 (attachment): 401, replay 200. part 2 (attachment) and the text part: 200.
	stub := newStubServer(t, []stubResponse{{401, `{"error":"expired"}`}, {200, `{"status":"success"}`}}, stubResponse{200, tokenOK.Body})
	// the queue repeats its last answer, so everything after the replay is a 200
	setup := stubSetup(stub)
	setup.statusCodes = []int{401}
	setup.extra = map[string]interface{}{configSendAttachmentInParts: "true"}

	status, err := sendDirect(t, setup.channel(), "Hello", "image/jpeg:https://foo.bar/1.jpg", "image/jpeg:https://foo.bar/2.jpg")
	require.NoError(t, err)
	assert.Equal(t, courier.MsgWired, status.Status())

	partnerCalls, tokenCalls, auth := stub.counts()
	assert.Equal(t, 1, tokenCalls)
	assert.Equal(t, 4, partnerCalls)
	assert.Equal(t, []string{
		bearer(fixtureAccessToken1), // part 1, first attempt
		bearer(fixtureAccessToken2), // part 1, replay
		bearer(fixtureAccessToken2), // part 2, no further token call
		bearer(fixtureAccessToken2), // text part
	}, auth)
}

// ---- persistence ----

func TestSendingTokenRefreshPersists(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	dates.SetNowSource(dates.NewFixedNowSource(now))
	defer dates.SetNowSource(dates.DefaultNowSource)

	run := func(t *testing.T, tokenBody string) (before, after map[string]interface{}) {
		channel := newSendRefreshChannel(map[string]interface{}{
			configRefreshToken:    fixtureRefreshToken,
			configMTResponseCheck: "success",
		})
		before = channel.Config()

		RunChannelSendTestCases(t, channel, newHandler(), []ChannelSendTestCase{{
			Label: "refresh and persist", Text: "Hello", URN: "tel:" + testURNPath,
			Status: "W",
			Responses: map[MockedRequest]MockedResponse{
				tokenReq:   {Status: 200, Body: tokenBody},
				partnerReq: partnerOK,
			},
			SendPrep: prepRefresh(tokenRefreshTypeCustom, true, nil),
		}}, nil)
		return before, channel.Config()
	}

	t.Run("token response with refresh token and expiry", func(t *testing.T) {
		before, after := run(t, `{"access_token":"`+fixtureAccessToken2+`","refresh_token":"rotated-refresh-token","expires_in":3600}`)

		assert.Equal(t, bearer(fixtureAccessToken2), after[courier.ConfigSendAuthorization])
		assert.Equal(t, "rotated-refresh-token", after[configRefreshToken])
		assert.Equal(t, strconv.FormatInt(now.Unix()+3600, 10), after[configExpiresIn])

		// every other key is kept
		for key, value := range before {
			switch key {
			case courier.ConfigSendAuthorization, configRefreshToken, configExpiresIn:
				continue
			}
			assert.Equal(t, value, after[key], key)
		}
		assert.Equal(t, "success", after[configMTResponseCheck])

		// the map read before the run still holds the old values: the handler saved a clone and did not mutate
		// the shared channel config
		assert.Equal(t, bearer(fixtureAccessToken1), before[courier.ConfigSendAuthorization])
		assert.Equal(t, fixtureRefreshToken, before[configRefreshToken])
		assert.Equal(t, pastExpiry, before[configExpiresIn])
	})

	t.Run("token response without refresh token or expiry", func(t *testing.T) {
		_, after := run(t, tokenOK.Body)

		assert.Equal(t, bearer(fixtureAccessToken2), after[courier.ConfigSendAuthorization])
		assert.Equal(t, fixtureRefreshToken, after[configRefreshToken])
		assert.Equal(t, pastExpiry, after[configExpiresIn])
	})
}

// failingConfigBackend fails every config save and counts the attempts
type failingConfigBackend struct {
	*courier.MockBackend
	saves int
}

func (b *failingConfigBackend) UpdateChannelConfig(ctx context.Context, channel courier.Channel, config map[string]interface{}) error {
	b.saves++
	return errors.New("backend unavailable")
}

func TestSendingTokenRefreshSaveFailure(t *testing.T) {
	logs := CaptureLogs(t)

	stub := newStubServer(t, []stubResponse{{200, `{"status":"success"}`}}, stubResponse{200, tokenOK.Body})
	setup := stubSetup(stub)
	setup.expired = true
	setup.extra = map[string]interface{}{configExpiresIn: pastExpiry}

	mb := courier.NewMockBackend()
	backend := &failingConfigBackend{MockBackend: mb}

	status, err := directSend(t, mb, backend, setup.channel(), "Hello")
	require.NoError(t, err)
	assert.Equal(t, courier.MsgWired, status.Status(), "the send continues with the in-memory token")

	_, tokenCalls, auth := stub.counts()
	assert.Equal(t, 1, tokenCalls)
	assert.Equal(t, []string{bearer(fixtureAccessToken2)}, auth)
	assert.Equal(t, tokenCalls, backend.saves, "one save per successful token call")

	var saveErrors []*logrus.Entry
	for _, entry := range logs.Entries() {
		if entry.Data["event"] == logEventTokenRefreshSave {
			saveErrors = append(saveErrors, entry)
		}
	}
	require.Len(t, saveErrors, 1)
	assert.Equal(t, logrus.ErrorLevel, saveErrors[0].Level)
	assert.Equal(t, testChannelUUID, saveErrors[0].Data["channel_uuid"])
	assert.NotEmpty(t, saveErrors[0].Data["msg_uuid"])

	for _, line := range logs.Lines(t) {
		for _, secret := range []string{fixtureAccessToken1, fixtureAccessToken2, fixtureRefreshToken, fixtureClientSecret} {
			assert.NotContains(t, line, secret)
		}
	}
}

// BenchmarkHandler runs benchmarks on our handler
func BenchmarkHandler(b *testing.B) {
	RunChannelBenchmarks(b, testChannels, newHandler(), handleTestCases)
}
