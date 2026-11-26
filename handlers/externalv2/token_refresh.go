package externalv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/dates"
)

const (
	// maxTokenRefreshReplays caps the replays of one message part after a token refresh. The replay re-sends a
	// request the partner may already have seen, so it is bounded to a single attempt.
	maxTokenRefreshReplays = 1

	// tokenRefreshReplayDelay is the backoff before a replay. It is zero on purpose: the replay carries a new
	// credential, and waiting cannot fix a stale one. It stays a named value so the strategy is visible.
	tokenRefreshReplayDelay = 0 * time.Second

	bearerPrefix = "Bearer "

	defaultTokenField  = "access_token"
	defaultTokenMethod = http.MethodPost

	tokenRefreshMatchAny    = "any"
	tokenRefreshTypeCustom  = "custom"
	tokenRefreshTypeRefresh = "refresh"

	refreshTokenGrantType      = "refresh_token"
	tokenRefreshLogDescription = "Token Refresh"

	configTokenRefreshEnabled = "token_refresh_enabled"
	configTokenRefreshType    = "token_refresh_type"
	configTokenRefreshConfig  = "token_refresh_config"
	configRefreshToken        = "refresh_token"
	configExpiresIn           = "expires_in"
	configClientID            = "client_id"
	configClientSecret        = "client_secret"

	formFieldGrantType    = "grant_type"
	formFieldRefreshToken = "refresh_token"
	formFieldClientID     = "client_id"
	formFieldClientSecret = "client_secret"

	headerContentType = "Content-Type"

	// jsonPathSeparator splits a dotted path such as data.token into object keys
	jsonPathSeparator = "."

	// fixed failure reasons: they feed the Token Refresh log error and the application log reason, and they are
	// never a raw error, because a transport error holds the URL and a body can echo credentials
	tokenFailureConnection   = "connection"
	tokenFailureStatus4xx    = "status_4xx"
	tokenFailureStatus5xx    = "status_5xx"
	tokenFailureInvalidJSON  = "invalid_json"
	tokenFailureMissingField = "missing_token_field"
)

// tokenRefreshEnabledValues are the values of token_refresh_enabled that turn the feature on (a slice cannot be a const)
var tokenRefreshEnabledValues = []string{"true", "1", "yes"}

// tokenRefreshWhen decides when the access token is renewed. Conditions are OR.
type tokenRefreshWhen struct {
	Match        string   `json:"match"`
	StatusCodes  []int    `json:"status_codes"`
	BodyContains []string `json:"body_contains"`
	Expired      bool     `json:"expired"`
}

// tokenRefreshConfig is the JSON stored in token_refresh_config
type tokenRefreshConfig struct {
	When              tokenRefreshWhen  `json:"when"`
	Method            string            `json:"method"`
	URL               string            `json:"url"`
	Headers           map[string]string `json:"headers"`
	Body              string            `json:"body"`
	TokenField        string            `json:"token_field"`
	RefreshTokenField string            `json:"refresh_token_field"`
	ExpiresInField    string            `json:"expires_in_field"`
	ExpiresInDefault  int64             `json:"expires_in_default"`
}

// tokenRefreshOptions is the immutable result of parsing the channel config, one per SendMsg
type tokenRefreshOptions struct {
	Type         string
	Config       tokenRefreshConfig
	RefreshToken string
	ClientID     string
	ClientSecret string
	ExpiresAt    int64
}

// tokenResult holds only what a token call changed: an empty RefreshToken and a zero ExpiresAt mean "unchanged"
type tokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
}

// tokenRefreshError is a failed token call. Its text is a fixed reason, never the underlying error.
type tokenRefreshError struct {
	Reason string
}

func (e *tokenRefreshError) Error() string {
	return "token refresh failed: " + e.Reason
}

// parseTokenRefreshOptions reads the opt-in keys from the channel config. It returns (nil, nil) when the feature is
// off, and an error for an invalid enabled config. The rules run in the order of the spec.
func parseTokenRefreshOptions(channel courier.Channel) (*tokenRefreshOptions, error) {
	if !tokenRefreshEnabled(channel.StringConfigForKey(configTokenRefreshEnabled, "")) {
		return nil, nil
	}

	tokenType := strings.ToLower(strings.TrimSpace(channel.StringConfigForKey(configTokenRefreshType, "")))
	if tokenType != tokenRefreshTypeCustom && tokenType != tokenRefreshTypeRefresh {
		return nil, fmt.Errorf("token_refresh_type must be custom or refresh")
	}

	rawConfig := strings.TrimSpace(channel.StringConfigForKey(configTokenRefreshConfig, ""))
	if rawConfig == "" {
		return nil, fmt.Errorf("missing token_refresh_config in E2 channel config")
	}

	cfg := tokenRefreshConfig{}
	if err := json.Unmarshal([]byte(rawConfig), &cfg); err != nil {
		return nil, fmt.Errorf("invalid token_refresh_config: %s", err)
	}

	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.URL == "" {
		return nil, fmt.Errorf("token_refresh_config.url is required")
	}

	cfg.When.Match = strings.ToLower(strings.TrimSpace(cfg.When.Match))
	if cfg.When.Match == "" {
		cfg.When.Match = tokenRefreshMatchAny
	}
	if cfg.When.Match != tokenRefreshMatchAny {
		return nil, fmt.Errorf("token_refresh_config.when.match must be any")
	}

	if tokenType == tokenRefreshTypeCustom && cfg.Body == "" {
		return nil, fmt.Errorf("token_refresh_config.body is required when token_refresh_type is custom")
	}

	refreshToken := strings.TrimSpace(channel.StringConfigForKey(configRefreshToken, ""))
	if tokenType == tokenRefreshTypeRefresh && refreshToken == "" {
		return nil, fmt.Errorf("refresh_token is required when token_refresh_type is refresh")
	}

	var expiresAt int64
	if rawExpiresIn := strings.TrimSpace(channel.StringConfigForKey(configExpiresIn, "")); rawExpiresIn != "" {
		parsed, err := strconv.ParseInt(rawExpiresIn, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid expires_in: %s", err)
		}
		expiresAt = parsed
	}

	cfg.Method = strings.TrimSpace(cfg.Method)
	if cfg.Method == "" {
		cfg.Method = defaultTokenMethod
	}
	cfg.TokenField = strings.TrimSpace(cfg.TokenField)
	if cfg.TokenField == "" {
		cfg.TokenField = defaultTokenField
	}
	cfg.RefreshTokenField = strings.TrimSpace(cfg.RefreshTokenField)
	cfg.ExpiresInField = strings.TrimSpace(cfg.ExpiresInField)

	return &tokenRefreshOptions{
		Type:         tokenType,
		Config:       cfg,
		RefreshToken: refreshToken,
		ClientID:     strings.TrimSpace(channel.StringConfigForKey(configClientID, "")),
		ClientSecret: strings.TrimSpace(channel.StringConfigForKey(configClientSecret, "")),
		ExpiresAt:    expiresAt,
	}, nil
}

func tokenRefreshEnabled(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, enabled := range tokenRefreshEnabledValues {
		if value == enabled {
			return true
		}
	}
	return false
}

// matches reports whether a partner response asks for a token refresh: its status is in status_codes, or it is
// non-2xx and its body holds a non-empty body_contains entry.
// A 2xx body is never matched, so a response the partner reported as delivered is not sent twice (spec Q3).
// A nil response or a connection failure (status 0) never matches.
func (o *tokenRefreshOptions) matches(rr *utils.RequestResponse) bool {
	if rr == nil || rr.StatusCode == 0 {
		return false
	}

	for _, code := range o.Config.When.StatusCodes {
		if rr.StatusCode == code {
			return true
		}
	}

	if rr.StatusCode >= http.StatusOK && rr.StatusCode < http.StatusMultipleChoices {
		return false
	}

	body := string(rr.Body)
	for _, needle := range o.Config.When.BodyContains {
		if needle != "" && strings.Contains(body, needle) {
			return true
		}
	}
	return false
}

// decodeJSON decodes a body keeping numbers as text. A body with trailing data is not valid JSON.
func decodeJSON(raw []byte) (interface{}, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var doc interface{}
	if err := decoder.Decode(&doc); err != nil {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return doc, true
}

// valueAtPath walks a dotted path through JSON objects only (not arrays)
func valueAtPath(doc interface{}, path string) (interface{}, bool) {
	if path == "" {
		return nil, false
	}

	current := doc
	for _, key := range strings.Split(path, jsonPathSeparator) {
		object, isObject := current.(map[string]interface{})
		if !isObject {
			return nil, false
		}
		value, found := object[key]
		if !found {
			return nil, false
		}
		current = value
	}
	return current, true
}

// scalarAtPath returns the text of a string, number or boolean at the path. An empty string, null, object or array
// is a miss (stricter than Mailroom, which turns objects and arrays into text).
func scalarAtPath(doc interface{}, path string) (string, bool) {
	value, found := valueAtPath(doc, path)
	if !found {
		return "", false
	}

	switch v := value.(type) {
	case string:
		return v, v != ""
	case json.Number:
		return v.String(), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

// lookupJSONPath returns the scalar at a dotted path of a JSON body
func lookupJSONPath(raw []byte, path string) (string, bool) {
	doc, ok := decodeJSON(raw)
	if !ok {
		return "", false
	}
	return scalarAtPath(doc, path)
}

// secondsAtPath reads a duration in whole, positive seconds. It must see the JSON type: a JSON number is accepted and
// a JSON string is rejected, even a numeric one, and so is a fraction. This is stricter than Mailroom (research R3).
// A rejected value counts as absent, so the caller falls back to expires_in_default.
func secondsAtPath(doc interface{}, path string) (int64, bool) {
	value, found := valueAtPath(doc, path)
	if !found {
		return 0, false
	}

	number, isNumber := value.(json.Number)
	if !isNumber {
		return 0, false
	}

	if seconds, err := strconv.ParseInt(number.String(), 10, 64); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		return seconds, true
	}

	// a whole-number float such as 3600.0 or 3.6e3
	f, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || f != math.Trunc(f) || f <= 0 || f >= math.MaxInt64 {
		return 0, false
	}
	return int64(f), true
}

// parseExpiresInSeconds reads the duration in seconds at field from a JSON body
func parseExpiresInSeconds(raw []byte, field string) (int64, bool) {
	doc, ok := decodeJSON(raw)
	if !ok {
		return 0, false
	}
	return secondsAtPath(doc, field)
}

// buildTokenRequest builds the token call. refreshToken is the current refresh token, which may have been rotated
// since the config was loaded.
func buildTokenRequest(opts *tokenRefreshOptions, refreshToken string) (*http.Request, error) {
	var body string
	if opts.Type == tokenRefreshTypeRefresh {
		form := url.Values{}
		form.Set(formFieldGrantType, refreshTokenGrantType)
		form.Set(formFieldRefreshToken, refreshToken)
		if opts.ClientID != "" {
			form.Set(formFieldClientID, opts.ClientID)
		}
		if opts.ClientSecret != "" {
			form.Set(formFieldClientSecret, opts.ClientSecret)
		}
		body = form.Encode()
	} else {
		body = opts.Config.Body
	}

	req, err := http.NewRequest(opts.Config.Method, opts.Config.URL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	for name, value := range opts.Config.Headers {
		req.Header.Set(name, value)
	}

	// Header.Get is case-insensitive, so a content-type set by the operator in any letter case is kept
	if opts.Type == tokenRefreshTypeRefresh && req.Header.Get(headerContentType) == "" {
		req.Header.Set(headerContentType, contentTypeMappings[contentURLEncoded])
	}

	return req, nil
}

// requestAccessToken calls the token endpoint. It always returns a body-less Token Refresh channel log, even on
// failure. A nil result comes with a *tokenRefreshError whose text is a fixed reason.
func requestAccessToken(channel courier.Channel, msgID courier.MsgID, opts *tokenRefreshOptions, refreshToken string) (*tokenResult, *courier.ChannelLog, error) {
	logURL := tokenLogURL(opts.Config.URL)

	fail := func(statusCode int, elapsed time.Duration, reason string) (*tokenResult, *courier.ChannelLog, error) {
		err := &tokenRefreshError{Reason: reason}
		log := courier.NewChannelLog(tokenRefreshLogDescription, channel, msgID, opts.Config.Method, logURL, statusCode, "", "", elapsed, errors.New(reason))
		return nil, log, err
	}

	req, err := buildTokenRequest(opts, refreshToken)
	if err != nil {
		// the request never left: reported as a connection failure, because the Go error would echo the URL
		return fail(0, 0, tokenFailureConnection)
	}

	// classify from the RequestResponse and not from err: MakeHTTPRequest also returns an error for a final 3xx,
	// which the spec treats as a success (research R4)
	rr, _ := utils.MakeHTTPRequest(req)
	if rr == nil || rr.StatusCode == 0 {
		elapsed := time.Duration(0)
		if rr != nil {
			elapsed = rr.Elapsed
		}
		return fail(0, elapsed, tokenFailureConnection)
	}

	// the body of an error response is never read
	if rr.StatusCode >= http.StatusInternalServerError {
		return fail(rr.StatusCode, rr.Elapsed, tokenFailureStatus5xx)
	}
	if rr.StatusCode >= http.StatusBadRequest {
		return fail(rr.StatusCode, rr.Elapsed, tokenFailureStatus4xx)
	}

	doc, ok := decodeJSON(rr.Body)
	if !ok {
		return fail(rr.StatusCode, rr.Elapsed, tokenFailureInvalidJSON)
	}

	accessToken, ok := scalarAtPath(doc, opts.Config.TokenField)
	if !ok {
		return fail(rr.StatusCode, rr.Elapsed, tokenFailureMissingField)
	}

	result := &tokenResult{AccessToken: accessToken}

	if opts.Config.RefreshTokenField != "" {
		// a missing or empty value leaves RefreshToken empty, which keeps the current refresh token
		result.RefreshToken, _ = scalarAtPath(doc, opts.Config.RefreshTokenField)
	}

	now := dates.Now().Unix()
	if seconds, ok := secondsAtPath(doc, opts.Config.ExpiresInField); ok {
		result.ExpiresAt = now + seconds
	} else if opts.Config.ExpiresInDefault > 0 {
		result.ExpiresAt = now + opts.Config.ExpiresInDefault
	}

	log := courier.NewChannelLog(tokenRefreshLogDescription, channel, msgID, opts.Config.Method, logURL, rr.StatusCode, "", "", rr.Elapsed, nil)
	return result, log, nil
}

// tokenLogURL drops the query string, fragment and user info from the token URL, so a credential placed in the URL
// is not stored in the channel log
func tokenLogURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
