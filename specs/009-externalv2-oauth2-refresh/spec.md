# Engineering Specification: ExternalV2 OAuth2 Access-Token Refresh

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `[009-externalv2-oauth2-refresh]`

**Created**: 2026-11-26

**Status**: Draft (no open questions; peak load declared by reusing the Courier service-wide peak, 242.9 req/s)

**Engineering design (source of the decisions below)**: `external-oauth/docs/superpowers/specs/2026-11-13-externalv2-oauth2-design.md`. It is an approved engineering design, not a Product Spec.

## Origin Without Product Spec

- Product Spec: none
- Problem: ExternalV2 (`E2`) sends with a static `send_authorization` header, so partners that issue short-lived OAuth2 access tokens break when the token expires and an operator has to rotate it by hand. Mailroom already renews tokens for the generic ticketer (`mailroom/services/tickets/generic/token_refresh.go`); `E2` has no equivalent.
- Acceptance criteria:
  - **AC-01**: With `token_refresh_enabled` unset or false, the request `E2` sends to the partner is exactly as today and no token endpoint is ever called. The only difference is in the stored send log, which follows AC-07 (the `Authorization` value is redacted).
  - **AC-02**: When `expires_in` has passed and `when.expired` is true, the token endpoint is called before the partner, and the partner receives only the new Bearer.
  - **AC-03**: When the partner response status is in `when.status_codes`, or the response is non-2xx and its body matches `when.body_contains`, the token is renewed and that part is sent again once with the new Bearer. A second match does not trigger another token call. A 2xx response is never replayed because of `when.body_contains`, so a delivered message is not duplicated.
  - **AC-04**: After a successful refresh, `send_authorization` (as `Bearer <token>`), `refresh_token` and `expires_in` are persisted in the channel config through `Backend().UpdateChannelConfig`, and every other config key is kept.
  - **AC-05**: When `send_attachment_in_parts` is true, a refresh in one part is reused by the following parts without another token call.
  - **AC-06**: A failed token call, or an invalid enabled config, never reaches the partner with a stale token silently.
  - **AC-07**: Token request/response bodies, `client_secret`, `refresh_token` and `access_token` are never written to channel logs or application logs. This includes the partner send logs of `E2` channels, where the value of the `Authorization` header is stored as `[REDACTED]`.
  - **AC-08**: Receive, status and stop routes are unchanged.

## Clarifications

### Session 2026-11-26

- Q: When is the single replay of a partner request allowed after the token is renewed (Principle XI)? → A: Option A. Replay on `status_codes`; a `body_contains` match replays only when the partner response is non-2xx. No `Idempotency-Key`; the residual duplicate-delivery risk is limited to operator-listed `status_codes` and documented in `doc.md`.
- Q: How should live OAuth2 access tokens be kept out of the channel logs stored for E2 sends (Principle III)? → A: Option A. The E2 handler redacts the `Authorization` header value (`[REDACTED]`) in its own send logs, for every E2 send; `channel_log.go` is not touched and no constitution exception is needed.
- Q: What peak number of E2 outbound sends per second, over what time window, must the OAuth2 refresh path sustain (Principle XII)? → A: Declared by reuse of the recommended figure (revised on 2026-11-26; the earlier answer, "unknown" with a measurement gate, is withdrawn). The peak is the Courier service-wide peak of 242.9 req/s (1-minute rate, 2026-10-28 14:06 UTC, window 2026-10-07 to 2026-11-06), already declared by specs 003, 005, 006, 007 and 008. It is used as the worst-case ceiling for `E2` outbound sends per second, because Courier has no per-route or outbound series. The constitution is not changed.

## Scope in This Repository

**This repository implements**:

- `handlers/externalv2/token_refresh.go` (new): option parsing and validation, `when` matching, token request builders for `custom` and `refresh`, dotted-path JSON lookup, expiry computation, the `Token Refresh` channel log.
- `handlers/externalv2/externalv2.go`: one in-memory auth state per `SendMsg`, proactive refresh, Bearer header, single replay with a body snapshot, persistence through `Backend().UpdateChannelConfig`.
- `handlers/externalv2/doc.md`: the new keys and the two design examples.
- Tests in `handlers/externalv2/token_refresh_test.go` and `handlers/externalv2/externalv2_test.go`.
- A `WENI-CHANGELOG.md` entry.

**Out of scope here** (and nobody else owns it in this change): Flows channel form or any UI; Mailroom; any other handler; `utils/` and `backend.go`/`backends/`; `channel_log.go` and `test.go` at the repository root; OAuth on receive, status or stop routes; a shared module with Mailroom; Redis caching of the access token; a `client_credentials` refresh type (expressed as `custom` with a literal body); a lock across Courier workers.

**Depends on**: nothing in other repositories. The new keys are written into the channel config out of band (channel API or database); Flows does not expose them and is not changed.

## Current State

- `handlers/externalv2/externalv2.go` — `SendMsg` (line 249) reads `send_url`, `send_url_template`, `send_template`, `send_attachment_in_parts` and calls `sendMsgPart` once, or once per attachment plus one for the text. `sendMsgPart` (line 322) renders the body, builds the request with `http.NewRequest` (no context), sets `Authorization` from `send_authorization` verbatim only when non-empty (lines 427-430), calls `utils.MakeHTTPRequest`, writes one channel log with `courier.NewChannelLogFromRR`, then applies `mt_response_check`. A `sendMsgPart` error is turned into `(status, nil)` by `SendMsg`, with status `E`.
- `utils/http.go:57` `MakeHTTPRequest` uses `GetHTTPClient()`, a shared client with `Timeout: 60s`. It returns a non-nil `RequestResponse` even on error, and a non-2xx response carries `StatusCode` and `Body` alongside the error. It does not take a context.
- `sender.go:226` and `sender.go:276` wrap `SendMsg` in a 35s context. No outbound call in `E2` receives that context today.
- `channel_log.go:56` `NewChannelLogFromRR` copies the dumped request and response with no redaction. The `Authorization` header of the partner request is therefore stored in plain text today.
- `backends/rapidpro/channel.go:738` `UpdateChannelConfig` marshals the whole map passed in and replaces the `config` column, then clears the local channel cache of this instance (`clearLocalChannel`). The cache TTL is 5 minutes (`localTTL`, line 164), so other Courier instances can keep serving the previous config for up to 5 minutes.
- `handlers/teams/teams.go:170,214` `cloneChannelConfig` + `persistTeamsV2Token` is the nearest existing example of refreshing a token and persisting it with `UpdateChannelConfig`. `E2` does not import it (Principle XVI) and carries its own small clone.
- `mailroom/services/tickets/generic/token_refresh.go` (sibling repository) is the source being ported. It is read for reference only; nothing is imported from Mailroom.
- `test.go:1573` `MockBackend.UpdateChannelConfig` replaces `MockChannel.config` with the map it receives, so a test can read the persisted result from `channel.Config()` after `SendMsg`. It has no failure injection for `UpdateChannelConfig`.
- `handlers/test.go:289` `RunChannelSendTestCases` starts one `httptest` server per case, calls `SendMsg` under a 10ms context (line 367), and resolves `Responses` by matching `MockedRequest{Method, Path, RawQuery, Body|BodyContains}` and requiring that every entry is matched (`mockRRCount == len(Responses)`). `handlers.NewTestServer(backend)` is exported, so a direct test can run the handler against a wrapped `MockBackend`.
- `handlers/externalv2/externalv2_test.go` — `TestSendingWithAuth` (line 196) already covers the static header path and stays unchanged.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `handlers/externalv2/token_refresh.go` | new | Pure refresh logic: parse options (8 ordered rules), decide `when`, build and send the token request via `utils.MakeHTTPRequest`, read the response fields, return the new auth values and a `Token Refresh` channel log. No import of Mailroom. |
| `handlers/externalv2/externalv2.go` | modified | `SendMsg` parses options once and owns one auth state for every part; `sendMsgPart` sets the Bearer, snapshots the body, replays once, redacts the `Authorization` value in the partner send log (all E2 sends, Q2), and the handler persists the config clone. The disabled path keeps today's provider request and differs only in the stored log. |
| `handlers/externalv2/doc.md` | modified | Documents the new keys, defaults and the two design examples (Portuguese, as the rest of the file), plus the limits operators need: `body_contains` matches only non-2xx responses, `401`/`403` are the recommended `status_codes` and listing other statuses risks duplicate delivery, set `expires_in_field` or `expires_in_default`, and refresh-token rotation across workers is last-write-wins. |
| `handlers/externalv2/token_refresh_test.go` | new | Parser and token-call unit tests. |
| `handlers/externalv2/externalv2_test.go` | modified | Flow tests through `RunChannelSendTestCases`, plus direct `SendMsg` tests for the stateful sequences. Existing tests untouched. |
| `WENI-CHANGELOG.md` | modified | Release entry (Principle VIII). |

### Flow

1. `SendMsg` calls the parser once. Disabled or absent returns `(nil, nil)` and the whole refresh path is skipped. A parse error returns `(nil, err)` before any partner call; the sender (`sender.go`) turns that into an errored status.
2. `SendMsg` builds one auth state shared by all parts of this message: enabled flag, raw access token (`send_authorization` trimmed, with one case-sensitive leading `Bearer ` removed), refresh token, unix expiry, parsed options, and a `refreshedInThisSend` marker (see Implementation Decisions).
3. For each `sendMsgPart` with refresh enabled:
   1. If `when.expired` is true, the expiry is greater than zero, `now >= expiry`, and no refresh happened yet in this `SendMsg`, refresh first. On failure return the errored status and the error; the partner is not called.
   2. Build URL and body as today and snapshot the body bytes so the replay can rebuild the request.
   3. Set `Authorization: Bearer <raw token>`, including when the raw token is empty.
   4. Send with `utils.MakeHTTPRequest`, then build the partner send log from a copy of the response whose request dump has the `Authorization` value redacted.
   5. If the response matches `when` (evaluated on the `RequestResponse` even when the call returned an error; a nil or connection-failure response never matches), refresh once and send the part again with the new header. A response matches when its status is in `status_codes`, or when it is non-2xx and its body contains a non-empty `body_contains` entry. If the replay matches `when` again, do not refresh again.
   6. Run `mt_response_check` on the response that remains.
4. A successful token call updates the in-memory state before the replay and before the next attachment part, then persists the config (Data Model). A save failure is logged and the send continues with the in-memory token.
5. With refresh disabled, `sendMsgPart` sets `Authorization` from `send_authorization` exactly as today and the token endpoint is never reached.

Upper bound per part: 2 token calls (one proactive, one after a match) and 2 partner calls. The replay cap is a named constant (Constitution Alignment, XI).

### Rejected Alternatives

- Shared package with Mailroom: couples two services' release cycles for ~150 lines; the design settles a port, not an import.
- Redis cache of the token or a cross-worker lock: adds shared state and a new failure mode; last-write-wins on the channel row is accepted (see Failure Handling).
- `utils.MakeHTTPRequestWithRetry` for the replay: it retries 5xx/429/transient errors with backoff and an `Idempotency-Key`, which are other failure classes, and it re-sends the same request, so it cannot swap the `Authorization` header for the new token. The `E2` send path does not use it today.
- Proactive refresh only (no replay): partners can revoke or expire a token before `expires_in`, so a reactive replay is still needed.
- Replay on `body_contains` for any status, with an `Idempotency-Key` on the replay (Q3 option B) or with the duplicate risk accepted (option C): not chosen. B needs a partner that honours the header and changes the replay request; C can deliver a message twice after a 2xx.
- Passing the `SendMsg` context to the token request: `RunChannelSendTestCases` runs under a 10ms context and the partner call does not use the context today, so the token call would become the only context-bound call and would make the harness flaky. See Failure Handling.
- Redacting in `channel_log.go` for every handler: wider than Principle XVI allows (Q2 option C, not chosen).
- Leaving the send log as is and recording a constitution exception (Q2 option B, not chosen): it would keep live access tokens in stored logs.
- A dedicated `client_credentials` type: out of scope; use `custom` with a literal body.

## Contracts

All new keys are optional strings on the channel config, read with `StringConfigForKey`. A JSON number is treated as absent for every key.

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| `token_refresh_enabled` | added: `true`, `1` or `yes`, case-insensitive, after trim, turns the feature on; missing or any other value keeps today's behaviour | minor | yes |
| `token_refresh_type` | added: `custom` or `refresh`, required when enabled | minor | yes |
| `token_refresh_config` | added: JSON object serialized as a string, required when enabled (fields below) | minor | yes |
| `refresh_token` | added: OAuth refresh token, required when type is `refresh`; rewritten after a refresh that returns a rotated token | minor | yes |
| `expires_in` | added: unix timestamp in seconds, decimal string, when `send_authorization` expires; not a duration; rewritten after a refresh | minor | yes |
| `client_id`, `client_secret` | added: appended to the form body only when type is `refresh` and non-empty | minor | yes |
| `send_authorization` | existing key; when the feature is on it is rewritten as `Bearer <access_token>` | minor | yes (see below) |
| Outgoing provider request (`Authorization` header) | modified for opted-in channels only | minor | yes: unchanged for every channel without `token_refresh_enabled` |
| Token endpoint request (new outbound HTTP call to the operator's token URL) | added | minor | yes: only made for opted-in channels |
| `E2` partner send channel log (stored in Postgres) | modified for every E2 send: the `Authorization` value in the request dump is `[REDACTED]`; no schema change | minor | yes: log content only. The provider request is unchanged |
| `/c/e2/<uuid>/receive`, `sent`, `delivered`, `failed`, `stopped` | unchanged | none | yes |

`token_refresh_config` fields:

| Field | Meaning and default |
|-------|---------------------|
| `when.match` | Only `any` (OR), compared case-insensitively after trimming, as Mailroom does. Empty defaults to `any`; any other value is a parse error |
| `when.status_codes` | Refresh when the partner status is one of these |
| `when.body_contains` | Refresh when the partner response is non-2xx and its body contains any non-empty entry as a substring. A 2xx response never matches on its body (Q3, option A), so this is narrower than Mailroom's generic ticketer, which matches the body on any status |
| `when.expired` | Refresh before the partner call when `now >= expires_in` |
| `method` | Token HTTP method. Empty defaults to `POST` |
| `url` | Token endpoint. Required |
| `headers` | Headers for the token request |
| `body` | Literal body. Required for `custom`. Ignored for `refresh` |
| `token_field` | Dotted JSON path of the access token. Empty defaults to `access_token` |
| `refresh_token_field` | Dotted path of a rotated refresh token. Empty skips the update |
| `expires_in_field` | Dotted path of a duration in seconds. The value must be a JSON number that is a positive whole number (an integer, or a float such as `3600.0`). A JSON string (even `"3600"`), a fraction, zero or a negative value counts as absent and `expires_in_default` applies. This is stricter than Mailroom, which accepts a numeric string and truncates a fraction |
| `expires_in_default` | Seconds used when `expires_in_field` is absent or not a positive number |

Parsing rules, in this order:

1. Disabled or absent returns `(nil, nil)`.
2. `token_refresh_type` must be `custom` or `refresh`, compared case-insensitively after trimming (as Mailroom does).
3. `token_refresh_config` must be non-empty JSON.
4. `url` is required.
5. `when.match` must be `any` after the default is applied.
6. `custom` requires a non-empty `body`.
7. `refresh` requires a non-empty `refresh_token`.
8. `expires_in`, when non-empty, must parse as a base-10 int64. That value is the unix expiry, not a duration.

Field-path rules: a dotted path walks JSON objects; strings, numbers and booleans are accepted; an empty string is a miss. `expires_in_field` accepts an integer or a float that is a whole number of seconds, and only values greater than zero.

Token request: `custom` sends `method`, `url`, `headers`, `body` as configured. `refresh` sends a form body with `grant_type=refresh_token`, `refresh_token`, `client_id` and `client_secret` (the last two only when non-empty) and sets `Content-Type: application/x-www-form-urlencoded` when not already set. A token response with status 400 or higher, or one missing `token_field`, fails the refresh; the access token is never read from an error body. On success the expiry becomes `now + seconds` from `expires_in_field` when positive, else `now + expires_in_default` when `expires_in_default > 0`, else the previous expiry is kept.

**Header format change for opted-in channels**: with the feature on, `Authorization` is always `Bearer <raw token>`. A `send_authorization` that was not a `Bearer` value (for example `Basic ...`) is taken as the raw token and is therefore sent as `Bearer Basic ...`. Operators opt in knowingly; channels that do not opt in are untouched.

**Consumers affected**: partners of opted-in `E2` channels (they receive a different `Authorization` value after a refresh and one extra HTTP call per replay); operators and support staff who read `E2` channel logs, who will see `Authorization: [REDACTED]` and can no longer read the sent credential from a log (the channel config still holds it). No other service consumes these keys; no deprecation path is needed because nothing is removed or renamed.

## Data Model and Migrations

- **Channel config** (`channels_channel.config`, JSON): gains the optional keys `token_refresh_enabled`, `token_refresh_type`, `token_refresh_config`, `refresh_token`, `expires_in`, `client_id`, `client_secret`. After a refresh, `send_authorization` is set to `Bearer ` + the new raw access token, `refresh_token` is set when the new refresh token is non-empty, and `expires_in` is set (decimal string) when the new expiry is greater than zero. Every other key is left as it was.
- `UpdateChannelConfig` replaces the whole config map. The handler therefore clones `channel.Config()` (a shallow copy is enough, values are not mutated), applies the three writes to the clone and saves the clone. `channel.Config()` itself is never mutated, because the rapidpro channel object is shared through the 5-minute cache and read by concurrent sends.
- Secrets at rest: `client_secret` and `refresh_token` live in the config column like `send_authorization` does today; no new storage or encryption is introduced.

**Migrations**: none. No schema change and no backfill. The previously deployed version still works against the new data: it ignores the new keys and keeps using `send_authorization`, which holds the last refreshed token (`Bearer <token>`, the same format it reads today) until that token expires. After a rollback the operator must rotate the token by hand again, which is today's behaviour.

## Non-Functional Requirements

### Peak Load

**Declared peak: 242.9 sends/s (1-minute rate) at 2026-10-28 14:06 UTC, window 2026-10-07 to 2026-11-06.** This is the Courier service-wide peak from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}`, the same series and figure already declared by specs 003, 005, 006, 007 and 008. It is a proxy and a deliberately conservative one: Istio has no path label and Courier has no per-route or outbound series (spec 003 records the same limit), so the `E2` outbound share cannot be isolated. The spec therefore sizes the feature as if every one of those 242.9 per second were an opted-in `E2` send part (an attachment sent in parts counts as one send per part). The figure is not an `E2` measurement; if an `E2` series appears (for example the `courier.msg_send_E2` gauge from `sender.go`), the lower figure may replace it in an amendment.

Added cost at that peak, worst case (every part opted in and replayed, which needs a partner that keeps returning a matching status):

- Partner calls: at most 485.8/s in total, of which 242.9/s are the extra replay.
- Token calls: at most 485.8/s (up to 2 per part).
- `UpdateChannelConfig` writes: at most 485.8/s (1 per successful token call), all on the `channels_channel` rows of the opted-in channels.
- Realistic steady state: one refresh per expiry window per channel, because the new expiry is persisted, so a handful of token calls and writes per channel per hour, not per send.
- Concurrency bound: `MaxWorkers` defaults to 32 (`config.go`), so one instance runs at most 32 `SendMsg` calls, hence at most 32 token calls, in flight at a time.

What this spec bounds regardless of the number:

- The feature is opt-in per channel, so unconverted channels add no load.
- Per part, the opted-in path adds at most 2 token calls and 1 extra partner call (the single replay) and at most 1 `UpdateChannelConfig` write per successful token call. In steady state it adds one refresh per expiry window per channel, because the new expiry is persisted.
- Failure amplification: a partner that keeps returning a matching status makes every part cost 1 token call + 1 replay. If the token endpoint rejects the call, the replay does not happen and the failure is the errored status Mailroom retries.
- Concurrent refreshes happen when several sends on a channel cross the expiry together, or when another instance still holds the previous config in its cache (up to 5 minutes). Each one costs a token call and a config write; last write wins.
- The auth state is per `SendMsg` call and lives only in memory for that call. The only state that outlives the request is the channel config row in Postgres (Principle XII).

### Observability

- Application logs use logrus fields only, never interpolated strings: `comp=externalv2`, `channel_uuid`, `msg_uuid` as correlation identifiers, plus `event` (`token_refresh`, `token_refresh_save`), `trigger` (`expired` or `match`), `outcome` (`ok` or `failed`), `reason` (a fixed classification such as `status_4xx`, `status_5xx`, `connection`, `missing_token_field`, `invalid_json`), `status_code` and `elapsed`. They never carry the URN path, the message text, request or response bodies, `client_secret`, `refresh_token`, `access_token` or the `Authorization` value.
- Every token attempt, successful or failed, writes its own channel log with description `Token Refresh` that records method, URL, status code and elapsed only. Its `Request` and `Response` bodies stay empty, and its `Error` carries a fixed reason string instead of the raw Go error (a transport error includes the URL; a body error could echo credentials). The logged URL is the configured token URL without its query string and user info, so a credential placed in the URL is not stored.
- A failed save of the config is logged at error level with `channel_uuid`, `msg_uuid` and the error returned by the backend, which carries no config content. Error level is deliberate: the send continues with the in-memory token, but a rotated `refresh_token` that was not saved is lost, and every later refresh then uses a stale one, which breaks the channel until an operator re-issues it. The existing Sentry hook (panic, fatal and error levels) therefore reports it. See the XIII row in Constitution Alignment.
- The partner send log. Today `NewChannelLogFromRR` stores the dumped request, including `Authorization`, in plain text; with rotating tokens that would be a live access token (Principle III). Decision (Q2, option A): the `E2` handler redacts the value of the `Authorization` header in the request dump before it builds the partner send log, for every `E2` send log and not only for opted-in channels, and `channel_log.go` is not touched. Details:
  - `sendMsgPart` passes `NewChannelLogFromRR` a copy of the `RequestResponse` whose `Request` has the value of every `Authorization:` header line replaced by the named constant `redactedHeaderValue` (`[REDACTED]`). The header name match is case-insensitive and stops at the end of the line, so the rest of the dump (method, URL, other headers, body) and its line endings are unchanged. The scheme (`Bearer`) is redacted with the value; nothing about the credential is kept.
  - It applies to both partner attempts of a replayed part, to every attachment part and to the disabled path, because `send_authorization` is already stored in plain text today. The provider request itself is unchanged: only what is stored in the channel log changes.
  - Only the `Authorization` header is redacted. Other headers, the partner response and the body template output are not touched; the partner URL is not touched either.
  - `sender.go` still logs `msg_text` and `msg_urn` in its own application log. That is existing behaviour outside this change (Principle XVI) and is not made worse here.
- Signals to watch after rollout: count of `Token Refresh` logs per channel, rate of errored `E2` messages, rate of `token_refresh_save` failures.

### Failure Handling

Timeouts: the token call and the partner call both go through `utils.MakeHTTPRequest`, so both are bounded by the shared client's 60s timeout (Principle X). The 35s `SendMsg` context created in `sender.go` is not passed to outbound `E2` calls today, and this spec keeps that for the partner call to leave the disabled path unchanged and does not add it to the token call (rejected alternative above). Consequence: a part with a refresh and a replay can hold a sender goroutine for longer than 35s in the worst case (up to 4 outbound calls of 60s each); the extra cost is bounded by the replay cap and is accepted. The `UpdateChannelConfig` call receives the `SendMsg` context.

Behaviour per situation (the first seven rows are the design's error catalog):

| Situation | Partner called | Token called | Result |
|-----------|----------------|--------------|--------|
| Refresh disabled | yes, as today | no | unchanged |
| Enabled config invalid | no | no | `SendMsg` returns `(nil, err)`; the sender builds an errored status with a `Sending Error` log. The error is permanent until an operator fixes the config |
| Proactive refresh fails | no | yes, failed | status `E`, `SendMsg` returns `(status, nil)`; the failed attempt has its `Token Refresh` log |
| Partner answers 2xx with a `body_contains` needle, status not in `status_codes` | once | no | no match, no replay: the response is handled as a normal 2xx (Q3, option A) |
| Partner matches `when`, refresh succeeds, replay succeeds | twice | once | status wired when `mt_response_check` passes |
| Partner matches `when`, refresh fails | once | yes, failed | status `E`, error from the original partner response |
| Partner matches `when`, refresh succeeds, replay matches `when` | twice | once | status `E`, no further token call |
| Connection error, no response | the failed attempt only | no | status `E` |
| Token save fails after a good token response | yes, with the new Bearer | once | send continues; the error is logged |
| Token response 4xx/5xx, unparsable JSON or missing `token_field` | per the row that triggered it | yes, failed | refresh fails as above; the access token is never read from an error body |
| Partner returns a non-matching 4xx/5xx | once | no | status `E` as today; Mailroom decides the retry |
| Proactive refresh succeeds, then the partner matches `when` | twice | twice | allowed by the design: the replay path does not know about the earlier refresh |
| Partner 2xx but `mt_response_check` fails | per the row above | per the row above | status `E` as today; `when` is evaluated first and a 2xx body is never matched by `body_contains`, so this failure does not trigger a refresh |

Retry policy of the replay: one replay per part, named cap `maxTokenRefreshReplays = 1` and named delay `tokenRefreshReplayDelay = 0` (no backoff), because it replays a credential and not a transient failure: the first attempt failed on a stale credential, and waiting cannot fix that. When it is exhausted the failure stays recoverable: status `E` (Mailroom retries per its own policy), the channel logs of both partner attempts and of the token attempt, and the warning the sender already logs.

Idempotency of the replay (Q3, option A): the replay never follows a 2xx body match, so a response the partner reported as delivered is not sent twice. No `Idempotency-Key` is added, because `E2` sends none today and the partner contract is unknown. A residual duplicate-delivery risk remains only when the operator lists a status in `when.status_codes` that the partner can return after processing the request (for example a 5xx or a 2xx). `doc.md` recommends `401` and `403` only and states this risk next to the key.

Concurrency across workers: two workers can refresh at once and the last successful `UpdateChannelConfig` wins. If the partner rotates refresh tokens, a stale `refresh_token` can overwrite a newer one and break the chain until an operator re-issues it. The design accepts this (no lock, no Redis); it is documented in `doc.md` so operators can choose a partner configuration without rotation, or accept the risk.

### Security and Authorization

- Trust boundary: the token URL, method, headers and body come from the channel config set by the operator, at the same trust level as `send_url`, `send_template` and `send_authorization`. They are not provider input and not end-user input. Nothing from a received message reaches the token request.
- This spec adds no URL validation: no scheme restriction, no host allow-list, no private-address block. This matches `send_url`, which `E2` does not validate either. The risk (an operator with config write access can make Courier call an arbitrary URL) already exists for `send_url` and changes only in having one more operator-set URL. A validation change would belong to its own spec and to every `E2` URL together (Principle XVI).
- Server-side authorization: no new route and no new inbound input. The refresh runs only for channels whose own config enables it; the channel is resolved by Courier from the route and the queue, not from the client.
- Secrets: `client_secret`, `refresh_token` and `access_token` are never written to the repository (test fixtures use fake values), never injected through `courier.toml`, and never written to the `Token Refresh` log or to application logs. Their at-rest location is the channel config column, as for `send_authorization` today.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| AC-01 disabled path | `TestSendingWithAuth` stays unchanged (static header). New `TestSendingTokenRefreshDisabled` via `RunChannelSendTestCases`: cases with `token_refresh_enabled` unset, `"false"` and `"no"`, a past `expires_in` and a valid-looking `token_refresh_config` whose `url` points at a closed port. Each expects status `W`, `Authorization` equal to the raw `send_authorization`, and the last request on the partner path. Had the handler called the token URL the send would end in `E`. |
| AC-02 proactive refresh | `TestSendingTokenRefreshExpired` via `RunChannelSendTestCases` with a token path and a partner path in `Responses`. Matching both entries exactly once (`mockRRCount == len(Responses)`) proves one token call and one partner call; `Headers` proves the partner got `Authorization: Bearer <new>`. Cases for `custom` and for `refresh` (form body checked through `Body`/`BodyContains` of the token entry). |
| AC-03 replay on `when` match | `TestSendingTokenRefreshReplay`, direct `SendMsg` with a stateful `httptest` server (counters per path): 401 then token then 200 gives status `W` with one token call; two consecutive 401s give status `E` and still one token call. `body_contains` cases (Q3, option A): a non-2xx response whose body has the needle and whose status is not in `status_codes` is replayed once and ends wired with one token call; a 200 with the needle is not replayed, makes no token call and ends wired (when `mt_response_check` passes); a status listed in `status_codes` replays whatever the body says. The `when` matcher has unit tests for the same rules (status match, body match on non-2xx, no body match on 2xx, nil response). |
| AC-04 persistence | `TestSendingTokenRefreshPersists`: a one-case `RunChannelSendTestCases` run on its own channel, then assertions on `channel.Config()`: `send_authorization == "Bearer <new>"`, `refresh_token` and `expires_in` present when the token response has them, an unrelated key (`send_url`, `mt_response_check`) unchanged, and the map captured before the run still holds the old `send_authorization` (the clone proof). |
| AC-05 attachment parts | `TestSendingTokenRefreshInParts` via `RunChannelSendTestCases`: expired token, token response without expiry info, two attachments plus text; one token entry and three partner entries all matched exactly once, every partner request carries the new Bearer. Plus a direct case where part 1 gets 401, refreshes and its replay returns 200, and part 2 sends the new Bearer with no further token call. |
| AC-06 stale token never reaches the partner | Harness cases: token 400, token 500, token 200 without `token_field`, unparsable token JSON, each with an expired token: status `E`, no partner entry matched. `TestSendingTokenRefreshInvalidConfig`: invalid enabled config returns the exact error and no partner call. Replay failure: partner 401, token fails, status `E`, partner called once. `TestSendingTokenRefreshPartnerServerError` via `RunChannelSendTestCases`: a partner 500 that does not match `when`, with a valid token, ends in `E` with the existing Bearer and `Path` on the partner path (a token call after it would change the last path); a 500 whose body has the `body_contains` needle replays once (Q3). |
| AC-07 no secrets in logs | `TestSendingTokenRefreshLogs`, direct `SendMsg`: the `Token Refresh` log has empty `Request`/`Response` and its `Error`, `URL` and `Description` contain none of the fixture `client_secret`, `refresh_token`, `access_token`; a `logrus` test hook asserts the same for application log fields and messages. The same test checks every partner send log of a replayed part (both attempts) and of an attachment part: the dump contains `Authorization: [REDACTED]` and none of the fixture token values. A direct `SendMsg` case on a channel with the feature disabled and a static `send_authorization` checks that its send log is redacted too (the existing `TestSendingWithAuth` stays unchanged). |
| AC-08 other routes unchanged | The existing `TestHandler`, `TestHandler2`, `TestTemplateHandler`, `TestHandleWithUrnAuth*` stay unmodified and green, and the diff does not touch `receiveMessage`, `receiveStatus`, `receiveStopContact`. |

Unit tests in `token_refresh_test.go`: disabled or absent returns `(nil, nil)`; `custom` defaults (`POST`, `access_token`, `any`); each error rule (missing type, bad type, empty config, invalid JSON, missing `url`, `when.match` not `any`, `custom` without `body`, `refresh` without `refresh_token`, non-numeric `expires_in`); truthy values `true`, `1`, `yes` with case and whitespace; `refresh` form body with `grant_type`, `refresh_token`, `client_id`, `client_secret` and default `Content-Type`; expiry from `expires_in_field` as `now + N`, `expires_in_default` fallback, previous expiry kept; whole-number float accepted, zero and negative rejected; dotted path hit, miss, empty string; `when` matching on status, body, nil response; the `Authorization` redaction function (header name in any case, `Bearer` and non-`Bearer` values, a dump without the header left unchanged, CRLF line endings and the rest of the dump preserved, only the `Authorization` header touched).

**Flow coverage**: complete `SendMsg` use cases from channel config and message to partner request, resulting status and persisted config: static send, proactive refresh, replay after a match, multi-part send sharing one refresh, persistence of the refreshed config.

**Failure paths covered**: token 4xx, token 5xx, 200 without `token_field`, invalid token JSON, partner 5xx without a match, partner connection error, replay that matches again, refresh failure after a partner match, invalid enabled config, config save failure (a thin wrapper around `MockBackend` that fails `UpdateChannelConfig`, run through `handlers.NewTestServer`: status still wired, partner got the new Bearer, the failure is logged).

**Not covered by design**: a timeout of the token or partner call. Both go through the shared client with a fixed 60s timeout that cannot be shortened without changing `utils/` (Principle XVI), and a test would wait 60s. The closed-server cases (`TestSendingTokenRefreshConnectionError`, token `connection` failure) exercise the same `MakeHTTPRequest` error branch the handler classifies, so the handler's behaviour on a transport error is covered; the timeout itself is not.

**Why the 401-then-retry sequence needs a direct test**: `ChannelSendTestCase.Responses` is keyed by request shape (`Method`, `Path`, `RawQuery`, `Body`/`BodyContains`) and cannot return two different answers to the same request, so it cannot express "401 first, 200 on the replay". Those sequences use a stateful `httptest` server with a counter and call `SendMsg` directly under a normal context timeout. The harness's own 10ms context is not used by those tests. The harness cannot assert that a request did not happen, so absence is shown through behaviour (an unreachable token URL, or exact `Responses` counts).

**Why the other direct tests are direct**: each needs something `RunChannelSendTestCases` does not offer. Same-shape answers that change between calls (partner 401 then 200, a 400 with a `body_contains` needle replayed once, a part that refreshes and the next part that does not) need a stateful server with counters. Asserting that a call did not happen (`TestSendingTokenRefreshConnectionError`, with a closed partner server and a counting token server) needs per-path counters and a server the harness cannot close. Reading `status.Logs()` and application logs (`TestSendingTokenRefreshSendLogs`, `TestSendingTokenRefreshAppLogs`) needs the `logrus` test hook and the status object, which the harness compares only through status, headers, path and body. The save-failure test needs a backend whose `UpdateChannelConfig` fails, and `MockBackend` has no failure injection.

**Stubbed at the boundary**: partner and token endpoints (`httptest` servers); the backend (`courier.MockBackend`, with a failing wrapper for the save-failure test). Test fixtures use obviously fake values (`test-access-token-1`, `fake-client-secret`, `fake-refresh-token`); no real credentials and no network access to real providers.

## Rollout and Rollback

- **Flag**: the feature is opt-in per channel, so the config key `token_refresh_enabled` is the flag. There is no environment variable and no deploy ordering, and no other repository has to ship first.
- **Staged enablement**: enable one pilot channel first and watch the signals listed in Observability, then widen. This is a precaution, not a gate on the peak: the peak is declared in Peak Load. While watching, compare the observed token calls and `token_refresh_save` events per channel with the steady state described there (one refresh per expiry window); a rate close to one per send means the partner returns no expiry (`expires_in_field`, `expires_in_default`) or keeps matching `when`, and the channel should be fixed before enabling more.
- **Done means in production**: an opted-in channel keeps sending past the expiry of its access token with no manual rotation, and an errored-message rate that does not exceed the static-header baseline.
- **Rollback**: remove `token_refresh_enabled` from the channel (takes effect when the channel cache is refreshed, up to 5 minutes), or deploy the previous Courier version. In both cases `E2` keeps using `send_authorization`, which already holds `Bearer <last refreshed token>`, until that token expires. No migration to reverse. The log redaction is not behind the key: it applies to every E2 send and is removed only by deploying the previous version, which also restores plain-text `Authorization` in E2 send logs.
- **Release**: new entry in `WENI-CHANGELOG.md` (Principle VIII). The entry is MINOR; the version number matches the tag cut at release time (next after the latest entry, `1.80.0`, if that is already tagged).

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| II. Secrets and Supply Chain | Fixtures use fake credentials only. `client_secret`, `refresh_token` and `access_token` are never committed, never in `courier.toml`, never logged. No new dependency: `github.com/buger/jsonparser` and `net/http` are already in `go.mod`. Run `govulncheck` unchanged in CI. | none. At-rest storage in the channel config is the pre-existing location for `send_authorization`. |
| III. Structured, Privacy-Safe Observability | Logrus fields only, `channel_uuid` and `msg_uuid` as correlation identifiers, no URN path, no message text, no bodies or secrets. The `Token Refresh` channel log holds method, URL (without query or user info), status and elapsed only. | none. Q2 chose option A: the E2 handler redacts the `Authorization` value in its own send logs, so channel logs no longer hold provider credentials from this handler. This replaces the design's "leave the send log as is". The `sender.go` application log of `msg_text` and `msg_urn` predates this change and is outside this spec's scope. |
| IV. Versioned Contracts | The new keys, the token endpoint call and the `Authorization` change for opted-in channels are additive and optional: MINOR, backward compatible, nothing removed. Receive, status and stop routes unchanged. | none |
| VIII. Release Changelog | One `WENI-CHANGELOG.md` entry under the next version, matching the tag; `CHANGELOG.md` (upstream) is not edited. | none |
| X. Fail Gracefully and Predictably | Token and partner calls use `utils.MakeHTTPRequest` and the shared client (60s timeout), never `http.DefaultClient`. Every failure of the token call becomes an errored status and a `Token Refresh` channel log; nothing panics and no internal detail is returned. | none. The 35s `SendMsg` context is not enforced on outbound calls, as for the existing partner call (see Failure Handling). |
| XI. Bounded Retry Over REST | The replay is bounded by `maxTokenRefreshReplays = 1` with an explicit, named `tokenRefreshReplayDelay = 0` as its backoff strategy, runs at most once per part, and its exhaustion stays recoverable (errored status, channel logs, Mailroom retry). No unbounded loop. | Departure: the replay can follow a 401/403 (a 4xx) and the `E2` request carries no deduplication key; XI says not to retry a 4xx and to protect retries with idempotency. Justification: a 401/403 caused by an expired credential reflects a stale credential, not a defect in the request, and one attempt with a new credential can succeed; the replay re-sends the same message once, as Mailroom's retry of an errored `E2` message already does. The scope of the departure is fixed by Q3 (option A): the replay follows a status in `status_codes`, or a `body_contains` match on a non-2xx response only, so a response reported as delivered (2xx) is never duplicated by a body match. No `Idempotency-Key` is added: `E2` sends none today and the partner contract is unknown. What remains is the replay of a non-2xx response the partner may have processed (a status the operator lists in `status_codes`); `doc.md` recommends `401` and `403` and states the risk. `MakeHTTPRequestWithRetry` is not used (see Rejected Alternatives). |
| XII. Stateless, Peak-Sized Services | The auth state is per `SendMsg` call and held only in memory for the call. The only state that outlives it is the channel config row in Postgres. No cache, no file, no Redis. | none. The peak is declared in Peak Load (242.9 sends/s, 1-minute rate, window 2026-10-07 to 2026-11-06), with the worst-case added cost at that peak. Note: the figure is the service-wide proxy used by specs 003 to 008 because no `E2` or outbound series exists, as those specs record. |
| XIII. Diagnosable Errors | The only new error report is the save failure (`event=token_refresh_save`), logged at error level so the existing Sentry hook reports it, with `channel_uuid` and `msg_uuid` as opaque identifiers. No URN path, name, message text or config content. | Departure: no contact UUID. `courier.Msg` exposes none inside the handler (same exception as specs 005 and 008). Error level is intentional: an unsaved rotated refresh token breaks later refreshes, so the signal must reach Sentry. |
| XIV. Tests Exercise Flows | Main paths run through `RunChannelSendTestCases`; the stateful sequences use a direct `SendMsg` test and the spec records why. Failure paths listed in Test Strategy are each covered. Unit tests only add edge cases. | none |
| XV. Explicit Over Clever | Named constants: `maxTokenRefreshReplays = 1`, `tokenRefreshReplayDelay = 0`, `bearerPrefix = "Bearer "`, `defaultTokenField = "access_token"`, `defaultTokenMethod = http.MethodPost`, plus `tokenRefreshMatchAny`, `tokenRefreshTypeCustom`, `tokenRefreshTypeRefresh`, `refreshTokenGrantType`, `tokenRefreshLogDescription`, `redactedHeaderValue = "[REDACTED]"`, the form field names, the `Authorization` and `Content-Type` header names (new code only), the fixed failure reasons, the log vocabulary (`comp`, `event`, `trigger` and `outcome` values), `tokenRefreshEnabledValues` (`true`, `1`, `yes`), `authorizationHeaderPattern`, and one constant per config key. Status thresholds use `http.StatusBadRequest` and `http.StatusInternalServerError`, and the token form `Content-Type` default reuses `contentTypeMappings[contentURLEncoded]`. Log field keys stay inline, each once, inside one helper. Existing literals in `sendMsgPart` are not rewritten (Principle XVI). Comments explain why (credential replay, clone before save, shared auth state across parts). | none |
| XVI. Contained Changes | Only `handlers/externalv2` and `WENI-CHANGELOG.md` change. No change to `channel_log.go`, `test.go`, `utils/`, `backend.go`, other handlers, Flows or Mailroom; the Teams clone helper is not reused or refactored. | none |

## Implementation Decisions

Carried from the design (settled, not re-asked):

- The renewed token is sent only as `Authorization: Bearer <access_token>`, and persisted in `send_authorization` as `Bearer <access_token>`.
- The keys and the two refresh types (`custom`, `refresh`) match `mailroom/services/tickets/generic`. The code is a port adapted to `utils.MakeHTTPRequest`; no shared package, no Redis cache, no `client_credentials` type.
- The logic lives in the `E2` handler. `token_refresh.go` holds parsing and the token call; `externalv2.go` holds the auth state, the Bearer header, the replay and the persistence.
- `SendMsg` parses once. A parse error returns `(nil, err)`. A `sendMsgPart` error keeps today's `(status, nil)` with status `E`.
- The raw access token is `send_authorization` trimmed, with one leading `Bearer ` removed when that exact case-sensitive prefix is present. With the feature on the header is `Bearer <raw>`, even when the raw token is empty.
- `expires_in` is read only as a string; a JSON number is treated as absent until a successful refresh writes the string form.
- Last successful `UpdateChannelConfig` wins across workers; there is no lock.
- `when` conditions are OR. `when` is evaluated on the `RequestResponse` even when `MakeHTTPRequest` returns an error; a nil response (connection failure) never matches.
- The config is cloned before the save because `UpdateChannelConfig` replaces the whole map.

Where this spec adds to, or differs from, the design:

- **Partner send log redaction (Q2, option A)** overrides the design, which left the send log as is and listed redacting `Authorization` on the normal send log as out of scope. The `E2` handler now redacts the `Authorization` value in all its send logs (see Observability). AC-07 therefore holds as written.
- **Replay policy (Q3, option A)** overrides the design, which replays on `status_codes` or on a `body_contains` match of any status. Here a `body_contains` match replays only when the response is non-2xx; `status_codes` replays on its status as designed. A design test case ("HTTP 200 whose body contains a `body_contains` needle refreshes and retries once") is therefore replaced by two cases: the 200 is not replayed, and a non-2xx with the needle is. This is also a semantic difference from Mailroom's generic ticketer for `body_contains` (the keys stay identical), recorded in `doc.md`. A partner that reports an expired token with a 200 and an error body is not covered by the reactive path; it relies on `when.expired` with `expires_in`.
- **Redaction applies to every E2 send log**, including channels that did not opt in, because `send_authorization` is stored in plain text today and Q2 limits the redaction to the handler, not to a flag. AC-01 ("sends exactly as today") is about the provider request, which does not change; the stored log content does.
- **Timeout wording**: the request said the calls run "under the 35s SendMsg context". The code shows the context is not passed to outbound `E2` calls; the effective bound is the shared client's 60s timeout. The spec records the facts and does not add a context to the token call.
- **`refreshedInThisSend` marker (addition)**: after a successful refresh in a `SendMsg`, the proactive-expiry check is skipped for the remaining parts of that same `SendMsg`. Without it, a token response that carries no expiry information keeps the old (past) `expires_in`, so every following part would refresh again, which would break AC-05. Messages sent later still follow `expires_in`; operators should configure `expires_in_field` or `expires_in_default` to avoid refreshing on every message, and `doc.md` says so.
- **Token log URL and error (addition)**: the `Token Refresh` log stores the token URL without query string and user info, and a fixed reason string instead of the raw error, so the log cannot echo credentials placed in the URL or in an upstream error. This stays inside the "method, URL, status code and elapsed only" rule.
- **Per-part call bound (note)**: a proactive refresh followed by a partner match can make two token calls in one part. The design allows it; it is kept and bounded by the replay cap.
- **Config save context**: `UpdateChannelConfig` receives the `SendMsg` context; the HTTP calls do not (see Timeout wording).
- **Test seams**: `RunChannelSendTestCases` shares one channel across cases and `MockBackend.UpdateChannelConfig` mutates it, so tests that refresh use their own channel per run to avoid leaking a refreshed config into other cases.

## Open Questions

None. Q1 to Q3 were answered on 2026-11-26; see Clarifications. The peak load is declared in Peak Load by reusing the Courier service-wide peak (Q1, revised on 2026-11-26); it is a proxy for `E2` outbound and is noted as such.
