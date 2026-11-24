# Engineering Specification: WWC human-support forwarding

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `009-wwc-human-support`

**Created**: 2026-11-23

**Status**: Draft

## Inheritance from Product Spec

- Product Spec: Atendimento humano visível no Shopping Assistant (tag, marcos, horário, status de leitura e nome do atendente) — https://github.com/weni-ai/vtex-cx-experience-specs/blob/59dabf0b030431fae835756a43aa7efd8d58f953/specs/009-sa-human-support-status/spec.md
- Pinned version: `59dabf0b030431fae835756a43aa7efd8d58f953`
- Architecture doc: none
- Inherited binding decisions: BD-001 (only the Shopping Assistant changes; WhatsApp and the Live Desk screen stay as they are); BD-002 (read status only on shopper messages sent during human support, and Read only after Live Desk confirmation); BD-003 (the shopper sees only the agent's first name, never surname, photo, e-mail, or the time of the read); BD-004 (every balloon or card sent from launch carries a time, in every mode); BD-005 (no merchant setting; every Shopping Assistant channel); BD-006 (one delivery, and only when tag, milestones, time, status including Read, agent name, and typing-with-name all exist); BD-007 (milestones, names, status, and times stay in conversation history and come back the same on reload); BD-008 (composer text, the AI transfer announcement, the voice screen, the launcher, the preview, and the unread counter do not change)
- Scope of this spec: the Weni Web Chat hop inside Courier. Accept human-support facts the Engine queue already carries, POST them to weni-webchat-socket `POST {base_url}/send` in the socket's privileged shapes, and store the shopper `message_id` the socket puts on the existing receive callback so Engine can name those rows later
- Divergences: none

## Scope in This Repository

**This repository implements**:

- Outbound WWC only, in `handlers/weniwebchat`. Read optional keys from `courier.Msg.Metadata()` and translate them into the socket bodies below. Attach `Authorization: Bearer <COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN>` on every privileged body.
- Inbound WWC `POST /c/wwc/<uuid>/receive`. When the callback JSON contains `message.message_id`, store that value on the incoming `msgs_msg.external_id`. Method, URL, and content type of the callback stay as they are.
- Status mapping so a privileged failure is either permanent (`MsgFailed`) or recoverable (`MsgErrored`) under the existing `msgs_msg` retry. Control events are not turned into chat bubbles.

**Out of scope here**:

- Rendering the header tag, milestones, ticks, grouping, time and date formats, ellipsis, and accessibility labels. Owned by **webchat-react**.
- Originating transfer, close, read, typing, and the agent's name. Owned by **Engage / Live Desk** and **Engine**. Courier does not call Live Desk.
- Persisting milestones, delivery status, or the agent name for the widget. Owned by **weni-webchat-socket**. Courier does not mint `message_id`, `support_id`, or `timestamp_ms`.
- Deciding that an unlabeled outbound row is an AI message. Engine sets `metadata.author`. The gRPC AI stream does not pass through Courier.
- WhatsApp (`handlers/facebookapp`, `handlers/whatsapp`), the Live Desk screen, voice mode, the launcher, the preview, and the unread counter (BD-001, BD-008).
- A failed-send retry action in the widget. The existing red-icon path stays.
- Changing `utils.shouldRetryTransient` for every channel. HTTP 500 retry for this feature is local to the privileged WWC POST.

**Depends on**:

- **Engine**, via the existing outgoing Redis queue (`backends/rapidpro` `msgQueueName`) and `msgs_msg.metadata`. Engine emits the keys in Contracts only on WWC channels, and only after this Courier build is serving and the ingress token matches the socket. Engine sends a first name that is already a single token. Engine reads `msgs_msg.external_id` of the inbound WWC row and sends that value back as `message_ids` on `message_status`. Engine does not call the socket `/send` directly.
- **weni-webchat-socket** `POST /send`, as implemented in this workspace and specified in that repo's `specs/006-human-support-status/contracts/http-send.md`. Privileged types: `human_support_started`, `human_support_ended`, `human_support_state`, `message_status`. Privileged also: `author` `human_agent`, or a non-empty `agent_first_name` (including on `typing_start` / `typing_stop`). Success is HTTP 202. `401`, `400`, and `409` are permanent. The socket stamps `timestamp_ms` on the history row; Courier does not send that field.
- **Flows / Mailroom**: no new callback URL. They already consume the inbound message row. They must keep `external_id` when they hand the shopper message to Engine.

## Current State

- `handlers/weniwebchat/weniwebchat.go` — `Initialize` registers `POST /c/wwc/<uuid>/receive` as `receiveMsg`. `miPayload` / `miMessage` require `type`, `from`, `message.type`, and `message.timestamp`. Unknown JSON keys are dropped, so a callback `message.message_id` is discarded and the incoming row is written with an empty `external_id`. `SendMsg` builds `moPayload` (`type`, `to`, `from`, `channel_uuid`, `message`) and POSTs `{ConfigBaseURL}/send` through `utils.MakeHTTPRequestWithRetry` (3 attempts, 500ms base backoff). The idempotency key includes `time.Now().UnixNano()`. There is no `Authorization` header. HTTP errors, including every 4xx and 5xx, become `MsgFailed`. `sendProductMessage` uses the same `sendPayload`. `SendAction` always POSTs `type: typing_start` with `from: ai-assistant` via `utils.MakeHTTPRequest` (no retry) and returns an error on any non-2xx. `moPayload` has no `author` or `agent_first_name`.
- `handlers/weniwebchat/weniwebchat_test.go` — receive fixtures are exact JSON strings without `message_id`. `TestSending` uses `RunChannelSendTestCases` and exact `RequestBody`. `TestActions` expects the nameless `typing_start` body and status 200. `channelUUID` in fixtures is `8eb23e93-5ecb-45ba-b726-3b064e0c568c`. The mock channel address is `250788383383`.
- `sender.go` — `action_type` `typing_indicator` returns through `SendMsgAction` and logs a warning on error. It does not write `MsgStatus`. Every other queued msg, including a future control event, falls through to `SendMsg`, which today would POST it as a chat `type: message`.
- `backends/rapidpro/msg.go` — `Metadata()` is the raw `msgs_msg.metadata` object. `ActionType()` reads `metadata.action_type`. `typing_indicator` is `courier.MsgActionTypingIndicator` in `msg.go`. No human-support action constants exist.
- `backends/rapidpro/status.go` — `MsgErrored` (`E`) increments `error_count` and sets `next_attempt` to `NOW() + 5 * (error_count+1) minutes`. The third `E` (`error_count >= 2` before the increment) is stored as `F`. `MsgFailed` (`F`) is not retried. `WriteMsg` already persists `external_id` for inbound rows (`msg.go` insert).
- `utils/http.go` — `GetHTTPClient` timeout is 60s. `shouldRetryTransient` retries 429, 502, 503, 504, and transport errors. It does not retry HTTP 500. A 2xx response, including 202, is success.
- `channel_log.go` — `NewChannelLogFromRR` stores `httputil.DumpRequestOut`, which includes headers. `sanitizeBody` does not redact `Authorization`.
- `config.go` — secrets are `COURIER_*` fields on `courier.Config`, read by handlers as `h.Server().Config()`. There is no WWC ingress token.
- Other channel handlers do not read these metadata keys. WhatsApp send is unchanged.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `config.go` | modified | `WwcHumanSupportIngressToken string` with an empty default. ezconf name: `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN`. Injected from the secrets manager. Same value as the socket `WWC_HUMAN_SUPPORT_INGRESS_TOKEN`. |
| `handlers/weniwebchat/weniwebchat.go` | modified | Decode optional `message.message_id`. Forward `author` and `agent_first_name`. POST the four control types and `typing_stop`. Bearer on privileged bodies. Local validation, status mapping, in-request retry of 500 and of `message_status` 404, and log redaction. |
| `sender.go` | modified | For channel type `WWC` only, `action_type` `typing_stop` uses the same non-critical `SendMsgAction` path as `typing_indicator`. Control events stay on the `SendMsg` path so a status is written. No other channel's branch changes. |
| `msg.go` | modified | Named constants for the new `action_type` strings. |
| `handlers/weniwebchat/weniwebchat_test.go` | modified | Flow cases for receive, chat send, control events, typing, and the failure statuses. |
| `WENI-CHANGELOG.md` | modified at release | One minor entry when the code ships. Not part of writing this spec. |

`backends/rapidpro` is not modified. `external_id` and `metadata` already round-trip.

### Flow

Constants in `handlers/weniwebchat`: `wwcPrivilegedAttempts` = 3, `wwcPrivilegedBaseBackoff` = 500ms, `wwcMaxEventIDLen` = 128, `wwcMaxAgentFirstNameRunes` = 100, `wwcMaxStatusMessageIDs` = 100, `wwcMaxMessageIDLen` = 64. Action type strings live in `msg.go`: `human_support_started`, `human_support_ended`, `human_support_state`, `message_status`, `typing_stop`. The existing `typing_indicator` value stays.

**Inbound shopper message.** `receiveMsg` keeps today's type checks and timestamp parse. If `message.message_id` is absent or empty, the row is written as today. If it is present, it must be 1 to 64 characters, with no Unicode whitespace; otherwise the field is dropped and the message is still accepted. A valid value is `msg.WithExternalID`. Courier does not read `author`, `delivery_status`, `support_id`, or `timestamp_ms` from the callback. `received_on` stays `time.Unix` of `message.timestamp` in seconds.

**Outbound chat message (no control `action_type`).** `SendMsg` and `sendProductMessage` build the same `moPayload` as today. When `metadata.author` is `ai_agent` or `human_agent`, set top-level `author`. When `metadata.agent_first_name` is present, trim it. Empty after trim omits the field. Internal Unicode whitespace, or more than 100 runes, sets `MsgFailed` and does not POST. `author: ai_agent` together with a non-empty name sets `MsgFailed` and does not POST. Any other `author` value sets `MsgFailed` and does not POST. Courier does not take the first token of a longer name.

A body is privileged when `author` is `human_agent` or `agent_first_name` is non-empty. Privileged chat sends, including product and attachment posts, use the privileged POST below. An `ai_agent` body with no name stays unauthenticated, matching the socket. Absent `author` leaves the JSON without `author`, which is today's body.

**Control events.** Before the chat-message builder, `SendMsg` handles `action_type` `human_support_started`, `human_support_ended`, `human_support_state`, and `message_status`. The text, attachments, and products of that queued row are not sent. The JSON is:

| `action_type` | JSON `type` | Required metadata | Notes |
|---------------|-------------|-------------------|--------|
| `human_support_started` | `human_support_started` | `event_id`, `occurred_at_ms` (JSON number, unix milliseconds) | No name. No second event for an agent accept or an agent-to-agent transfer. |
| `human_support_ended` | `human_support_ended` | `event_id`, `occurred_at_ms` | |
| `human_support_state` | `human_support_state` | `event_id`, `open: true` | `open: false` or a missing `open` is `MsgFailed` and does not POST. |
| `message_status` | `message_status` | `event_id`, `status` of `delivered` or `read` | `message_ids` optional. Absent is valid only for `read`. Present and empty, more than 100 ids, or `status` `delivered` without ids, is `MsgFailed` and does not POST. `sending` is `MsgFailed` and does not POST. |

`to` is `msg.URN().Path()`. `from` is `msg.Channel().Address()`. `channel_uuid` is the channel UUID. `event_id` missing, empty, or longer than 128 characters is `MsgFailed` and does not POST. A valid id is 1 to 128 characters. `occurred_at_ms` must be a positive integer. These bodies are always privileged.

**Typing.** `sender.go` sends `typing_indicator` as today, on every channel. For `WWC` only, `typing_stop` uses that same `SendMsgAction` path and returns without a `MsgStatus`. `SendAction` maps `typing_indicator` to JSON `type: typing_start` and `typing_stop` to `typing_stop`. The nameless `typing_start` body stays `{"type":"typing_start","to":"<urn path>","from":"ai-assistant","channel_uuid":"<uuid>"}` with no `Authorization`, so `TestActions` still matches. A non-empty valid `agent_first_name` is added and the request becomes privileged. An invalid name returns an error and does not POST. A typing failure is still logged and not retried by mailroom: a missed indicator is not stored, and the widget's own 10-second hide is the recovery.

**Privileged POST.** `postPrivileged` uses `utils.MakeHTTPRequest` on `utils.GetHTTPClient` (the send context is the existing 35s `SendMsg` timeout). It does not use `http.DefaultClient`. Attempts: `wwcPrivilegedAttempts`. Backoff: `wwcPrivilegedBaseBackoff` shifted by the attempt index, with the same jitter shape as `utils.withJitter`. Retry 500, 429, 502, 503, 504, and transport errors. On `message_status` only, also retry HTTP 404, with the same attempt cap and backoff: the socket returns 404 when the shopper row is not committed yet, writes nothing, and the row appears in the same short window as `SaveHistory`. Do not retry 401, 403, 400, or 409 inside the request. A 404 on any other privileged type is not retried inside the request. `Idempotency-Key` is the `event_id` for control events and `msg.UUID()` for a privileged chat message, stable across the attempts of that call. Header `Authorization: Bearer <token>`. Content-Type `application/json`.

If the token config is empty, privileged work returns `MsgFailed` (or, for typing, an error) and does not open a connection.

Status after the privileged call:

| Socket result | Courier status | Mailroom |
|---------------|----------------|----------|
| 202 | `MsgSent` | done |
| 401, 400, 409 | `MsgFailed` | not retried |
| `message_status` 404, after the in-request attempts | `MsgErrored` | existing `E` schedule, then `F` on the third write |
| 403, and 404 on any other privileged type | `MsgErrored` | same schedule, with no in-request retry |
| 500, 429, 502, 503, 504, or a transport error, after the in-request attempts | `MsgErrored` | same schedule |
| Context deadline | `MsgErrored` | same schedule |

`403` uses only the mailroom schedule. The socket returns it while `WWC_HUMAN_SUPPORT_STATUS` is still false, and a tight loop cannot succeed until that flag changes. Engine emits the new actions only after the flag is on. A `message_status` 404 is retried inside the send because the missing row is the gap between the courier callback and the socket `SaveHistory`, which is milliseconds, not the 5-minute mailroom delay. The same `event_id` is sent again. Status only moves forward, so a replay does not rewind a tick. `401`, `400`, and `409` are defects in the token or the payload; another attempt with the same body cannot succeed.

The channel log is written from the last response. Before it is stored, the request dump replaces the bearer value with `Bearer [redacted]` and replaces the JSON string value of `agent_first_name` with `[redacted]`. Logrus fields on this path are `channel_uuid`, `msg_uuid`, `event_id` or `message_id`, `action_type`, `http_status`, and `socket_error` (the `error` code from a JSON error body, when present). They do not include the token, the agent name, the URN path, or the message text.

### Rejected Alternatives

- **A new Courier HTTP route that Engine calls.** The queue and `MsgErrored` retry already exist. A second ingress would need its own auth, timeout, and retry.
- **Courier calling Live Desk for transfer, read, or the name.** Those facts are Engine's. Courier only forwards the queue payload.
- **Minting `message_id` or `timestamp_ms` in Courier.** The socket mints the id on the callback and stamps milliseconds on the stored row. A second clock would disagree with the history the widget reloads.
- **Splitting a full name on the first space.** The socket rejects internal whitespace and does not split. Splitting would publish a token Engine did not choose and would hide a bad payload.
- **Defaulting a missing `author` to `ai_agent`.** Human-agent rows use the same `SendMsg`. A default would label them as AI when Engine omits the key.
- **Sending control events through the typing early-return.** That path drops the error and writes no status. A lost start or end would not be retried.
- **Teaching `utils.shouldRetryTransient` to retry HTTP 500 for every channel.** The constitution records that as its own change. This handler retries 500 only on the privileged WWC POST, which is idempotent on `event_id` or on the socket's forward-only status rank.

## Contracts

Wire values are snake_case JSON. New keys use `omitempty`, so a queued message without them produces the same `/send` body as today.

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| Outgoing queue `msgs_msg.metadata` keys `author`, `agent_first_name`, `event_id`, `occurred_at_ms`, `open`, `status`, `message_ids`; `action_type` values `human_support_started`, `human_support_ended`, `human_support_state`, `message_status`, `typing_stop` | added, optional | minor | yes — absent keys keep today's WWC body. Engine must not enqueue the new `action_type` values until this build is serving. An older Courier would POST those rows as chat `type: message`. |
| WWC `POST {base_url}/send` bodies for those types, plus top-level `author` and `agent_first_name` on chat messages, plus `Authorization: Bearer` on privileged bodies | added | minor | yes — nameless `typing_start` and unlabeled chat messages are unchanged and stay unauthenticated. |
| Inbound `POST /c/wwc/<uuid>/receive` optional `message.message_id` | added | minor | yes — absent or invalid is ignored; the message is still accepted. A valid value is stored in the existing `external_id` column. |
| `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN` | added | minor | yes — empty default. Privileged sends fail closed without a request. Unprivileged sends do not read it. |
| Redis queue name, Postgres `msgs_msg` columns, WhatsApp payloads, `COURIER_*` variables other than the token | unchanged | none | yes |

**Example control body** (`human_support_started`):

```json
{
  "type": "human_support_started",
  "to": "123",
  "from": "250788383383",
  "channel_uuid": "8eb23e93-5ecb-45ba-b726-3b064e0c568c",
  "event_id": "evt-1",
  "occurred_at_ms": 1730000000123
}
```

**Example chat body** when Engine set a human agent: the existing `moPayload` plus `"author":"human_agent"` and `"agent_first_name":"Julia"` at the top level, not inside `message`.

**Consumers affected**: Engine (must emit the metadata and must read inbound `external_id`), weni-webchat-socket (already accepts these bodies). Mailroom sees only the existing status values `S`, `E`, and `F`. WhatsApp is not a consumer of this contract.

## Data Model and Migrations

No new column, table, or index.

- **Inbound `msgs_msg.external_id`**: the socket `message.message_id` when it passes the length and whitespace rule. Empty when the callback has no id. The column already exists and is what Engine reads.
- **`msgs_msg.metadata`**: the new keys are stored by Engine before the row is queued. Courier reads them. It does not rewrite them.
- **Nothing else is persisted for this feature.** Milestones, delivery status, support id, and the agent name stay in the socket's Mongo collections. Typing is not stored here.

**Migrations**: none. The previously deployed Courier ignores unknown metadata keys. It does not read `message.message_id`, so a rollback loses only the new `external_id` population on messages received after the rollback. Rollback is safe for schema and unsafe for in-flight control events: see Rollout.

## Non-Functional Requirements

### Peak Load

Service-wide inbound peak, reused from specs 005 through 008 because Istio has no path label and this repo has no newer measurement:

- **242.9 req/s** over a 1-minute window, at 2026-10-28 14:06 UTC, from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}` for 2026-10-07 through 2026-11-06.

This feature does not add an inbound route. The added work is outbound, on top of the WWC `SendMsg` calls that already exist, during that same peak window:

- One privileged POST per shopper message that Engine marks `delivered`, and one privileged POST per agent open that marks the open support `read`.
- Two privileged POSTs per human support (start and end). `human_support_state` is the release path for a support already open, one POST, and it does not add a milestone.
- Typing POSTs are the ones Engine already queues, plus `typing_stop` when Engine sends it. They are not retried.

There is no fan-out beyond the contact named on the queued message, and no new connection. The privileged retry holds one sender goroutine for at most 3 attempts inside the existing 35s send context. That is not a new throughput class.

### Observability

Structured logrus fields only, on the privileged path and on an invalid inbound `message_id`: `channel_uuid`, `msg_uuid`, `event_id` or `message_id`, `action_type`, `http_status`, `socket_error`. An invalid callback id is logged as `message_id_rejected: true` without the raw value.

The ingress token, `agent_first_name`, the URN path, and the message text are not logrus fields and are not attached to Sentry. The channel log request dump redacts the bearer and the `agent_first_name` string before `WriteChannelLogs`. The socket error code is the correlation detail for a 4xx body. The message UUID plus `event_id` is the correlation id for a control event. The inbound correlation id is the stored `external_id` when it was accepted.

The contact UUID and the org id are not on `courier.Msg`. This path does not add a database lookup to fetch them. The message UUID is the row that already points at the contact. The URN is not used as a substitute.

### Failure Handling

- **Empty token on a privileged body**: `MsgFailed` for control events and chat sends, error return for typing. No HTTP call.
- **Invalid local payload** (bad `event_id`, bad `occurred_at_ms`, bad name, bad `author`, `open` not true, bad `message_status`): `MsgFailed` or a typing error. No HTTP call.
- **Socket 401, 400, 409**: `MsgFailed`. Not retried. Fix the token or the Engine payload.
- **`message_status` 404**: up to `wwcPrivilegedAttempts` tries inside the 35s send context, backoff `wwcPrivilegedBaseBackoff` with jitter. The socket has written nothing, and the same `event_id` is safe to send again. A 202 on a later try is `MsgSent`. If all tries return 404, `MsgErrored`. Mailroom then uses `next_attempt = NOW() + 5 * (error_count+1) minutes`, and the third `E` write becomes `F`.
- **Socket 403, and 404 on any other privileged type**: one request, then `MsgErrored` and the same mailroom schedule. No in-request retry. Start, end, and state stay idempotent on `event_id`.
- **Socket 500, 429, 502, 503, 504, transport error**: up to `wwcPrivilegedAttempts` tries inside the 35s send context, backoff `wwcPrivilegedBaseBackoff` with jitter. Then `MsgErrored` and the same mailroom schedule. The operation is idempotent as above. Chat message retries are the same POST; the socket treats a duplicate platform message as it does today.
- **Timeout**: the send context is 35s. The HTTP client timeout is 60s and does not outlive that context. Deadline exceeded is `MsgErrored`.
- **Typing failure**: logged, not written as `MsgErrored`. The indicator is not stored. The shopper sees no new error.
- **Inbound callback**: a bad `message_id` does not fail the webhook. The shopper message is still queued. Status updates for that id will not find a row; that is an Engine/socket problem, not a dropped shopper message.
- **Exhausted `F`**: the failure stays on `msgs_msg` and in the channel log. It is not deleted. The shopper is not given a new error string; the widget keeps the last status the socket confirmed.

Unprivileged chat sends keep today's `MakeHTTPRequestWithRetry` behavior, including its lack of an HTTP 500 retry. That gap is pre-existing and is not widened.

### Security and Authorization

- **Trust boundary**: the receive callback is the socket, and the queued metadata is Engine. Both are outside this process. `receiveMsg` checks `message_id` length and whitespace before `external_id` is written. `SendMsg` / `SendAction` check author, name, event id, time, `open`, status, and id-list length before any privileged POST.
- **Authorization**: the bearer is the server-side credential for the socket's privileged `/send`. Courier sends it only on privileged bodies. The socket compares it; Courier does not trust the queue payload to carry a token. An empty config fails closed. The value is a `COURIER_*` secret, never committed, never logged, and redacted in the channel log.
- **What stays unauthenticated**: nameless `typing_start`, chat sends with no `author` or with `author: ai_agent` and no name, and the receive route's existing channel-UUID dispatch. WhatsApp request signing is unchanged.
- **Personal data**: `agent_first_name` is forwarded as Engine sent it and is redacted in the channel log and omitted from logrus and Sentry. Courier does not store it. Read time is not present on these payloads and is not added.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| SC-001, Courier half: the transfer and close times that leave this process are the times Engine queued, and the shopper id that comes back on the callback is the id stored for a later status | `handlers/weniwebchat/weniwebchat_test.go` flow cases below. The widget comparison in SC-001 is webchat-react plus the socket history, not this repo. |

**Flow coverage**:

- Receive a text callback that includes `message.message_id` `3f1a8c0e-6b2d-4e5a-9c7d-1b0e4a6c8d20`. The queued incoming msg `ExternalID()` is that string. `RunChannelTestCases`.
- Receive the same callback without `message_id`. `ExternalID()` is empty. The existing text receive cases still pass.
- Receive a `message_id` of 65 characters or one that contains a space. The message is still accepted and `ExternalID()` is empty.
- Send a text msg with `metadata.author` `ai_agent` and no name. The POST body contains `"author":"ai_agent"`, has no `Authorization`, and has no `agent_first_name`. Status `MsgSent` on HTTP 202.
- Send a text msg with `author` `human_agent` and `agent_first_name` `Julia`. The POST is the existing text body plus those two top-level fields, header `Authorization: Bearer test-token`, status `MsgSent` on 202. The channel log request does not contain `test-token` or `Julia`.
- Send a product-list msg with the same human-agent metadata. `sendProductMessage` posts the bearer and the two fields on the product body.
- Send `action_type` `human_support_started` with `event_id` `evt-1` and `occurred_at_ms` `1730000000123`. The POST body is the example in Contracts, not a `type: message` body. Status `MsgSent` on 202. A second test with the socket body `{"message":"human support started","result":"replay"}` and 202 is also `MsgSent`.
- Send `human_support_ended` with a different `occurred_at_ms`. The POST carries that number unchanged.
- Send `human_support_state` with `open: true`. The POST contains `"open":true` and does not contain a chat `message` object.
- Send `message_status` `read` without `message_ids`, and `delivered` with one id. Each POST matches that shape and is `MsgSent` on 202.
- Send `typing_indicator` with no name. The body and the lack of `Authorization` match today's `TestActions` case.
- Send `typing_indicator` with `agent_first_name` `Julia` on WWC. The body is `typing_start` plus the name, with the bearer.
- Send `typing_stop` on WWC with and without a name. `SendAction` posts `type: typing_stop`. The branch that keeps `typing_stop` off `SendMsg` is `TestBestEffortAction` in `sender_test.go`: best-effort only when the channel type is `WWC`.

**Failure paths covered**:

- Privileged send with an empty token: no httptest request is received, status `MsgFailed`.
- `agent_first_name` `Julia Silva`, and a 101-rune name: no request, `MsgFailed`.
- `author` `ai_agent` with a name, `author` `shopper`, `human_support_state` with `open: false`, `message_status` `sending`, `message_status` `delivered` without ids, `event_id` of 129 characters, and `event_id` present and empty: no request, `MsgFailed`.
- httptest 401, 400, and 409: one request each, `MsgFailed`.
- httptest 403: one request, `MsgErrored`.
- `message_status` httptest 404 twice then 202: three requests, then `MsgSent`. `message_status` httptest 404 three times: three requests, then `MsgErrored`.
- httptest 500 twice then 202: three requests, then `MsgSent`. httptest 500 three times: three requests, then `MsgErrored`.
- Send context already cancelled or expired: `MsgErrored`, and the httptest hit count does not exceed 3.
- Nameless `typing_start` against a 500: the existing single `MakeHTTPRequest` error return, no bearer.

**Stubbed at the boundary**: the socket is `httptest`. No Postgres, Redis, or RabbitMQ in the handler tests. `RunChannelTestCases` / `RunChannelSendTestCases` / `RunChannelActionTestCases` in `handlers/test.go` are the seams. Cases that need the token construct the server the way `handlers/facebookapp` tests do and set `server.Config().WwcHumanSupportIngressToken`, because `RunChannelSendTestCases` does not take a config hook.

WhatsApp tests are not added. Those handlers are not edited, and an existing send case in `handlers/facebookapp` is not required to change.

## Rollout and Rollback

Deploy order:

1. weni-webchat-socket is already able to take these bodies. Its flag `WWC_HUMAN_SUPPORT_STATUS` stays false until step 3. Privileged calls while it is false return 403.
2. Deploy this Courier build with `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN` set to the same secret as `WWC_HUMAN_SUPPORT_INGRESS_TOKEN`. Engine does not enqueue the new `action_type` values yet. Ordinary WWC traffic is unchanged.
3. Turn the socket flag on and let Engine emit transfer, close, read, typing, and the first name together (BD-006). Courier has no second flag. A 403 during the flip is `MsgErrored` and is retried by the existing schedule.

Rollback:

1. Stop Engine from emitting the new metadata. In-flight control rows would be POSTed as chat messages by the previous Courier.
2. Then roll back the Courier image. No schema rollback. Inbound `external_id` values already written remain; the previous image simply stops filling new ones. The socket flag can be turned off independently; Courier then receives 403 on any privileged row still queued and those rows end as `E` and then `F`.
3. The previous image still serves unlabeled chat sends and nameless `typing_start`.

The release that contains the code change adds a minor entry to `WENI-CHANGELOG.md` (Principle VIII). `CHANGELOG.md` is not edited.

Done means a production WWC channel, with the token set and the socket flag on, forwarding a start, a shopper `message_id`, a `delivered` or `read`, a named human-agent message, a named typing event, and an end, with the statuses above. A merged pull request is not done.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| I. Reviewed changes on a protected main | The code change enters through a pull request that names this spec. | none |
| II. Secrets and supply chain | The ingress token is a `COURIER_*` secret with an empty default. It is redacted in the channel log and omitted from logrus and Sentry. No new dependency. | none |
| III. Structured, privacy-safe observability | New logs are logrus fields. URN path, message text, token, and agent name are not fields. The channel log redacts the bearer and the name. | none |
| IV. Versioned contracts | The queue metadata, the `/send` bodies, the receive field, and the env var are a minor, additive contract. Old bodies stay valid. | none |
| V. Specification traceability | This spec opens with the inheritance block and pins commit `59dabf0b030431fae835756a43aa7efd8d58f953`. The product file at that commit is clean and matches `origin/main`. There is no git tag on that commit. The product document's own status line is still Draft. | The pin is the full commit SHA, which this principle allows as an immutable version. The sentence that the product spec must be tagged before the engineering spec is created is not met. The developer pointed at this file, and the socket engineering spec already pins the same SHA. A tag on this commit, and a status other than Draft, are still required before this spec should be treated as mergeable against a ratified product spec. |
| VI. No silent divergence | Nothing inherited is contradicted. The status-mapping choices are implementation decisions. | none |
| VII. Conventional, atomic commits | Implementation commits use `feat:`, `test:`, and `docs:` with imperative subjects of at most 50 characters. | none |
| VIII. Release changelog | The shipping release adds a minor `WENI-CHANGELOG.md` entry. Upstream `CHANGELOG.md` is not edited. | none |
| IX. Never trust the client | Callback `message_id` and queued metadata are validated at the handler boundary before they are stored or forwarded. | none |
| X. Fail gracefully | Privileged calls use `utils.GetHTTPClient` and the 35s send context. Failures become `MsgFailed` or `MsgErrored`, not a panic and not a body that describes the token or the name. | none |
| XI. Bounded retry | Privileged 500, 429, 502, 503, 504, transport errors, and `message_status` 404 retry at most 3 times with backoff and jitter, on an idempotent body. 401, 400, and 409 are not retried. 403 is not retried inside the request; it uses the existing bounded `MsgErrored` schedule. Exhaustion remains on the row. | none |
| XII. Stateless, peak-sized services | No process memory and no new local disk. State stays in Postgres `external_id` and in the socket. Peak is the 242.9 req/s window above. | Per-route WWC peak is not in the Istio series. The service-wide peak is the declared ceiling, as in specs 005–008. The spool exception is not extended. |
| XIII. Diagnosable errors | Channel UUID, message UUID, and `event_id` or `message_id` are the context. | `courier.Msg` has no contact UUID or org id. This change does not add a lookup. The message UUID identifies the row that has the contact. The URN is not logged in its place. |
| XIV. Tests exercise flows | Receive, chat send, product send, each control type, and typing are handler flow tests, including the failure statuses. | none |
| XV. Explicit over clever | Attempt counts, backoff, and the length caps are named constants. No hidden split of the agent name and no inferred author. | none |
| XVI. Contained changes | Edits stay in the WWC handler, the WWC typing branch in `sender.go`, the action constants, the config field, and the WWC tests. `shouldRetryTransient` and WhatsApp handlers are not edited. | none |

## Implementation Decisions

- **Queue metadata, not a new route**: Engine keeps using the outgoing msg queue. Control facts are `metadata.action_type` plus the keys in Contracts. Chat facts are `metadata.author` and `metadata.agent_first_name` on a normal outgoing msg.
- **`message_id` is `external_id`**: one existing column, which Engine already knows how to read. An invalid id is dropped so a bad callback cannot fail the shopper message.
- **Courier does not stamp time or split names**: `occurred_at_ms` is copied through. `timestamp_ms` is the socket's stamp. A name the socket would reject is `MsgFailed` locally.
- **`message_status` 404 is retried inside the send**: three attempts, 500ms backoff with jitter, then `MsgErrored` if the row is still missing. The socket writes nothing on 404, and the same `event_id` is safe. The race is the gap between the courier callback and `SaveHistory`, so the first recovery stays inside the 35s send instead of the 5-minute mailroom delay.
- **403 stays on the mailroom schedule**: one request, then `MsgErrored`. A tight loop cannot succeed while the socket flag is false. 401, 400, and 409 are `MsgFailed`.
- **HTTP 500 retry is local to `postPrivileged`**: three attempts, 500ms backoff with jitter, then the mailroom schedule. The shared helper is unchanged.
- **Typing stays best-effort**: `typing_stop` on WWC joins the existing non-critical action path. Control events do not.
- **Channel log redaction is local to this handler**: the shared sanitizer is not changed. The bearer and the agent first name are replaced in the dump this handler stores.
- **No Courier feature flag**: the socket flag and the shared token are the switches. Empty token fails closed. Engine must not emit the new actions before step 3 of the rollout.

## Divergences

none
