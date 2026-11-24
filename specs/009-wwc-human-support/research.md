# Research: WWC human-support forwarding

The engineering spec already fixed the product behaviour. These decisions are how that behaviour sits on the current code.

## 1. Where the privileged retry lives

- **Decision**: a function `postPrivileged` in `handlers/weniwebchat/weniwebchat.go` loops on `utils.MakeHTTPRequest` and `utils.GetHTTPClient`. It retries HTTP 500, 429, 502, 503, 504, transport errors, and, only when the JSON `type` is `message_status`, HTTP 404. Cap is `wwcPrivilegedAttempts` (3). Backoff is a local copy of the `utils.withJitter` formula (`base << attempt`, then half plus a random half). 401, 400, 409, and 403 return on the first response.
- **Rationale**: `shouldRetryTransient` does not retry 500 or 404, and the spec forbids changing it. `utils.withJitter` is unexported, so the handler cannot call it. `MakeHTTPRequestWithRetry` would also hide the `message_status`-only 404 rule.
- **Alternatives considered**: export `withJitter` or teach `shouldRetryTransient` about 500 (both touch every channel); call `MakeHTTPRequestWithRetry` and then special-case 404 (two retry policies in one send).

## 2. Idempotency-Key

- **Decision**: `postPrivileged` sets `Idempotency-Key` itself: `event_id` for control events, `msg.UUID()` for a privileged chat message. The same value is reused on every attempt of that call.
- **Rationale**: `MakeHTTPRequest` does not set the header. Today's `SendMsg` key includes `time.Now().UnixNano()`, so a retry inside one call would still be stable only if the key is computed once. The socket ignores the header; the body `event_id` is the real idempotency key. The header stays stable anyway.
- **Alternatives considered**: omit the header (works against this socket, and drops the spec's requirement); keep the nano suffix (a mailroom requeue would look like a different operation in channel logs).

## 3. Reading metadata

- **Decision**: `jsonparser` on `msg.Metadata()`, the same style as `customDataFromMetadata` and `ActionType`. `author`, `agent_first_name`, and `event_id` are strings. `occurred_at_ms` is `jsonparser.GetInt`. `open` is `GetBoolean`. `message_ids` uses `ArrayEach` and rejects a non-string element. `status` is a string.
- **Rationale**: metadata is already a JSON blob on `courier.Msg`. No new accessor on `DBMsg`.
- **Alternatives considered**: unmarshal the whole object into a struct (drops unknown keys' order and pulls in fields this handler must ignore, including `custom_data`).

## 4. Chat payload compatibility

- **Decision**: add `Author` and `AgentFirstName` to `moPayload` with `omitempty`. Empty strings stay off the wire, so existing `RequestBody` fixtures keep matching. Control events use a separate struct without a `message` object. `sendPayload` and the inline text/attachment sender both call one helper that chooses `postPrivileged` when `author` is `human_agent` or the name is non-empty, and today's `MakeHTTPRequestWithRetry` otherwise.
- **Rationale**: product and attachment sends already go through `moPayload`. A second builder would drift. `omitempty` is what keeps unlabeled AI traffic unchanged.
- **Alternatives considered**: put `author` inside `message` (the socket reads the top-level field and strips a nested one on save).

## 5. Typing path and the sender

- **Decision**: `RunChannelActionTestCases` hardcodes `action_type` `typing_indicator` and does not record `Authorization`. Leave that harness alone. Nameless typing stays on `TestActions`. Named typing and `typing_stop` are direct `SendAction` tests in `weniwebchat_test.go`. In `sender.go`, extract `bestEffortAction(msg) bool`: true for `typing_indicator` on every channel (today's early return), and true for `typing_stop` only when `msg.Channel().ChannelType() == "WWC"`. A unit test feeds mock messages. `SendMsg` does not grow a typing branch.
- **Rationale**: a missed sender branch would turn `typing_stop` into a chat `type: message` only if `SendMsg` also built that body. The predicate test locks the branch that prevents the fall-through. Changing `RunChannelActionTestCases` would touch every channel's action tests.
- **Alternatives considered**: a Foreman integration test (no existing harness, and it pulls Redis); handling `typing_stop` inside `SendMsg` and returning a status (the spec says typing writes no `MsgStatus`).

## 6. Token config

- **Decision**: field `WwcHumanSupportIngressToken string` on `courier.Config`, default `""`. ezconf `CamelToSnake` yields `wwc_human_support_ingress_token`, so the env var is `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN` (loader name `courier`). Tests that need it call `handlers.NewTestServer`, set the field, then `Initialize`. They do not go through `RunChannelSendTestCases`, which builds its own server.
- **Rationale**: matches `WhatsappCloudWebhooksToken` and the spec's env name. The shared send harness cannot see the field without a new hook.
- **Alternatives considered**: a package-level test setter (hidden global); a config hook on `RunChannelSendTestCases` (shared test API for one handler).

## 7. Channel log redaction

- **Decision**: before `NewChannelLogFromRR`, replace the `Authorization` header value in `rr.Request` with `Bearer [redacted]` and replace the JSON string of `agent_first_name` with `[redacted]`. Do not change `channel_log.go` `sanitizeBody`.
- **Rationale**: `httputil.DumpRequestOut` stores the bearer. `sanitizeBody` only drops non-text bodies. A shared sanitizer change would rewrite logs for Meta and other bearers in the same release.
- **Alternatives considered**: omit the channel log on privileged sends (loses the status code operators use); redact inside `NewChannelLogFromRR` (every channel).

## 8. Inbound `message_id`

- **Decision**: optional string on `miMessage`, `json:"message_id,omitempty"`. Accept when length is 1–64 and `utf8` runes contain no Unicode space (`unicode.IsSpace`). Then `WithExternalID`. Otherwise drop it, log `message_id_rejected: true`, and still queue the message. Do not require a hyphenated UUID shape.
- **Rationale**: the socket mints a UUID, which fits the length rule. Rejecting a non-UUID would fail a future opaque id the socket is allowed to mint. Failing the webhook would drop the shopper message. `WriteMsg` already inserts `external_id`.
- **Alternatives considered**: strict UUID parse (tighter than the spec's 64-character rule); store the id in metadata as well (second copy for Engine to miss).

## 9. What this plan does not research

- Live-frame `timestamp_ms` and default `author: ai_agent` on HTTP `/send` stay socket work. Courier does not send `timestamp_ms` and does not invent `author`.
- `event_id` validation on the socket's `message_status` stays a socket fix. Courier already refuses a missing or over-long `event_id` before the POST.
