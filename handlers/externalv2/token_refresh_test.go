package externalv2

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
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
