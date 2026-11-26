package externalv2

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/dates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testChannelUUID = "8eb23e93-5ecb-45ba-b726-3b064e0c56ab"

	// fake values only: nothing here is a real credential
	fixtureAccessToken1 = "test-access-token-1"
	fixtureAccessToken2 = "test-access-token-2"
	fixtureRefreshToken = "fake-refresh-token"
	fixtureClientSecret = "fake-client-secret"
	fixtureTokenURL     = "https://auth.example.invalid/oauth/token"
)

func newRefreshChannel(config map[string]interface{}) *courier.MockChannel {
	return courier.NewMockChannel(testChannelUUID, "E2", "2020", "US", config)
}

// jsonString serializes v, as an operator would when filling token_refresh_config
func jsonString(t testing.TB, v interface{}) string {
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func validCustomConfig(t testing.TB) map[string]interface{} {
	return map[string]interface{}{
		"url":  fixtureTokenURL,
		"body": "grant_type=password&client_secret=" + fixtureClientSecret,
	}
}

func enabledChannel(t testing.TB, tokenType string, refreshConfig map[string]interface{}, extra map[string]interface{}) *courier.MockChannel {
	config := map[string]interface{}{
		configTokenRefreshEnabled: "true",
		configTokenRefreshType:    tokenType,
		configTokenRefreshConfig:  jsonString(t, refreshConfig),
	}
	for k, v := range extra {
		config[k] = v
	}
	return newRefreshChannel(config)
}

func TestParseTokenRefreshDisabled(t *testing.T) {
	validConfig := jsonString(t, validCustomConfig(t))

	tcs := []struct {
		label  string
		config map[string]interface{}
	}{
		{"key absent", map[string]interface{}{configTokenRefreshType: "custom", configTokenRefreshConfig: validConfig}},
		{"empty", map[string]interface{}{configTokenRefreshEnabled: ""}},
		{"false", map[string]interface{}{configTokenRefreshEnabled: "false"}},
		{"no", map[string]interface{}{configTokenRefreshEnabled: "no"}},
		{"zero", map[string]interface{}{configTokenRefreshEnabled: "0"}},
		{"enabled word", map[string]interface{}{configTokenRefreshEnabled: "enabled"}},
		{"non string", map[string]interface{}{configTokenRefreshEnabled: true}},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			opts, err := parseTokenRefreshOptions(newRefreshChannel(tc.config))
			assert.NoError(t, err)
			assert.Nil(t, opts)
		})
	}
}

func TestParseTokenRefreshCustom(t *testing.T) {
	full := map[string]interface{}{
		"when": map[string]interface{}{
			"match":         "any",
			"status_codes":  []int{401, 403},
			"body_contains": []string{"token_expired"},
			"expired":       true,
		},
		"method":              "PUT",
		"url":                 "  " + fixtureTokenURL + "  ",
		"headers":             map[string]string{"X-Api": "abc"},
		"body":                "grant_type=password",
		"token_field":         "data.token",
		"refresh_token_field": "data.refresh",
		"expires_in_field":    "data.ttl",
		"expires_in_default":  7200,
	}

	opts, err := parseTokenRefreshOptions(enabledChannel(t, "custom", full, nil))
	require.NoError(t, err)
	require.NotNil(t, opts)

	assert.Equal(t, tokenRefreshTypeCustom, opts.Type)
	assert.Equal(t, fixtureTokenURL, opts.Config.URL)
	assert.Equal(t, http.MethodPut, opts.Config.Method)
	assert.Equal(t, map[string]string{"X-Api": "abc"}, opts.Config.Headers)
	assert.Equal(t, "grant_type=password", opts.Config.Body)
	assert.Equal(t, []int{401, 403}, opts.Config.When.StatusCodes)
	assert.Equal(t, []string{"token_expired"}, opts.Config.When.BodyContains)
	assert.True(t, opts.Config.When.Expired)
	assert.Equal(t, tokenRefreshMatchAny, opts.Config.When.Match)
	assert.Equal(t, "data.token", opts.Config.TokenField)
	assert.Equal(t, "data.refresh", opts.Config.RefreshTokenField)
	assert.Equal(t, "data.ttl", opts.Config.ExpiresInField)
	assert.Equal(t, int64(7200), opts.Config.ExpiresInDefault)

	t.Run("defaults", func(t *testing.T) {
		opts, err := parseTokenRefreshOptions(enabledChannel(t, "custom", validCustomConfig(t), nil))
		require.NoError(t, err)
		require.NotNil(t, opts)
		assert.Equal(t, http.MethodPost, opts.Config.Method)
		assert.Equal(t, "access_token", opts.Config.TokenField)
		assert.Equal(t, tokenRefreshMatchAny, opts.Config.When.Match)
		assert.False(t, opts.Config.When.Expired)
		assert.Empty(t, opts.Config.When.StatusCodes)
		assert.Empty(t, opts.Config.RefreshTokenField)
		assert.Zero(t, opts.ExpiresAt)
	})

	for _, enabled := range []string{"true", "TRUE", "1", "yes", " Yes "} {
		t.Run("enabled "+enabled, func(t *testing.T) {
			ch := enabledChannel(t, "custom", validCustomConfig(t), map[string]interface{}{configTokenRefreshEnabled: enabled})
			opts, err := parseTokenRefreshOptions(ch)
			require.NoError(t, err)
			assert.NotNil(t, opts)
		})
	}

	t.Run("type and match are case-insensitive after trim", func(t *testing.T) {
		cfg := validCustomConfig(t)
		cfg["when"] = map[string]interface{}{"match": " Any "}
		ch := enabledChannel(t, " CUSTOM ", cfg, nil)
		opts, err := parseTokenRefreshOptions(ch)
		require.NoError(t, err)
		require.NotNil(t, opts)
		assert.Equal(t, tokenRefreshTypeCustom, opts.Type)
		assert.Equal(t, tokenRefreshMatchAny, opts.Config.When.Match)
	})
}

func TestParseTokenRefreshInvalid(t *testing.T) {
	enabled := func(tokenType, refreshConfig string) map[string]interface{} {
		config := map[string]interface{}{configTokenRefreshEnabled: "true"}
		if tokenType != "" {
			config[configTokenRefreshType] = tokenType
		}
		if refreshConfig != "" {
			config[configTokenRefreshConfig] = refreshConfig
		}
		return config
	}
	valid := jsonString(t, validCustomConfig(t))

	tcs := []struct {
		label  string
		config map[string]interface{}
		err    string // exact, or a prefix when prefix is true
		prefix bool
	}{
		{"missing type", enabled("", valid), "token_refresh_type must be custom or refresh", false},
		{"unknown type", enabled("oauth", valid), "token_refresh_type must be custom or refresh", false},
		{"empty config", enabled("custom", ""), "missing token_refresh_config in E2 channel config", false},
		{"invalid json", enabled("custom", "{"), "invalid token_refresh_config: ", true},
		{"missing url", enabled("custom", `{"body":"x"}`), "token_refresh_config.url is required", false},
		{"blank url", enabled("custom", `{"url":"  ","body":"x"}`), "token_refresh_config.url is required", false},
		{"bad match", enabled("custom", `{"when":{"match":"all"},"url":"https://auth.example.invalid/t","body":"x"}`), "token_refresh_config.when.match must be any", false},
		{"custom without body", enabled("custom", `{"url":"https://auth.example.invalid/t"}`), "token_refresh_config.body is required when token_refresh_type is custom", false},
		// rule order
		{"type error before config error", enabled("oauth", ""), "token_refresh_type must be custom or refresh", false},
		{"url error before match error", enabled("custom", `{"when":{"match":"all"}}`), "token_refresh_config.url is required", false},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			opts, err := parseTokenRefreshOptions(newRefreshChannel(tc.config))
			assert.Nil(t, opts)
			require.Error(t, err)
			if tc.prefix {
				assert.True(t, strings.HasPrefix(err.Error(), tc.err), err.Error())
			} else {
				assert.Equal(t, tc.err, err.Error())
			}
		})
	}
}

func TestParseTokenRefreshRefreshType(t *testing.T) {
	cfg := map[string]interface{}{"url": fixtureTokenURL}

	t.Run("missing refresh_token", func(t *testing.T) {
		opts, err := parseTokenRefreshOptions(enabledChannel(t, "refresh", cfg, nil))
		assert.Nil(t, opts)
		require.Error(t, err)
		assert.Equal(t, "refresh_token is required when token_refresh_type is refresh", err.Error())
	})

	t.Run("blank refresh_token", func(t *testing.T) {
		_, err := parseTokenRefreshOptions(enabledChannel(t, "refresh", cfg, map[string]interface{}{configRefreshToken: "   "}))
		require.Error(t, err)
	})

	t.Run("with refresh_token", func(t *testing.T) {
		extra := map[string]interface{}{
			configRefreshToken: " " + fixtureRefreshToken + " ",
			configClientID:     " abc ",
			configClientSecret: " " + fixtureClientSecret + " ",
		}
		opts, err := parseTokenRefreshOptions(enabledChannel(t, "refresh", cfg, extra))
		require.NoError(t, err)
		require.NotNil(t, opts)
		assert.Equal(t, tokenRefreshTypeRefresh, opts.Type)
		assert.Equal(t, fixtureRefreshToken, opts.RefreshToken)
		assert.Equal(t, "abc", opts.ClientID)
		assert.Equal(t, fixtureClientSecret, opts.ClientSecret)
		assert.Empty(t, opts.Config.Body, "body is not required for refresh")
	})

	t.Run("body is ignored", func(t *testing.T) {
		withBody := map[string]interface{}{"url": fixtureTokenURL, "body": "ignored=true"}
		opts, err := parseTokenRefreshOptions(enabledChannel(t, "refresh", withBody, map[string]interface{}{configRefreshToken: fixtureRefreshToken}))
		require.NoError(t, err)
		require.NotNil(t, opts)
	})
}

func TestParseTokenRefreshExpiresIn(t *testing.T) {
	parse := func(t *testing.T, value interface{}) (*tokenRefreshOptions, error) {
		extra := map[string]interface{}{}
		if value != nil {
			extra[configExpiresIn] = value
		}
		return parseTokenRefreshOptions(enabledChannel(t, "custom", validCustomConfig(t), extra))
	}

	t.Run("unix expiry, not a duration", func(t *testing.T) {
		opts, err := parse(t, "1731500000")
		require.NoError(t, err)
		assert.Equal(t, int64(1731500000), opts.ExpiresAt)
	})

	t.Run("trimmed", func(t *testing.T) {
		opts, err := parse(t, "  1731500000 ")
		require.NoError(t, err)
		assert.Equal(t, int64(1731500000), opts.ExpiresAt)
	})

	t.Run("empty", func(t *testing.T) {
		opts, err := parse(t, "")
		require.NoError(t, err)
		assert.Zero(t, opts.ExpiresAt)
	})

	t.Run("invalid", func(t *testing.T) {
		opts, err := parse(t, "abc")
		assert.Nil(t, opts)
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "invalid expires_in: "), err.Error())
	})

	t.Run("json number is treated as absent", func(t *testing.T) {
		opts, err := parse(t, float64(1731500000))
		require.NoError(t, err)
		assert.Zero(t, opts.ExpiresAt)
	})
}

func TestTokenRefreshWhen(t *testing.T) {
	opts := &tokenRefreshOptions{Config: tokenRefreshConfig{When: tokenRefreshWhen{
		StatusCodes:  []int{401, 200},
		BodyContains: []string{"token_expired", ""},
	}}}

	rr := func(status int, body string) *utils.RequestResponse {
		return &utils.RequestResponse{StatusCode: status, Body: []byte(body)}
	}

	tcs := []struct {
		label string
		rr    *utils.RequestResponse
		match bool
	}{
		{"listed status", rr(401, ""), true},
		{"listed 2xx status matches on any body", rr(200, "ok"), true},
		{"needle on non-2xx", rr(400, `{"error":"token_expired"}`), true},
		{"needle on 5xx", rr(500, "token_expired"), true},
		{"needle on 2xx does not match", rr(201, "token_expired"), false},
		{"non-2xx without needle", rr(400, "bad request"), false},
		{"empty needle is ignored", rr(400, "anything"), false},
		{"nil response", nil, false},
		{"connection failure", rr(0, "dial tcp: token_expired"), false},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			assert.Equal(t, tc.match, opts.matches(tc.rr))
		})
	}

	t.Run("non-2xx that also carries an error still matches", func(t *testing.T) {
		// the caller passes the RequestResponse even when MakeHTTPRequest returned an error
		assert.True(t, opts.matches(&utils.RequestResponse{StatusCode: 401, Status: utils.RRStatusFailure}))
	})
}

func TestLookupJSONPath(t *testing.T) {
	tcs := []struct {
		label string
		body  string
		path  string
		value string
		found bool
	}{
		{"top level", `{"access_token":"abc"}`, "access_token", "abc", true},
		{"dotted", `{"data":{"token":"abc"}}`, "data.token", "abc", true},
		{"whole number", `{"n":3600}`, "n", "3600", true},
		{"large integer", `{"n":12345678901234567}`, "n", "12345678901234567", true},
		{"float", `{"n":1.5}`, "n", "1.5", true},
		{"boolean", `{"ok":true}`, "ok", "true", true},
		{"missing key", `{"a":"b"}`, "c", "", false},
		{"empty string", `{"a":""}`, "a", "", false},
		{"null", `{"a":null}`, "a", "", false},
		{"object", `{"a":{"b":"c"}}`, "a", "", false},
		{"array", `{"a":["b"]}`, "a", "", false},
		{"not json", `not json`, "a", "", false},
		{"arrays are not walked", `{"items":["x"]}`, "items.0", "", false},
		{"empty path", `{"a":"b"}`, "", "", false},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			value, found := lookupJSONPath([]byte(tc.body), tc.path)
			assert.Equal(t, tc.found, found)
			assert.Equal(t, tc.value, value)
		})
	}
}

func TestParseExpiresInSeconds(t *testing.T) {
	tcs := []struct {
		label   string
		body    string
		field   string
		seconds int64
		ok      bool
	}{
		{"integer", `{"expires_in":3600}`, "expires_in", 3600, true},
		{"whole float", `{"expires_in":3600.0}`, "expires_in", 3600, true},
		{"dotted", `{"data":{"ttl":60}}`, "data.ttl", 60, true},
		{"numeric string is rejected", `{"expires_in":"3600"}`, "expires_in", 0, false},
		{"fraction is rejected", `{"expires_in":3600.5}`, "expires_in", 0, false},
		{"zero", `{"expires_in":0}`, "expires_in", 0, false},
		{"negative", `{"expires_in":-5}`, "expires_in", 0, false},
		{"empty field name", `{"expires_in":3600}`, "", 0, false},
		{"missing field", `{"other":3600}`, "expires_in", 0, false},
		{"non numeric string", `{"expires_in":"soon"}`, "expires_in", 0, false},
		{"not json", `nope`, "expires_in", 0, false},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			seconds, ok := parseExpiresInSeconds([]byte(tc.body), tc.field)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.seconds, seconds)
		})
	}
}

// ---------------------------------------------------------------------------
// Phase 2: token call
// ---------------------------------------------------------------------------

func customOptions(tokenURL string) *tokenRefreshOptions {
	return &tokenRefreshOptions{
		Type: tokenRefreshTypeCustom,
		Config: tokenRefreshConfig{
			When:       tokenRefreshWhen{Match: tokenRefreshMatchAny},
			Method:     http.MethodPost,
			URL:        tokenURL,
			Body:       "grant_type=password&client_secret=" + fixtureClientSecret,
			TokenField: "access_token",
		},
	}
}

func refreshOptions(tokenURL string) *tokenRefreshOptions {
	return &tokenRefreshOptions{
		Type:         tokenRefreshTypeRefresh,
		Config:       tokenRefreshConfig{When: tokenRefreshWhen{Match: tokenRefreshMatchAny}, Method: http.MethodPost, URL: tokenURL, TokenField: "access_token"},
		RefreshToken: fixtureRefreshToken,
		ClientID:     "abc",
		ClientSecret: fixtureClientSecret,
	}
}

func TestBuildTokenRequestRefresh(t *testing.T) {
	read := func(t *testing.T, req *http.Request) url.Values {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		values, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		return values
	}

	t.Run("form fields and default content type", func(t *testing.T) {
		req, err := buildTokenRequest(refreshOptions(fixtureTokenURL), "rotated-refresh-token")
		require.NoError(t, err)

		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, fixtureTokenURL, req.URL.String())
		assert.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))

		values := read(t, req)
		assert.Equal(t, "refresh_token", values.Get("grant_type"))
		assert.Equal(t, "rotated-refresh-token", values.Get("refresh_token"))
		assert.Equal(t, "abc", values.Get("client_id"))
		assert.Equal(t, fixtureClientSecret, values.Get("client_secret"))
	})

	t.Run("client id and secret omitted when empty", func(t *testing.T) {
		opts := refreshOptions(fixtureTokenURL)
		opts.ClientID, opts.ClientSecret = "", ""
		req, err := buildTokenRequest(opts, fixtureRefreshToken)
		require.NoError(t, err)

		values := read(t, req)
		assert.False(t, values.Has("client_id"))
		assert.False(t, values.Has("client_secret"))
		assert.Equal(t, fixtureRefreshToken, values.Get("refresh_token"))
	})

	t.Run("operator content type is kept in any letter case", func(t *testing.T) {
		opts := refreshOptions(fixtureTokenURL)
		opts.Config.Headers = map[string]string{"content-type": "application/x-custom"}
		req, err := buildTokenRequest(opts, fixtureRefreshToken)
		require.NoError(t, err)

		assert.Equal(t, []string{"application/x-custom"}, req.Header.Values("Content-Type"))
	})
}

func TestBuildTokenRequestCustom(t *testing.T) {
	opts := customOptions(fixtureTokenURL)
	opts.Config.Method = http.MethodPut
	opts.Config.Headers = map[string]string{"X-Api": "abc", "Content-Type": "application/json"}
	opts.Config.Body = `{"grant":"password"}`

	req, err := buildTokenRequest(opts, "ignored")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPut, req.Method)
	assert.Equal(t, fixtureTokenURL, req.URL.String())
	assert.Equal(t, "abc", req.Header.Get("X-Api"))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"grant":"password"}`, string(body))

	t.Run("default method", func(t *testing.T) {
		opts := customOptions(fixtureTokenURL)
		req, err := buildTokenRequest(opts, "")
		require.NoError(t, err)
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Empty(t, req.Header.Get("Content-Type"), "custom sends only the configured headers")
	})
}

func tokenServer(t *testing.T, status int, body string) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func tokenChannel() courier.Channel {
	return newRefreshChannel(map[string]interface{}{})
}

func TestRequestAccessTokenFields(t *testing.T) {
	msgID := courier.NewMsgID(10)

	t.Run("default access_token", func(t *testing.T) {
		server := tokenServer(t, 200, `{"access_token":"`+fixtureAccessToken2+`"}`)
		result, log, err := requestAccessToken(tokenChannel(), msgID, customOptions(server.URL), "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, log)
		assert.Equal(t, fixtureAccessToken2, result.AccessToken)
		assert.Empty(t, result.RefreshToken)
		assert.Zero(t, result.ExpiresAt)
	})

	t.Run("dotted token_field", func(t *testing.T) {
		server := tokenServer(t, 200, `{"data":{"token":"`+fixtureAccessToken2+`"}}`)
		opts := customOptions(server.URL)
		opts.Config.TokenField = "data.token"
		result, _, err := requestAccessToken(tokenChannel(), msgID, opts, "")
		require.NoError(t, err)
		assert.Equal(t, fixtureAccessToken2, result.AccessToken)
	})

	t.Run("refresh token present", func(t *testing.T) {
		server := tokenServer(t, 200, `{"access_token":"a","refresh_token":"rotated"}`)
		opts := customOptions(server.URL)
		opts.Config.RefreshTokenField = "refresh_token"
		result, _, err := requestAccessToken(tokenChannel(), msgID, opts, "")
		require.NoError(t, err)
		assert.Equal(t, "rotated", result.RefreshToken)
	})

	for label, body := range map[string]string{
		"absent": `{"access_token":"a"}`,
		"empty":  `{"access_token":"a","refresh_token":""}`,
	} {
		t.Run("refresh token "+label+" means unchanged", func(t *testing.T) {
			server := tokenServer(t, 200, body)
			opts := customOptions(server.URL)
			opts.Config.RefreshTokenField = "refresh_token"
			result, _, err := requestAccessToken(tokenChannel(), msgID, opts, "")
			require.NoError(t, err)
			assert.Empty(t, result.RefreshToken)
		})
	}

	t.Run("refresh token field not configured", func(t *testing.T) {
		server := tokenServer(t, 200, `{"access_token":"a","refresh_token":"ignored"}`)
		result, _, err := requestAccessToken(tokenChannel(), msgID, customOptions(server.URL), "")
		require.NoError(t, err)
		assert.Empty(t, result.RefreshToken)
	})
}

func TestRequestAccessTokenExpiry(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	dates.SetNowSource(dates.NewFixedNowSource(now))
	defer dates.SetNowSource(dates.DefaultNowSource)

	msgID := courier.NewMsgID(10)
	call := func(t *testing.T, body string, field string, def int64) *tokenResult {
		server := tokenServer(t, 200, body)
		opts := customOptions(server.URL)
		opts.Config.ExpiresInField = field
		opts.Config.ExpiresInDefault = def
		result, _, err := requestAccessToken(tokenChannel(), msgID, opts, "")
		require.NoError(t, err)
		return result
	}

	t.Run("field in seconds", func(t *testing.T) {
		result := call(t, `{"access_token":"a","expires_in":3600}`, "expires_in", 0)
		assert.Equal(t, now.Unix()+3600, result.ExpiresAt)
	})

	t.Run("default when the field is absent", func(t *testing.T) {
		result := call(t, `{"access_token":"a"}`, "expires_in", 7200)
		assert.Equal(t, now.Unix()+7200, result.ExpiresAt)
	})

	t.Run("default when the field is not a positive number", func(t *testing.T) {
		result := call(t, `{"access_token":"a","expires_in":"3600"}`, "expires_in", 7200)
		assert.Equal(t, now.Unix()+7200, result.ExpiresAt)
		result = call(t, `{"access_token":"a","expires_in":0}`, "expires_in", 7200)
		assert.Equal(t, now.Unix()+7200, result.ExpiresAt)
	})

	t.Run("field wins over default", func(t *testing.T) {
		result := call(t, `{"access_token":"a","expires_in":60}`, "expires_in", 7200)
		assert.Equal(t, now.Unix()+60, result.ExpiresAt)
	})

	t.Run("neither means unchanged", func(t *testing.T) {
		result := call(t, `{"access_token":"a"}`, "", 0)
		assert.Zero(t, result.ExpiresAt)
	})
}

func TestRequestAccessTokenFailures(t *testing.T) {
	msgID := courier.NewMsgID(10)

	tcs := []struct {
		label  string
		status int
		body   string
		reason string
	}{
		{"400", 400, `{"access_token":"` + fixtureAccessToken2 + `"}`, tokenFailureStatus4xx},
		{"401", 401, `{"access_token":"` + fixtureAccessToken2 + `"}`, tokenFailureStatus4xx},
		{"500", 500, `{"access_token":"` + fixtureAccessToken2 + `"}`, tokenFailureStatus5xx},
		{"missing token field", 200, `{"other":"x"}`, tokenFailureMissingField},
		{"empty token field", 200, `{"access_token":""}`, tokenFailureMissingField},
		{"non scalar token field", 200, `{"access_token":{"a":"b"}}`, tokenFailureMissingField},
		{"not json", 200, `not json`, tokenFailureInvalidJSON},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			server := tokenServer(t, tc.status, tc.body)
			result, log, err := requestAccessToken(tokenChannel(), msgID, customOptions(server.URL), "")
			assert.Nil(t, result)
			require.Error(t, err)
			require.NotNil(t, log)
			assert.Equal(t, tc.reason, log.Error)
			assert.NotContains(t, err.Error(), fixtureAccessToken2)
		})
	}

	t.Run("connection failure", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		closedURL := server.URL
		server.Close()

		result, log, err := requestAccessToken(tokenChannel(), msgID, customOptions(closedURL), "")
		assert.Nil(t, result)
		require.Error(t, err)
		require.NotNil(t, log)
		assert.Equal(t, tokenFailureConnection, log.Error)
		assert.Zero(t, log.StatusCode)
	})

	t.Run("final 3xx status below 400 is parsed like a success", func(t *testing.T) {
		server := tokenServer(t, http.StatusMultipleChoices, `{"access_token":"`+fixtureAccessToken2+`"}`)
		result, _, err := requestAccessToken(tokenChannel(), msgID, customOptions(server.URL), "")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, fixtureAccessToken2, result.AccessToken)
	})

	t.Run("refresh failure keeps the reason out of the error text", func(t *testing.T) {
		server := tokenServer(t, 500, "boom "+fixtureClientSecret)
		_, _, err := requestAccessToken(tokenChannel(), msgID, customOptions(server.URL), "")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), fixtureClientSecret)
	})
}

func TestRequestAccessTokenLog(t *testing.T) {
	msgID := courier.NewMsgID(10)
	secrets := []string{fixtureClientSecret, fixtureRefreshToken, fixtureAccessToken1, "fake-key", "fake-pass"}

	assertClean := func(t *testing.T, log *courier.ChannelLog) {
		fields := []string{log.Description, log.Method, log.URL, log.Error, log.Request, log.Response}
		for _, f := range fields {
			for _, secret := range secrets {
				assert.NotContains(t, f, secret)
			}
		}
		assert.Empty(t, log.Request)
		assert.Empty(t, log.Response)
		assert.Equal(t, tokenRefreshLogDescription, log.Description)
		assert.Equal(t, http.MethodPost, log.Method)
	}

	withCredentials := func(rawURL string) string {
		u, err := url.Parse(rawURL)
		require.NoError(t, err)
		u.User = url.UserPassword("user", "fake-pass")
		u.Path = "/oauth/token"
		u.RawQuery = "api_key=fake-key"
		return u.String()
	}

	t.Run("success", func(t *testing.T) {
		server := tokenServer(t, 200, `{"access_token":"`+fixtureAccessToken1+`","refresh_token":"`+fixtureRefreshToken+`"}`)
		opts := customOptions(withCredentials(server.URL))
		opts.Config.RefreshTokenField = "refresh_token"

		result, log, err := requestAccessToken(tokenChannel(), msgID, opts, fixtureRefreshToken)
		require.NoError(t, err)
		require.NotNil(t, result)
		assertClean(t, log)
		assert.Empty(t, log.Error)
		assert.Equal(t, http.StatusOK, log.StatusCode)
		assert.Equal(t, server.URL+"/oauth/token", log.URL)
		assert.Equal(t, msgID, log.MsgID)
	})

	t.Run("each failure", func(t *testing.T) {
		for _, status := range []int{400, 500} {
			server := tokenServer(t, status, `{"error":"`+fixtureClientSecret+`"}`)
			_, log, err := requestAccessToken(tokenChannel(), msgID, customOptions(withCredentials(server.URL)), fixtureRefreshToken)
			require.Error(t, err)
			assertClean(t, log)
			assert.NotEmpty(t, log.Error)
			assert.Equal(t, status, log.StatusCode)
		}

		server := tokenServer(t, 200, `not json`)
		_, log, err := requestAccessToken(tokenChannel(), msgID, customOptions(withCredentials(server.URL)), fixtureRefreshToken)
		require.Error(t, err)
		assertClean(t, log)

		closed := httptest.NewServer(http.NotFoundHandler())
		closedURL := withCredentials(closed.URL)
		closed.Close()
		_, log, err = requestAccessToken(tokenChannel(), msgID, customOptions(closedURL), fixtureRefreshToken)
		require.Error(t, err)
		assertClean(t, log)
		assert.Zero(t, log.StatusCode)
		assert.Equal(t, tokenFailureConnection, log.Error)
	})
}
