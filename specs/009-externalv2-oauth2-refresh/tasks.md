---

description: "Task list for ExternalV2 OAuth2 access-token refresh"
---

# Tasks: ExternalV2 OAuth2 Access-Token Refresh

**Input**: Design documents from `/specs/009-externalv2-oauth2-refresh/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/e2-token-refresh.md](./contracts/e2-token-refresh.md), [quickstart.md](./quickstart.md)

**Tests**: requested (tests before implementation in every phase).

**Organization**: the spec has acceptance criteria (AC-01 to AC-08), not user stories, and the request fixed six phases. Tasks follow those phases. Each task names the acceptance criterion it serves.

## Format: `- [ ] T### [P?] Description with file path` then a `Commit:` line

- **[P]**: can run in parallel (different files, no dependency on an incomplete task).
- **Commit:** the Conventional Commit subject for the task (Principle VII: type in `feat`, `fix`, `docs`, `refactor`, `test`, `chore`; imperative description of at most 50 characters; one logical change). A task marked **no commit** only verifies.
- All paths are relative to the repository root `courier/`.

## Scope guard (Principle XVI)

Only these paths may change: `handlers/externalv2/token_refresh.go`, `handlers/externalv2/token_refresh_test.go`, `handlers/externalv2/externalv2.go`, `handlers/externalv2/externalv2_test.go`, `handlers/externalv2/doc.md`, `WENI-CHANGELOG.md`. No task touches Flows, Mailroom, other handlers, `utils/`, `backend.go`, `backends/`, `channel_log.go` or `test.go`.

## Names the tasks rely on (from plan, research and data-model)

- `parseTokenRefreshOptions(channel courier.Channel) (*tokenRefreshOptions, error)`
- `(*tokenRefreshOptions).matches(rr *utils.RequestResponse) bool`
- `lookupJSONPath(raw []byte, path string) (string, bool)`, `parseExpiresInSeconds(raw []byte, field string) (int64, bool)`
- `buildTokenRequest(opts *tokenRefreshOptions, refreshToken string) (*http.Request, error)`
- `requestAccessToken(channel courier.Channel, msgID courier.MsgID, opts *tokenRefreshOptions, refreshToken string) (*tokenResult, *courier.ChannelLog, error)` (`refreshToken` is the current one to send; the result carries only what changed, with an empty `refreshToken` and `expiresAt` 0 meaning "unchanged")
- `tokenResult{AccessToken, RefreshToken string; ExpiresAt int64}`
- `tokenAuth` (state of one `SendMsg`, in `externalv2.go`), `redactAuthorizationHeader(dump string) string`
- Error texts (exact), in rule order: `token_refresh_type must be custom or refresh`; `missing token_refresh_config in E2 channel config`; `invalid token_refresh_config: <json error>`; `token_refresh_config.url is required`; `token_refresh_config.when.match must be any`; `token_refresh_config.body is required when token_refresh_type is custom`; `refresh_token is required when token_refresh_type is refresh`; `invalid expires_in: <parse error>`.
- Failure reasons (fixed strings, never a raw error): `connection`, `status_4xx`, `status_5xx`, `invalid_json`, `missing_token_field`.
- Fixtures use fake values only: `test-access-token-1`, `test-access-token-2`, `fake-refresh-token`, `fake-client-secret`, token host `auth.example.invalid` where the URL is never dialled.

---

## Phase 1: Parser (`token_refresh_test.go`, then `token_refresh.go`)

**Purpose**: option parsing, the `when` matcher, path lookup and expiry parsing, usable alone. Nothing in the send flow calls them yet.

**Checkpoint**: `go test ./handlers/externalv2/ -run 'TestParseTokenRefresh|TestTokenRefreshWhen|TestLookupJSONPath|TestParseExpiresInSeconds'` is green and the existing tests still pass.

- [X] T001 Create the feature branch `009-externalv2-oauth2-refresh` from `main` (the repository is on `main` today, and the T046 and T049 diffs against `main` need a branch), then run `go test -p=1 ./handlers/externalv2/... -count=1` and record that it is green before any change.
  Commit: **no commit**

- [X] T002 Add `TestParseTokenRefreshDisabled` in `handlers/externalv2/token_refresh_test.go`: key absent, `""`, `"false"`, `"no"`, `"0"`, `"enabled"`, and a non-string config value each return `(nil, nil)` using a `courier.NewMockChannel` config. (AC-01)
  Commit: `test: cover disabled E2 token refresh parse`

- [X] T003 Add `TestParseTokenRefreshCustom` in `handlers/externalv2/token_refresh_test.go`: a valid `custom` config parses `url` (trimmed), `body`, `headers`, `when.status_codes`, `when.body_contains`, `when.expired`, `refresh_token_field`, `expires_in_field`, `expires_in_default`; defaults apply: method `POST`, `token_field` `access_token`, `when.match` `any`; `true`, `TRUE`, `1`, `yes`, ` Yes ` (after trim) all enable it; `token_refresh_type` ` CUSTOM ` and `when.match` ` Any ` are accepted (case-insensitive after trim, as Mailroom). (AC-02, AC-03)
  Commit: `test: cover valid custom token refresh config`

- [X] T004 Add `TestParseTokenRefreshInvalid` in `handlers/externalv2/token_refresh_test.go`: table of the invalid cases with the exact error texts listed above: missing type, type other than `custom`/`refresh`, empty `token_refresh_config`, invalid JSON, missing `url`, `when.match` other than `any`, `custom` without `body`. Also assert the rule order: a bad type with an empty config reports the type error first; a missing `url` with a bad `when.match` reports `url` first. (AC-06)
  Commit: `test: cover invalid token refresh configs`

- [X] T005 Add `TestParseTokenRefreshRefreshType` in `handlers/externalv2/token_refresh_test.go`: `refresh` without `refresh_token` returns the exact error; with it, options carry the trimmed `refresh_token`, `client_id`, `client_secret`; `body` is ignored and not required. (AC-02, AC-06)
  Commit: `test: cover refresh type token options`

- [X] T006 Add `TestParseTokenRefreshExpiresIn` in `handlers/externalv2/token_refresh_test.go`: `expires_in` `"1731500000"` parses as the unix expiry (not a duration), surrounding spaces are trimmed, empty gives 0, `"abc"` returns `invalid expires_in: ...`, and a JSON number (non-string value) is treated as absent. (AC-02)
  Commit: `test: cover expires_in as unix expiry`

- [X] T007 Add `TestTokenRefreshWhen` in `handlers/externalv2/token_refresh_test.go` for the spec's Q3 rule: status in `status_codes` matches on any status (including a listed 2xx); a `body_contains` needle matches only when the response is non-2xx; a 2xx body with the needle does not match; empty needles are ignored; a nil `RequestResponse` and a connection failure (`StatusCode` 0) never match; a non-2xx `RequestResponse` that also carries an error still matches. (AC-03)
  Commit: `test: cover token refresh when matcher`

- [X] T008 Add `TestLookupJSONPath` in `handlers/externalv2/token_refresh_test.go`: top-level and dotted hits; string, whole number (`3600`), large integer without exponent, float, and boolean values return their text; a missing key, an empty string, `null`, an object, an array and a non-JSON body are misses; the walk goes through objects only (`items.0` is a miss). (AC-02)
  Commit: `test: cover token JSON path lookup`

- [X] T009 Add `TestParseExpiresInSeconds` in `handlers/externalv2/token_refresh_test.go`: integer and whole-number float (`3600.0`) accepted; a JSON string, even a numeric one (`"3600"`), is rejected, as is a fractional value (`3600.5`), zero and a negative value; an empty field name, a missing field and a non-numeric string are rejected. This is stricter than Mailroom, which accepts a numeric string and truncates a fraction (research R3, spec Contracts). A rejected value means "absent", so `expires_in_default` applies. (AC-02)
  Commit: `test: cover expires_in seconds parsing`

- [X] T010 Create `handlers/externalv2/token_refresh.go` with the constants (`maxTokenRefreshReplays = 1`, `tokenRefreshReplayDelay = 0 * time.Second` (the named backoff strategy, Principle XI: zero on purpose, because the replay carries a new credential and waiting cannot fix a stale one; add a why-comment on both constants), `bearerPrefix = "Bearer "`, `defaultTokenField = "access_token"`, `defaultTokenMethod = http.MethodPost`, `tokenRefreshMatchAny`, `tokenRefreshTypeCustom`, `tokenRefreshTypeRefresh`, `refreshTokenGrantType`, `tokenRefreshLogDescription`, one constant per config key, the form field names of the refresh request `formFieldGrantType = "grant_type"`, `formFieldRefreshToken = "refresh_token"`, `formFieldClientID = "client_id"`, `formFieldClientSecret = "client_secret"`, the header names `headerAuthorization = "Authorization"` and `headerContentType = "Content-Type"` for new code only, and `tokenRefreshEnabledValues = []string{"true", "1", "yes"}` as a package `var` because a slice cannot be a `const`), the types `tokenRefreshWhen`, `tokenRefreshConfig`, `tokenRefreshOptions` (JSON tags and defaults exactly as in data-model.md), and `parseTokenRefreshOptions` with the 8 ordered rules and the exact error texts. Makes T002 to T006 pass. (AC-01, AC-02, AC-06)
  Commit: `feat: parse E2 token refresh options`

- [X] T011 Add `(*tokenRefreshOptions).matches` in `handlers/externalv2/token_refresh.go` per research R3 and Q3: status in `status_codes`, or non-2xx and body contains a non-empty entry; nil response never matches. Makes T007 pass. (AC-03)
  Commit: `feat: match token refresh when rules`

- [X] T012 Add `lookupJSONPath` (decode with `encoding/json` and `UseNumber`, walk objects only, scalars only) and `parseExpiresInSeconds` (whole positive seconds only; it must see the JSON type, so it accepts a JSON number and rejects a JSON string, which means it walks the decoded value itself or the lookup reports the kind) in `handlers/externalv2/token_refresh.go`, with a why-comment on each stricter rule. Makes T008 and T009 pass. (AC-02)
  Commit: `feat: read token fields and expiry seconds`

---

## Phase 2: Token call (`token_refresh_test.go`, then `token_refresh.go`)

**Purpose**: the outbound token request, the response handling and the body-less `Token Refresh` channel log, exercised against an `httptest` token server.

**Checkpoint**: `go test ./handlers/externalv2/ -run 'TestBuildTokenRequest|TestRequestAccessToken'` is green.

- [X] T013 Add `TestBuildTokenRequestRefresh` in `handlers/externalv2/token_refresh_test.go`: for type `refresh` the request is `POST` to `url` with form fields `grant_type=refresh_token`, `refresh_token`, `client_id`, `client_secret`; `client_id` and `client_secret` are omitted when empty; `Content-Type: application/x-www-form-urlencoded` is set when no `Content-Type` header is configured, and an operator-set `content-type` in any letter case is kept without a second header. (AC-02)
  Commit: `test: cover refresh token request form body`

- [X] T014 Add `TestBuildTokenRequestCustom` in `handlers/externalv2/token_refresh_test.go`: type `custom` sends the configured method (default `POST`), `url`, every configured header and the literal `body` unchanged. (AC-02)
  Commit: `test: cover custom token request`

- [X] T015 Add `TestRequestAccessTokenFields` in `handlers/externalv2/token_refresh_test.go` against an `httptest` token server: default `access_token`; a dotted `token_field` such as `data.token`; `refresh_token_field` present gives a non-empty `refreshToken` in the result; absent or empty gives an empty `refreshToken` in the result, which means "unchanged" (the caller keeps the old one). (AC-02, AC-04)
  Commit: `test: cover token response field lookup`

- [X] T016 Add `TestRequestAccessTokenExpiry` in `handlers/externalv2/token_refresh_test.go`, with `dates.SetNowSource(dates.NewFixedNowSource(...))` reset in a `defer`: `expires_in_field` of N seconds gives `now + N`; `expires_in_default` is used when the field is absent or not a positive number; with neither, `expiresAt` is 0 in the result, which means "unchanged" (the caller keeps the previous expiry). (AC-02, AC-04)
  Commit: `test: cover token expiry computation`

- [X] T017 Add `TestRequestAccessTokenFailures` in `handlers/externalv2/token_refresh_test.go`: status 400, 401 and 500 fail with reasons `status_4xx` / `status_5xx` and an error body that contains `access_token` is never used; a 200 without `token_field`, a 200 with an empty `token_field` and a non-scalar value fail with `missing_token_field`; a non-JSON 200 fails with `invalid_json`; a closed server fails with `connection`; a final 3xx status below 400 is parsed like a success (research R4). Each failure returns a nil result and a non-nil channel log. (AC-06)
  Commit: `test: cover failed token requests`

- [X] T018 Add `TestRequestAccessTokenLog` in `handlers/externalv2/token_refresh_test.go`: on success and on each failure the returned channel log has description `Token Refresh`, the method, the token URL without query string and without user info (use a URL with `?api_key=fake-key` and `user:fake-pass@`), the status code (0 on a connection failure), elapsed, empty `Request` and `Response`, and an `Error` that is empty on success and one of the fixed reasons otherwise; no field contains `fake-client-secret`, `fake-refresh-token` or `test-access-token-1`. (AC-07)
  Commit: `test: cover Token Refresh channel log`

- [X] T019 Add `buildTokenRequest` in `handlers/externalv2/token_refresh.go`: `custom` and `refresh` bodies and headers as in contracts/e2-token-refresh.md, `Content-Type` default checked with `req.Header.Get`. Build the `refresh` form with the `formField*` constants and take the default `Content-Type` from the existing `contentTypeMappings[contentURLEncoded]` (do not add a new constant for `application/x-www-form-urlencoded`). Makes T013 and T014 pass. (AC-02)
  Commit: `feat: build E2 token refresh requests`

- [X] T020 Add `tokenResult` and `requestAccessToken` in `handlers/externalv2/token_refresh.go`: call `utils.MakeHTTPRequest`, classify from the `RequestResponse` (transport failure, status >= `http.StatusBadRequest`, JSON, `token_field`; split 4xx from 5xx at `http.StatusInternalServerError`, with no numeric literal), read the refresh token and compute the expiry with `dates.Now()`. Declare the fixed failure reasons as constants: `tokenFailureConnection = "connection"`, `tokenFailureStatus4xx = "status_4xx"`, `tokenFailureStatus5xx = "status_5xx"`, `tokenFailureInvalidJSON = "invalid_json"`, `tokenFailureMissingField = "missing_token_field"`; they feed the token log `Error` and the application-log `reason`, so one definition keeps them in step. Never read the body of an error response. Makes T015, T016 and T017 pass. (AC-02, AC-04, AC-06)
  Commit: `feat: request and read E2 access tokens`

- [X] T021 In `handlers/externalv2/token_refresh.go`, build the `Token Refresh` log with `courier.NewChannelLog` (not `NewChannelLogFromRR`): empty request and response, URL via a helper that drops query string and user info, fixed reason as the error; return it from `requestAccessToken` for every attempt. Makes T018 pass. (AC-07)
  Commit: `feat: add body-less Token Refresh channel log`

---

## Phase 3: Send flow (`externalv2_test.go`, then `externalv2.go`)

**Purpose**: one auth state per `SendMsg`, Bearer header, proactive refresh, single replay from a body snapshot, shared state across parts, invalid config error. The partner send log is not redacted yet (Phase 5), so the feature is not releasable until Phase 5 is done.

**Checkpoint**: `go test ./handlers/externalv2/ -run 'TestSending'` is green, including the unchanged existing tests.

- [X] T022 Run `go test -p=1 ./handlers/externalv2/... -count=1` and confirm the existing tests (`TestSendingWithAuth`, `TestSendingInParts`, `TestSimpleSend`, `TestSendingWithCustomURL`, `TestHandler*`) are green before touching `externalv2.go`.
  Commit: **no commit**

- [X] T023 Add `TestSendingTokenRefreshDisabled` in `handlers/externalv2/externalv2_test.go` through `RunChannelSendTestCases`: channels with `token_refresh_enabled` unset, `"false"` and `"no"`, a past `expires_in` and a valid-looking `token_refresh_config` whose `url` is a closed port; each expects status `W`, `Authorization` equal to the raw `send_authorization` and `Path` on the partner path. (AC-01)
  Commit: `test: cover E2 send with refresh disabled`

- [X] T024 Add `TestSendingTokenRefreshExpired` in `handlers/externalv2/externalv2_test.go` through `RunChannelSendTestCases`, one case for `custom` and one for `refresh`: `Responses` holds a token entry and a partner entry on the same server, `expires_in` is in the past and `when.expired` is true; status `W`, `Headers` `Authorization: Bearer test-access-token-2`, and both entries matched exactly once (`mockRRCount == len(Responses)`). Use its own channel per run. (AC-02)
  Commit: `test: cover proactive E2 token refresh`

- [X] T025 Add `TestSendingTokenRefreshTokenFailure` in `handlers/externalv2/externalv2_test.go` through `RunChannelSendTestCases`: token 400, token 500, 200 without `token_field`, and an invalid JSON token body, each with an expired token; `Responses` holds only the token entry and `Path` is the token path, so the partner is not called; status `E` and no returned error. (AC-06)
  Commit: `test: cover token failure before partner call`

- [X] T026 Add `TestSendingTokenRefreshInvalidConfig` in `handlers/externalv2/externalv2_test.go` through `RunChannelSendTestCases`: each invalid enabled config from T004 makes `SendMsg` return the exact error and no status; `Responses` is empty and no partner request is made. (AC-06)
  Commit: `test: cover invalid enabled config on send`

- [X] T027 Add `TestSendingTokenRefreshReplay` in `handlers/externalv2/externalv2_test.go`: direct `SendMsg` with a stateful `httptest` server and per-path counters (the harness `Responses` map cannot answer one request shape twice, so it cannot express this sequence): partner 401 then token then partner 200 gives status `W`, one token call, and the second partner request carries `Bearer test-access-token-2`; two consecutive 401 give status `E` and still one token call. Use a normal context timeout. (AC-03)
  Commit: `test: cover single replay after 401`

- [X] T028 Add `TestSendingTokenRefreshReplayBody` in `handlers/externalv2/externalv2_test.go`, same direct style: a 400 whose body has a `body_contains` needle is replayed once and ends `W` with one token call; a 200 with the needle is not replayed, makes no token call and ends `W`; a status listed in `status_codes` replays whatever the body says. (AC-03, spec Q3)
  Commit: `test: cover body_contains replay policy`

- [X] T029 Add `TestSendingTokenRefreshConnectionError` in `handlers/externalv2/externalv2_test.go`, direct style: a closed partner server gives status `E`, a nil error and zero calls on a counting token server. (AC-06)
  Commit: `test: cover connection error without refresh`

- [X] T030 Add `TestSendingTokenRefreshReplayFailure` in `handlers/externalv2/externalv2_test.go`, direct style: partner 401, token call fails, status `E`, the partner is called once; and partner 401 always with an expired token gives exactly 2 token calls and 2 partner calls (one proactive, one after a match) and status `E`, documenting the per-part bound. (AC-03, AC-06)
  Commit: `test: cover refresh failure and call bound`

- [X] T030a Add `TestSendingTokenRefreshPartnerServerError` in `handlers/externalv2/externalv2_test.go` through `RunChannelSendTestCases`: refresh enabled with `when.status_codes` `[401]` (and no `body_contains`) and a future `expires_in`, so no proactive refresh; `Responses` holds only the partner entry returning 500; status `E`, `Authorization` equal to the existing Bearer and `Path` equal to the partner path (a token call after the 500 would make the token path the last one and leave an unmatched request). Second case: a 500 whose body contains the `body_contains` needle is replayed once (Q3, non-2xx), so it needs the direct style of T028; add it there if the harness cannot express it. (AC-03, AC-06, Principle XIV)
  Commit: `test: cover partner 5xx without refresh`

- [X] T031 Add `TestSendingTokenRefreshInParts` in `handlers/externalv2/externalv2_test.go` through `RunChannelSendTestCases`: expired token, token response without expiry information, `send_attachment_in_parts` true, two attachments plus text; one token entry and three partner entries each matched exactly once, so no part refreshed again. (AC-05)
  Commit: `test: cover shared token across E2 parts`

- [X] T032 Add `TestSendingTokenRefreshPartsReplay` in `handlers/externalv2/externalv2_test.go`, direct style: part 1 gets 401, refreshes and its replay returns 200; part 2 sends `Bearer test-access-token-2` with no further token call; the text part does the same; the status is `W`. (AC-05)
  Commit: `test: cover refresh in first attachment part`

- [X] T033 In `handlers/externalv2/externalv2.go`, parse the options once in `SendMsg` with `parseTokenRefreshOptions`; return `(nil, err)` for an invalid enabled config before any partner call; build one `tokenAuth` (options, raw access token from `send_authorization` trimmed with one case-sensitive `Bearer ` removed, refresh token, expiry, `refreshedInThisSend`); pass it to every `sendMsgPart` call. Disabled keeps `auth == nil` and today's lines. Makes T026 pass and keeps T023 green. (AC-01, AC-05, AC-06)
  Commit: `feat: build E2 auth state once per send`

- [X] T034 In `handlers/externalv2/externalv2.go`, turn the request construction in `sendMsgPart` into a closure `newRequest(authorization string)` that reuses the already rendered body (snapshot) and the unchanged `Content-Type` logic; set `Authorization` from `send_authorization` only when non-empty on the disabled path, and always as `bearerPrefix + raw token` when `auth != nil`. No behaviour change for existing channels: T022 tests stay green. (AC-01, AC-02)
  Commit: `refactor: rebuild E2 request from body snapshot`

- [X] T035 In `handlers/externalv2/externalv2.go`, add a handler method that refreshes (calls `requestAccessToken`, adds the `Token Refresh` log to the part's `MsgStatus`, updates `tokenAuth`, sets `refreshedInThisSend`) and use it for the proactive step: when `when.expired` is true, the expiry is above zero, `dates.Now().Unix() >= expiry` and no refresh happened yet in this `SendMsg`, refresh before the partner call; on failure return the errored status and the error without calling the partner. Makes T024, T025 and T031 pass. (AC-02, AC-05, AC-06)
  Commit: `feat: refresh E2 token before send when expired`

- [X] T036 In `handlers/externalv2/externalv2.go`, after `MakeHTTPRequest` and before `mt_response_check`, evaluate `auth.options.matches(rr)` (even when the call returned an error); on a match refresh once and send the part again with `newRequest`, capped by `maxTokenRefreshReplays` and preceded by `time.Sleep(tokenRefreshReplayDelay)` (zero, so it returns at once; it keeps the backoff strategy a visible, named knob); a second match returns the errored result without another token call; a failed refresh returns the error of the original partner response. Add a why-comment on the cap. Makes T027 to T030 and T032 pass. (AC-03, AC-06)
  Commit: `feat: replay E2 send once after token refresh`

---

## Phase 4: Persistence (`externalv2_test.go`, then `externalv2.go`)

**Purpose**: write the refreshed values into the channel config without losing any other key.

**Checkpoint**: `go test ./handlers/externalv2/ -run 'TestSendingTokenRefresh'` is green.

- [X] T037 Add `TestSendingTokenRefreshPersists` in `handlers/externalv2/externalv2_test.go`: a one-case `RunChannelSendTestCases` run on its own channel, then assertions on `channel.Config()`: `send_authorization == "Bearer test-access-token-2"`; `refresh_token` and `expires_in` (decimal string) present when the token response carries them; `refresh_token` and `expires_in` unchanged when it does not; unrelated keys (`send_url`, `send_template`, `mt_response_check`) unchanged; and the map captured before the run still holds the old `send_authorization`, which proves the clone. (AC-04)
  Commit: `test: cover refreshed config persistence`

- [X] T038 Add `TestSendingTokenRefreshSaveFailure` in `handlers/externalv2/externalv2_test.go`: a small type embedding `*courier.MockBackend` that overrides `UpdateChannelConfig` to fail and to count calls, run through `handlers.NewTestServer` and the handler's `Initialize`; status stays `W`, the partner request carries the new Bearer, and the `logrus` test hook (`github.com/sirupsen/logrus/hooks/test`) sees one error entry with `event=token_refresh_save`, `channel_uuid` and `msg_uuid` and none of the fixture secrets. Also assert exactly one save call per successful token call. (AC-04, AC-07)
  Commit: `test: cover token save failure`

- [X] T039 In `handlers/externalv2/externalv2.go`, add `cloneChannelConfig` (shallow copy of `channel.Config()`) and `persistRefreshedToken`: set `send_authorization` to `bearerPrefix + accessToken`, `refresh_token` only when `tokenResult.refreshToken` is non-empty, `expires_in` only when `tokenResult.expiresAt` is above zero as a base-10 string (empty and 0 mean "unchanged", so unchanged values are not rewritten); save with `Backend().UpdateChannelConfig(ctx, channel, clone)` after each successful token call, never mutating `channel.Config()`. Add a why-comment on the clone. Makes T037 pass. (AC-04)
  Commit: `feat: persist refreshed E2 token in config`

- [X] T040 In `handlers/externalv2/externalv2.go`, handle a failed save: log at error level with logrus fields `comp` (constant `logComp = "externalv2"`), `event` (constant `logEventTokenRefreshSave = "token_refresh_save"`), `channel_uuid`, `msg_uuid` and the backend error, then continue the send with the in-memory token. Keep error level on purpose and say why in a comment: the Sentry hook must report it, because a rotated refresh token that was not saved is lost and breaks later refreshes (spec XIII row; no contact UUID, as in specs 005 and 008). Makes T038 pass. (AC-04, AC-07)
  Commit: `feat: log and continue on E2 token save error`

---

## Phase 5: Redaction, logging and constants (Principles III and XV)

**Purpose**: spec Q2 (redact `Authorization` in every E2 partner send log), structured application logs for token attempts, and named constants.

**Checkpoint**: `go test ./handlers/externalv2/...` is green; no token, secret or `Authorization` value is found in any log the tests inspect.

- [X] T041 Add `TestRedactAuthorizationHeader` in `handlers/externalv2/externalv2_test.go`: header name in any letter case, `Bearer` and non-`Bearer` values, CRLF line endings and every other line preserved, only the `Authorization` line changed, a dump without the header returned unchanged, an empty string returned unchanged. (AC-07)
  Commit: `test: cover Authorization header redaction`

- [X] T042 Add `TestSendingTokenRefreshSendLogs` in `handlers/externalv2/externalv2_test.go`, direct style: every partner send log in `status.Logs()` shows `Authorization: [REDACTED]` and none of `test-access-token-1`, `test-access-token-2`, `fake-refresh-token`, `fake-client-secret`, for both attempts of a replayed part, for every attachment part, and for a channel with refresh disabled and a static `send_authorization`; the Token Refresh logs stay body-less. (AC-07)
  Commit: `test: cover redacted E2 partner send logs`

- [X] T043 In `handlers/externalv2/externalv2.go`, add `redactedHeaderValue = "[REDACTED]"`, the package-level regexp `authorizationHeaderPattern` (`regexp.MustCompile`, pattern from research R6) and `redactAuthorizationHeader` (case-insensitive header name, stops at the end of the line, per research R6) and build the partner send log from a copy of the `RequestResponse` whose `Request` is redacted, for every send including the disabled path. The request on the wire is unchanged. Makes T041 and T042 pass. (AC-07)
  Commit: `feat: redact Authorization in E2 send logs`

- [X] T044 Add `TestSendingTokenRefreshAppLogs` in `handlers/externalv2/externalv2_test.go` with the `logrus` test hook: every token attempt, successful or failed, produces an entry with `comp`, `channel_uuid`, `msg_uuid`, `event=token_refresh`, `trigger` (`expired` or `match`), `outcome`, `reason` (on failure), `status_code`, `elapsed`; no entry or message contains the URN path, the message text, a request or response body, or any fixture secret. (AC-07)
  Commit: `test: cover token refresh application logs`

- [X] T045 In `handlers/externalv2/externalv2.go`, log each token attempt with logrus fields only (never interpolated strings), at info level on success and warn level on failure, using the fields listed in T044. Build the fields in one helper `logTokenAttempt`, so each field key appears once; its values come from constants: `logEventTokenRefresh = "token_refresh"`, `tokenTriggerExpired = "expired"`, `tokenTriggerMatch = "match"`, `tokenOutcomeOK = "ok"`, `tokenOutcomeFailed = "failed"`, plus `logComp` and the `tokenFailure*` reasons. Makes T044 pass. (AC-07)
  Commit: `feat: log E2 token refresh attempts`

- [X] T046 Audit Principle XV in `handlers/externalv2/token_refresh.go` and `handlers/externalv2/externalv2.go`: list the quoted literals on lines this feature adds, with `git diff -U0 main -- 'handlers/externalv2/*.go' ':!*_test.go' | rg '^\+' | rg '"'` (the feature branch from T001 must exist: on `main` the diff is empty), and review each one by hand. Every literal that remains must be a constant or `var` declaration, an error message, a `logrus` field key inside `logTokenAttempt` or at the save-failure call, or an exempt value (the base `10` of `strconv.FormatInt`, index 0, increment 1). Meaningful literals (thresholds, enabled values, event/trigger/outcome/reason values, form field names, the `Authorization` regex, the header names in new code) must use their named constants; the existing literals at `externalv2.go` lines for `"Content-Type"` and `"Authorization"` in `sendMsgPart` stay as they are (Principle XVI). Also confirm `time.Sleep(` in the replay uses only `tokenRefreshReplayDelay`. This is a review aid, not a pass/fail script. Replace any stray literal with its named constant and make sure every comment explains why (credential replay, clone before save, shared state across parts, stricter than Mailroom) instead of restating the code. Skip the commit if nothing changes.
  Commit: `chore: name E2 token refresh literals`

---

## Phase 6: Docs and changelog

**Purpose**: operator documentation and the release entry. The two tasks touch different files and do not depend on each other.

- [X] T047 [P] Update `handlers/externalv2/doc.md` (Portuguese, as the rest of the file): add the new keys to the configuration tables, the two examples from contracts/e2-token-refresh.md, and the operator limits from the spec: `body_contains` matches only non-2xx responses; use `401` and `403` as `status_codes`, other statuses can duplicate a delivered message; set `expires_in_field` or `expires_in_default` or every message refreshes; last-write-wins across workers and refresh-token rotation risk; when the feature is on the header is always `Bearer <token>`; the `Authorization` value is redacted in E2 send logs; enable one pilot channel first and check that token calls stay at one per expiry window. Do not paste real credentials. (AC-01 to AC-07)
  Commit: `docs: document E2 OAuth2 token refresh`

- [X] T048 [P] Add the entry to `WENI-CHANGELOG.md` above `1.80.0`, in the same ` * feat: ...` style: opt-in OAuth2 token refresh for E2 channels (`token_refresh_enabled`), and `Authorization` redacted in E2 send logs. The PR description states the declared peak (242.9 sends/s, a service-wide proxy) and suggests a pilot channel first. Use `1.81.0` as the heading unless the release owner assigns another version: tags `1.81.0-a0-staging` and `1.81.1-a1-staging` already exist, and the heading must match the release tag (Principle VIII). Do not edit `CHANGELOG.md`.
  Commit: `docs: add E2 token refresh changelog entry`

---

## Final Phase: Verification

- [ ] T049 Run, in this order, and report the real output: `go test -p=1 ./handlers/externalv2/... -count=1`; then `go vet ./...`; then `gofmt -l handlers/externalv2` (must print nothing). Also run `git diff --stat main...HEAD` and confirm that the source and doc files it lists are only the six paths in the scope guard (`handlers/externalv2/doc.md`, `externalv2.go`, `externalv2_test.go`, `token_refresh.go`, `token_refresh_test.go`, `WENI-CHANGELOG.md`), plus the spec material under `specs/009-externalv2-oauth2-refresh/` and `.specify/feature.json`; and run the checks listed in [quickstart.md](./quickstart.md) (no secret reaches a logger or channel log). If `go vet ./...` reports a problem in a package outside `handlers/externalv2`, record it and do not fix it here (Principle XVI). Then do the pilot-channel manual check and record the observed token calls and config writes next to the declared peak in the PR description.
  Commit: **no commit**

---

## Dependencies and Execution Order

- Phase order is strict: 1 → 2 → 3 → 4 → 5 → 6 → verification. Inside a phase, a test task comes before the implementation that makes it pass.
- T010 needs T002 to T006; T011 needs T007; T012 needs T008 and T009.
- Phase 2 needs T010 to T012. T019 needs T013 and T014; T020 needs T015 to T017 and T019; T021 needs T018 and T020.
- Phase 3 needs Phase 2. T033 needs T026; T034 needs T033; T035 needs T024, T025, T031, T034; T036 needs T027 to T030, T030a, T032, T035.
- Phase 4 needs T036. T039 needs T037; T040 needs T038 and T039.
- Phase 5 needs Phase 4. T043 needs T041 and T042; T045 needs T044; T046 needs T043 and T045.
- Until T043 lands, E2 send logs of an opted-in channel still hold the plain `Authorization` value (Principle III). Phases 3 and 4 must therefore ship in the same PR as Phase 5, and no intermediate commit may be merged or deployed on its own.
- T047 needs the final behaviour (after T046); T048 needs the final behaviour too. Neither depends on the other.
- A test commit is red until its implementation task lands and a test file that names a not-yet-defined function does not compile until then. Land each test task and its implementation task in the same push (or squash them if a reviewer prefers); every pair is listed adjacent to its dependencies above.

## Parallel Opportunities

- Only T047 and T048 are marked `[P]`: they touch different files (`doc.md` and `WENI-CHANGELOG.md`) and neither depends on the other.
- Every other task edits `token_refresh_test.go`, `token_refresh.go`, `externalv2_test.go` or `externalv2.go`, which are shared by several tasks in sequence, or depends on an earlier task. They are not parallel.

## Acceptance Criteria Coverage

| Criterion | Tasks |
|-----------|-------|
| AC-01 disabled path | T002, T023, T033, T034, T047 |
| AC-02 proactive refresh | T003, T005 to T009, T012 to T016, T019, T020, T024, T034, T035 |
| AC-03 replay on a `when` match | T007, T011, T027, T028, T030, T030a, T036 |
| AC-04 persistence | T015, T016, T037 to T040 |
| AC-05 attachment parts | T031, T032, T033, T035 |
| AC-06 no stale token silently | T004, T005, T017, T025, T026, T029, T030, T030a, T033, T035, T036 |
| AC-07 no secrets in logs | T018, T021, T038, T040, T041 to T045, T047 |
| AC-08 other routes unchanged | T001, T022, T049 (existing receive, status and stop tests stay unmodified and green) |

## Implementation Strategy

1. Phases 1 and 2 are internal and change no behaviour: nothing calls the new code until Phase 3.
2. Phase 3 is the first behavioural change and only for channels that opt in. Phases 3 and 4 together make the feature work but leave the partner send log unredacted, so do not enable the feature on any channel before Phase 5 lands.
3. Phase 5 closes the privacy gap (spec Q2). After it, the work is releasable behind `token_refresh_enabled`.
4. Phase 6 and the final verification close the change. Release with one pilot channel enabled first as a precaution, then widen (the peak is declared in the spec, Q1).
