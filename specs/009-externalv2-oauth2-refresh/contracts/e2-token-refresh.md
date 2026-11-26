# Contract: E2 OAuth2 token refresh

SemVer impact: **MINOR**. Every key is additive and optional, and nothing is removed or renamed. A channel without `token_refresh_enabled` sends the same provider request as today. The stored partner send log changes for every E2 channel (see Logs).

## Channel config (input)

See [data-model.md](../data-model.md) for the keys, defaults and validation order. Example (`custom`), values are fake:

```json
{
  "token_refresh_enabled": "true",
  "token_refresh_type": "custom",
  "token_refresh_config": "{\"when\":{\"match\":\"any\",\"status_codes\":[401,403],\"body_contains\":[\"token_expired\"],\"expired\":true},\"method\":\"POST\",\"url\":\"https://auth.example.invalid/oauth/token\",\"headers\":{\"Content-Type\":\"application/x-www-form-urlencoded\"},\"body\":\"grant_type=password&client_id=abc&client_secret=fake-client-secret\",\"token_field\":\"access_token\",\"refresh_token_field\":\"refresh_token\",\"expires_in_field\":\"expires_in\",\"expires_in_default\":7200}",
  "send_authorization": "Bearer current-access-token",
  "expires_in": "1731500000"
}
```

Example (`refresh`):

```json
{
  "token_refresh_enabled": "true",
  "token_refresh_type": "refresh",
  "token_refresh_config": "{\"when\":{\"match\":\"any\",\"status_codes\":[401],\"expired\":true},\"url\":\"https://auth.example.invalid/oauth/token\",\"token_field\":\"access_token\",\"expires_in_field\":\"expires_in\"}",
  "refresh_token": "fake-refresh-token",
  "client_id": "abc",
  "client_secret": "fake-client-secret",
  "send_authorization": "Bearer current-access-token"
}
```

## Token request (new outbound call, opted-in channels only)

| Type | Method | URL | Headers | Body |
|------|--------|-----|---------|------|
| `custom` | `method` (default `POST`) | `url` | `headers` as configured | `body` as configured |
| `refresh` | `method` (default `POST`) | `url` | `headers`; `Content-Type: application/x-www-form-urlencoded` when none is set (case-insensitive) | form: `grant_type=refresh_token`, `refresh_token`, `client_id` and `client_secret` when non-empty |

Sent through `utils.MakeHTTPRequest` (shared client, 60s timeout). The token URL, headers and body are operator-set, at the same trust level as `send_url`; no URL validation is added.

## Token response (input from the partner)

| Case | Result |
|------|--------|
| status 400 or higher, transport failure | refresh fails; the body is never read |
| JSON body without a scalar at `token_field` | refresh fails (`missing_token_field`) |
| JSON body, `token_field` present | new access token |
| `refresh_token_field` present | refresh token replaced; absent or empty: kept |
| `expires_in_field` a JSON number that is a positive whole number of seconds (a JSON string, a fraction, zero or a negative value counts as absent) | expiry = now + seconds |
| otherwise, `expires_in_default` > 0 | expiry = now + `expires_in_default` |
| otherwise | previous expiry kept |

Field paths are dotted object paths; strings, numbers and booleans are accepted; an empty string, `null`, object or array is a miss.

## Partner request (changed for opted-in channels only)

`Authorization: Bearer <raw access token>`, always set, including when the raw token is empty. The raw token is `send_authorization` trimmed with one leading case-sensitive `Bearer ` removed; any other value (for example `Basic ...`) is the raw token and is sent as `Bearer Basic ...`.

Replay (spec, Q3 option A): at most once per part, with a freshly rebuilt request, when the response status is in `when.status_codes`, or the response is non-2xx and its body contains a non-empty `when.body_contains` entry. A nil response (connection failure) never matches. A second match does not refresh again.

## Persisted config (output)

After a successful token call, `Backend().UpdateChannelConfig(ctx, channel, clone)` with the clone of `channel.Config()` plus:

| Key | Value |
|-----|-------|
| `send_authorization` | `Bearer <new access token>` |
| `refresh_token` | the new refresh token, only when non-empty |
| `expires_in` | the new unix expiry as a decimal string, only when above zero |

All other keys unchanged. A failed save is logged and the send continues with the in-memory token.

## Logs

| Log | Content |
|-----|---------|
| `Token Refresh` channel log, one per token attempt | method, URL (no query string, no user info), status code, elapsed; empty request and response; fixed reason as error on failure |
| E2 partner send channel log (all E2 channels) | unchanged except `Authorization: [REDACTED]` in the request dump (Q2, option A) |
| Application log | logrus fields `comp`, `channel_uuid`, `msg_uuid`, `event`, `trigger`, `outcome`, `reason`, `status_code`, `elapsed`; no URN path, no message text, no bodies, no credentials |

## Unchanged

`/c/e2/<uuid>/receive`, `sent`, `delivered`, `failed` and `stopped` routes and their handlers are not modified.
