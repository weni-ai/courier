# Data Model: ExternalV2 OAuth2 Access-Token Refresh

No table, column or index changes. The only persisted state is the channel config JSON in `channels_channel.config`.

## Channel config keys

All values are strings read with `StringConfigForKey`; a JSON number is treated as absent. Key names are constants in `token_refresh.go` (Principle XV).

| Key | Written by | Notes |
|-----|-----------|-------|
| `token_refresh_enabled` | operator | `true`, `1` or `yes`, case-insensitive after trim. Anything else or absent: feature off |
| `token_refresh_type` | operator | `custom` or `refresh`, required when enabled |
| `token_refresh_config` | operator | JSON object serialized as a string, required when enabled |
| `refresh_token` | operator, then Courier | rewritten when a refresh returns a rotated token |
| `expires_in` | operator, then Courier | unix expiry in seconds, decimal string; rewritten after a refresh when the new expiry is above zero |
| `client_id`, `client_secret` | operator | form body of `refresh` only, when non-empty |
| `send_authorization` | operator, then Courier | rewritten as `Bearer <access token>` after a refresh |

Every other key (`send_url`, `send_template`, `mt_response_check`, `content_type`, ...) is preserved: Courier writes the three keys above into a clone of `channel.Config()` and saves the clone, because `UpdateChannelConfig` replaces the whole map (research R1). The shared `channel.Config()` map is never written.

## `tokenRefreshConfig` (parsed from `token_refresh_config`)

| Field | Type | Default after parsing |
|-------|------|-----------------------|
| `when.match` | string | `any` (`tokenRefreshMatchAny`); any other value is a parse error |
| `when.status_codes` | `[]int` | empty |
| `when.body_contains` | `[]string` | empty; empty entries are ignored at match time |
| `when.expired` | bool | false |
| `method` | string | `POST` (`defaultTokenMethod`) |
| `url` | string | required, trimmed |
| `headers` | `map[string]string` | empty |
| `body` | string | required for `custom` |
| `token_field` | string | `access_token` (`defaultTokenField`) |
| `refresh_token_field` | string | empty: the refresh token is not updated |
| `expires_in_field` | string | empty |
| `expires_in_default` | int64 | 0 |

## `tokenRefreshOptions` (immutable result of parsing, one per `SendMsg`)

`Type` (`custom` or `refresh`), `Config` (above), `RefreshToken`, `ClientID`, `ClientSecret` (trimmed strings), `ExpiresAt` (int64 from `expires_in`, 0 when absent).

Validation order (spec, Contracts): disabled returns `(nil, nil)`; type; non-empty `token_refresh_config`; `url`; `when.match`; `custom` needs `body`; `refresh` needs `refresh_token`; `expires_in` parses as base-10 int64.

## `tokenAuth` (mutable state of one `SendMsg`)

Owned by `SendMsg` and passed to every `sendMsgPart`. A `SendMsg` runs its parts one after another in a single goroutine, so it needs no lock.

| Field | Meaning |
|-------|---------|
| `options` | the parsed `tokenRefreshOptions` |
| `accessToken` | raw token: `send_authorization` trimmed, one leading case-sensitive `Bearer ` removed; replaced after a refresh |
| `refreshToken` | current refresh token; replaced when `refresh_token_field` is present in the response |
| `expiresAt` | unix expiry; replaced as `now + seconds`, else `now + expires_in_default`, else kept |
| `refreshedInThisSend` | set after a successful refresh; skips the proactive check for the remaining parts of the same `SendMsg` |

State transitions: `new` → (proactive refresh, or refresh after a match) → `refreshed`. Refresh on a `refreshed` state is allowed only by the replay path (cap 1 per part). A failed refresh leaves the state unchanged.

## `tokenResult` (output of one token call)

`accessToken`, `refreshToken` (empty when unchanged), `expiresAt` (0 when unchanged), plus the `Token Refresh` channel log for the attempt, returned on success and on failure.

## `Token Refresh` channel log

| Field | Value |
|-------|-------|
| `Description` | `Token Refresh` (`tokenRefreshLogDescription`) |
| `Method`, `StatusCode`, `Elapsed` | from the attempt (`StatusCode` 0 on a transport failure) |
| `URL` | token URL without query string and user info |
| `Request`, `Response` | empty |
| `Error` | empty on success; otherwise a fixed reason: `connection`, `status_4xx`, `status_5xx`, `invalid_json`, `missing_token_field` |

## Constants (Principle XV)

`maxTokenRefreshReplays = 1`, `bearerPrefix = "Bearer "`, `defaultTokenField = "access_token"`, `defaultTokenMethod = http.MethodPost`, `tokenRefreshMatchAny = "any"`, `tokenRefreshTypeCustom = "custom"`, `tokenRefreshTypeRefresh = "refresh"`, `refreshTokenGrantType = "refresh_token"`, `tokenRefreshLogDescription = "Token Refresh"`, `redactedHeaderValue = "[REDACTED]"`, the form field names `formFieldGrantType`, `formFieldRefreshToken`, `formFieldClientID`, `formFieldClientSecret`, the header names `headerAuthorization` and `headerContentType` (new code only), the fixed failure reasons `tokenFailureConnection`, `tokenFailureStatus4xx`, `tokenFailureStatus5xx`, `tokenFailureInvalidJSON`, `tokenFailureMissingField`, the log vocabulary `logComp`, `logEventTokenRefresh`, `logEventTokenRefreshSave`, `tokenTriggerExpired`, `tokenTriggerMatch`, `tokenOutcomeOK`, `tokenOutcomeFailed`, the variables `tokenRefreshEnabledValues` (`true`, `1`, `yes`) and `authorizationHeaderPattern` (the redaction regexp), `tokenRefreshReplayDelay = 0 * time.Second`, and one constant per config key. The status thresholds use `http.StatusBadRequest` and `http.StatusInternalServerError`; the token form `Content-Type` default reuses the existing `contentTypeMappings[contentURLEncoded]`.
