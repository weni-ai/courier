# Engineering Specification: Record WAC Standby

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `[007-record-wac-standby]`

**Created**: 2026-11-13

**Status**: Draft

## Origin Without Product Spec

- Product Spec: none
- Problem: `field=standby` does not become conversation context. With `EnableWACStandbyContext` off, Courier logs `ignoring standby webhook` and drops the payload; with the flag on, it persists only a renderable `conversation_context`. Shopper messages, partner echoes, templates, and flows are ignored, and `httputil.DumpRequest` in `server.go` still stores the raw body on the channel log.
- Acceptance criteria:
  - **AC-01**: With `EnableWACStandbyContext` true, a `WAC` `field=standby` shopper message (`value.standby.messages`) appends one pending `wa_conversation_handover` line `[shopper] {text}` for a text body. The line is `context_type=history`. Courier does not call `WriteMsg`.
  - **AC-02**: With the flag true, a text message echo (`value.standby.message_echoes`, `message.type=text`) appends `[outro atendimento] {text}`. Courier does not call `WriteMsg`.
  - **AC-03**: With the flag true, a flow echo is one readable line under `[outro atendimento]`, built only from text already on the payload: interactive header text, body text, footer text, and `flow_cta`, joined in that order by ` — `, skipping empty parts. If none of those are set, the line is the sibling `flow.name`. If that is also empty, the line is `[outro atendimento] <flow>`.
  - **AC-04**: With the flag true, a template echo whose sibling `template` definition is present is rebuilt into one `context_text`: header media becomes a marker (`<image>`, `<video>`, `<document>`, `<audio>`); header and body `{{n}}` placeholders are replaced by the matching send-time text parameters (1-based, in array order); a `parameter_name` also replaces `{{parameter_name}}`; footer text is kept; each button contributes its label and, when it has a URL, the URL with the same substitution. Parts are separated by newlines inside that single append. The first line carries `[outro atendimento]`.
  - **AC-05**: With the flag true, a template echo without the sibling `template` object appends `[outro atendimento] template:{name} {values}` where `{values}` is the send-time parameter texts and media markers, in component order, separated by ` | `. No Graph fetch. A template with a name and no parameter values is `[outro atendimento] template:{name}`.
  - **AC-06**: With the flag true, media on a shopper message or an echo is a marker and nothing else: `<image>`, `<audio>`, `<video>`, `<document>`, `<voice>`, `<sticker>`. Courier does not call `resolveMediaURL` and does not download bytes. The origin marker is `[shopper]` or `[outro atendimento]` as for text.
  - **AC-07**: With the flag true, `value.standby.statuses` returns HTTP 200, appends no `context_text`, and does not call `WriteMsg` or `WriteMsgStatus`. It still applies `not_owner` under AC-08.
  - **AC-08**: With the flag true, every standby item that has a contact URN and a usable timestamp applies Redis state `not_owner` through the PR 335 Lua compare-and-set (`incoming <= stored` is `stale`; mode `set`, so a missing key is created). A standby timestamp older than or equal to the stored timestamp does not change `state` or `ts`. A newer timestamp sets `not_owner` and `ts`. Context lines are still appended when AC-01–AC-06 say so, even if the ownership write is `stale`.
  - **AC-09**: No standby path calls `WriteMsg`, `WriteMsgStatus`, or `WriteChannelEvent`. `LenQueuedMsgs` stays 0. Mailroom therefore does not start a flow, an agent turn, or a Live Desk attendance from this webhook.
  - **AC-10**: Shopper messages and echoes are deduplicated by message id in Redis for 24 hours. The key includes the channel UUID, so the same id on another channel is not the same key. A second delivery on the same channel does not append again. Status receipts are not deduplicated.
  - **AC-11**: If `WriteWAConversationHandover` returns an error for a standby line, Courier deletes that line's dedup key if it claimed one, and the webhook response is non-200. A later delivery of the same id is processed and can append. A nil return (row written, or the existing spool accepted the event) keeps the key.
  - **AC-12**: The channel log for a `field=standby` request does not contain the HTTP body, whether `EnableWACStandbyContext` is true or false. The wrapper's error and panic log lines that attach the dumped request use the same omitted body. A `field=messages` webhook still logs its body.
  - **AC-13**: With `EnableWACStandbyContext` false, Courier writes no standby context and does not change `wac:thread_owner:*` (the PR 335 standby rule). It still omits the body from the channel log (AC-12), returns HTTP 200, and does not call `WriteMsg`.

## Scope in This Repository

**This repository implements**:

- Parsing `value.standby` (`messages`, `message_echoes`, `statuses`) on `WAC` `field=standby` when `EnableWACStandbyContext` is true, and appending one history line per shopper message or echo through the existing `WriteWAConversationHandover` path.
- Origin prefixes `[shopper]` and `[outro atendimento]`, flow text, template hydration, and media markers, with no media download.
- `not_owner` updates for those items through `applyWACThreadOwner` (`set` + the existing timestamp rule), including status receipts that add no context line.
- A channel-scoped Redis dedup key, TTL 24 hours, claimed only around the context write and removed when that write returns an error.
- Omission of the standby HTTP body from the channel log and from the wrapper log lines that currently attach `DumpRequest`, independent of the flag.
- Flow tests and renderer tests listed in Test Strategy. The existing `handoverStandbyContextWAC.json` case stays, and its flag-on assertion grows the `not_owner` write from AC-08.

**Out of scope here**:

- Mailroom, RapidPro, and Live Desk. They keep consuming one pending `wa_conversation_handover` row. Courier does not call them.
- `field=messages`, `messaging_handovers`, `EnableWACSendOwnershipBlock`, and the Graph send path. A `not_owner` key blocks a send only when that flag is already on (spec 005).
- Downloading standby media, calling Graph to fetch a template that was not on the echo, and calling the Thread Control API.
- Channel types other than `WAC` for transcript lines and ownership (`WCD`, `FBA`, `IG`, on-premises `WA`). `WCD` shares `processCloudWhatsAppPayload`; this spec does not add transcript or `not_owner` writes there. Body omission still applies when that handler sees `field=standby`, because the dump is the same.
- A new `COURIER_*` flag, a schema change, and dedup of the legacy `conversation_context` summary (it has no message id; spec 006 already accepts a duplicate summary on redelivery).
- Changing `writeWAConversationHandover`'s existing `contact_urn` log on database error.

**Depends on**:

- Spec `005-wac-thread-ownership` (PR 335), in this tree: `applyWACThreadOwner`, `luaWACThreadOwnerApply`, `EnableWACStandbyContext`.
- Spec `006-accumulate-wac-context` (PR 336), in this tree: pending-row append in `upsertWAConversationHandoverSQL`, `persistRenderedWAHandover`.
- Meta standby webhooks as documented for `standby.messages`, `standby.message_echoes` (including sibling `template` and `flow`), and `standby.statuses`. Courier does not call Meta to fill gaps.

## Current State

- `handlers/facebookapp/facebookapp.go` `processCloudWhatsAppPayload` (from line 1116): `field=standby` (lines 1147–1168) either logs `ignoring standby webhook` and `continue`s when `EnableWACStandbyContext` is false, or calls `persistWAHandoverContext` and `continue`s. It never reads `messages`, `message_echoes`, or `statuses`, and it never calls `applyWACThreadOwner`. The messages loop and its `WriteMsg` (line 1456) are not reached for this field. `moPayload` unmarshals `value.messages`, `value.statuses`, and `value.conversation_context`. It has no `value.standby` field, so the nested object is discarded.
- `handlers/facebookapp/wa_handover.go` `persistWAHandoverContext` (lines 446–475) renders `conversation_context` via `renderWAHandoverContextText`. No render returns `"standby without conversation context"` and writes nothing. A render resolves the sender URN, then `persistRenderedWAHandover` calls `WriteWAConversationHandover`. A write error is returned and the webhook fails. `wacHandoverHistoryText` (lines 220–238) already formats lines as `[{role}] {content}` and non-text history as `<type>`.
- `handlers/facebookapp/testdata/wac/handoverStandbyContextWAC.json` is the only standby fixture: top-level `sender`, `timestamp`, and `conversation_context.summary`, no nested `standby` object. `TestReceiveWACStandbyContext` expects flag-off ignore with zero queued msgs and an empty Redis hash, and flag-on one summary event, zero queued msgs, and an empty Redis hash. Flag-on writer error expects HTTP 400.
- `handlers/facebookapp/wa_thread_owner.go`: key `wac:thread_owner:{channel_uuid}:{urn.Identity()}`, hash fields `state` and `ts`, TTL `wacThreadOwnerTTL` (24h). `luaWACThreadOwnerApply` returns `stale` when `incoming <= stored`. Mode `set` creates the key; mode `update_if_exists` does not. `control_taken` uses `set` + `not_owner`. `field=messages` uses `update_if_exists` + `owner`. Spec 005 recorded standby as not an ownership signal.
- `server.go` `channelHandleWrapper` (lines 323–377): `httputil.DumpRequest(r, true)` runs before the handler. Standby returns no events, so the wrapper writes `NewChannelLog("Request Ignored", ..., string(request), ...)`. The error and panic logrus lines (lines 345 and 359) attach the same dump. `channel_log.go` `sanitizeBody` keeps text bodies. `MockBackend.WriteChannelLogs` stores logs; `GetLastChannelLog` returns the last one. `TestHandlerRequest` hits this wrapper through `s.Router().ServeHTTP`.
- `backends/rapidpro/msg.go` `checkExternalIDSeen` / `writeExternalIDSeen` dedup a real `Msg` for about 24 hours in `seen:externalid:{date}`, keyed by channel UUID, URN, and external id, and compared to message text. Standby must not build a `Msg` to use it.
- `backends/rapidpro/wa_conversation_handover.go` `writeWAConversationHandover`: a database error is logged with `contact_urn` and the event is spooled; a successful spool returns nil. `WriteWAConversationHandover` bounds the call with `backendTimeout` (20s). The pending-row `ON CONFLICT` appends `context_text` and sets `context_type` to `history` when either side is `history`.
- `config.go`: `EnableWACStandbyContext` defaults false. No new flag is required.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `handlers/facebookapp/wa_standby.go` | added | Parse `value.standby`, render lines, claim dedup, apply `not_owner`, call `persistRenderedWAHandover`. Keeps the new rules out of the 5k-line handler file. |
| `handlers/facebookapp/wa_standby_test.go` | added | Renderer cases: text, flow fallbacks, template with and without structure, media markers. |
| `handlers/facebookapp/facebookapp.go` | modified | `moPayload` gains `Standby json.RawMessage`. The `field=standby` branch marks the body omitted, then either keeps today's ignore (flag off) or calls the new processor (flag on, `WAC` only). It still `continue`s before the messages loop. |
| `handlers/facebookapp/wa_handover.go` | unmodified caller contract | Transcript lines use `persistRenderedWAHandover` with `context_type=history`, a nil `conversation_context`, and a nil `controlPassed`. Legacy summary persist stays. |
| `server.go` | modified | A pointer in the request context lets the handler mark the body omitted. `channelHandleWrapper` replaces the dumped body before every `NewChannelLog` and before the error and panic `request` fields. |
| `handlers/facebookapp/facebookapp_test.go` | modified | Flow cases for AC-01–AC-13. `TestReceiveWACStandbyContext` flag-on expects `not_owner` as well as the summary. |
| `handlers/facebookapp/testdata/wac/` | added | Standby fixtures for shopper text, text echo, flow, template with structure, template without structure, image, audio, status, and a second delivery. |
| `handlers/facebookapp/wa_thread_owner.go` | unmodified Lua | Reuse `applyWACThreadOwner` with state `not_owner`, mode `set`, source `standby`. |

`courier.WAConversationHandoverEvent`, `Backend.WriteWAConversationHandover`, the Redis ownership script, and `config.go` stay as they are. No new `Backend` method. Dedup uses `RedisPool()` from the handler, the same way `applyWACThreadOwner` does.

### Flow

**Entry** (`POST /c/wac/receive` → `receiveEvent` → signature check → `processCloudWhatsAppPayload`). `WCD` uses the same function through `receiveDemoEvent` and is excluded below where noted.

1. On `change.Field == standby`, before the flag check, call `courier.MarkChannelLogBodyOmitted(ctx)` so flag-off, flag-on, and a later error all omit the body.
2. Flag **false**: keep today's log line `ignoring standby webhook`, append that info string, `continue`. No context write, no Redis ownership, no dedup key, no `WriteMsg`.
3. Flag **true** and channel type is not `WAC`: keep today's `persistWAHandoverContext` only (WCD must not gain transcript lines or `not_owner`). Then `continue`.
4. Flag **true** and channel type `WAC`:
   1. Unmarshal `value.standby` in `wa_standby.go`. Also keep the legacy `persistWAHandoverContext` call when `conversation_context` renders, **after** the item loop, so a failed summary write does not run before deduped lines, and a failed line returns before the summary is attempted. If there is no renderable legacy context, do not stop; items may still be present. Today's early return `"standby without conversation context"` applies only when there is nothing to append and no status to apply.
   2. For each shopper message and each echo, in payload order:
      1. Resolve the contact URN. Shopper: phone URN from `from` when set, otherwise `from_user_id` (same preference as the `field=messages` loop). Echo: phone URN from `message.to` when set, otherwise `message.recipient`. No URN: skip the item, log `wac_standby_skipped` with `channel_uuid` and `external_id` only, do not fail the webhook.
      2. Timestamp: the item `timestamp` when it parses as Unix seconds. Empty timestamp uses `entry.time`, including `0`, matching `parseWAHandoverOccurredOn`. A non-empty unparseable timestamp does not call the Lua script; the line's `OccurredOn` is `entry.time`.
      3. `applyWACThreadOwner` with `not_owner`, mode `set`, source `standby`, and that Unix timestamp, only when step 2 produced an item timestamp or an empty one fell back to `entry.time`. A non-empty unparseable timestamp skips this call. Redis error: return the error. No dedup key has been claimed yet, so Meta redelivery retries ownership and the line.
      4. Render the line (rules below). Empty render (blank text and no marker): do not append and do not claim a key. Ownership from step 3 still stands.
      5. Message id required. Empty id: skip the append (a redelivery could not be deduped) and log `wac_standby_skipped`.
      6. `SET wac:standby:seen:{channel_uuid}:{message_id} 1 NX EX 86400`. If the key already exists, skip the append and continue to the next item. Ownership already ran.
      7. `persistRenderedWAHandover` with `context_type=history`, the rendered text, contact name from `standby.contacts` via `wacHandoverContactName` when a contact matches the URN path, `OccurredOn` from step 2, nil context payload, nil `controlPassed`. Nil return: keep the key. Non-nil return: `DEL` the key and return the error.
   3. For each status: resolve URN from `recipient_id`, else `recipient_user_id`. Apply `not_owner` with the same timestamp rules. Do not render, do not claim a dedup key, do not call `WriteMsgStatus`. Missing URN skips that status without failing the webhook.
   4. Legacy `conversation_context`, if it still renders: `persistWAHandoverContext` as today. Its error still fails the webhook. It is not deduped.
5. The branch `continue`s. It does not enter `for _, msg := range change.Value.Messages`. It does not call `resolveMediaURL`, `WriteMsg`, `WriteMsgStatus`, or `WriteChannelEvent`.
6. HTTP 200 on success. Info strings are `standby context recorded`, `standby status recorded`, the existing `wa conversation handover persisted` when a legacy summary was written, or `ignoring standby webhook` when the flag is off. None of those strings include message text, template parameters, phone numbers, or URNs. The response is stored on the channel log too.

**Line rules** (one `context_text` per message or echo; internal newlines are part of that one append):

| Payload | Line |
|---------|------|
| Shopper or echo `type=text` | `[{origin}] {body}` with trimmed body. Blank body does not append. |
| `image`, `audio`, `video`, `document`, `voice`, `sticker` | `[{origin}] <type>` only. Caption, media id, and mime type are not added. |
| Flow echo | AC-03. |
| Template echo with sibling `template` | AC-04. Unmatched `{{n}}` stays as written. Header format `IMAGE`/`VIDEO`/`DOCUMENT`/`AUDIO` is the marker. Button line is `{label}` or `{label}: {resolved url}` when the URL is present. |
| Template echo without sibling `template` | AC-05. Text parameters contribute their text. Media parameters contribute the marker. Other parameter types contribute nothing but do not drop the name. |
| Any other shopper or echo type | If the payload already has a short title or body (button text, interactive reply title), use that text. Otherwise `[{origin}] <type>`. No download and no `WriteMsg`. |

`{origin}` is `shopper` for `standby.messages` and `outro atendimento` for `standby.message_echoes`.

**Channel log**: `channelHandleWrapper` stores `*channelLogBody` on the handler context before `handlerFunc`. `MarkChannelLogBodyOmitted` sets it. When set, the bytes after the header/body separator of the dump (`\r\n\r\n`) are replaced with `[standby body omitted]` before `NewChannelLog` and before the logrus `request` field on the error and panic paths. Request line and headers stay. A `field=messages` request does not set the flag, so `Hello World` from `helloWAC.json` remains in that log.

**Dedup key**: `wac:standby:seen:{channel_uuid}:{message_id}`. Named constants `wacStandbySeenKeyPrefix` and `wacStandbySeenTTL = 24 * time.Hour`. Value `1`. Not `seen:externalid:*` and not `wac:thread_owner:*`.

### Rejected Alternatives

- **Fall through into the `field=messages` loop**: that loop calls `WriteMsg` and `resolveMediaURL`. AC-06 and AC-09 forbid both.
- **Build a `Msg` and call `CheckExternalIDSeen`**: that API compares text, keys by URN, and exists to collapse real inbound msgs. Standby must not construct a msg. AC-09.
- **`update_if_exists` for `not_owner`**: a standby-first contact would stay unmarked and the send path would stay fail-open. Standby is sent only when this app is not the thread owner, so creating the key (`set`) matches `control_taken`. The timestamp rule, not the mode, is what stops an old standby from overwriting a newer `owner`.
- **Skip the context line when ownership returns `stale`**: a late copy of a real message would vanish. AC-08 keeps the line and only freezes `state`/`ts`.
- **SET the dedup key only after a nil write, with no claim before it**: two concurrent deliveries can both append. `SET NX` before the write, plus `DEL` on error, is one winner and still lets Meta's redelivery in after a failed write (AC-11).
- **Return HTTP 200 when the context write fails**: Meta would not redeliver and the line would be lost. The standby path stays fail-closed, which is what makes AC-11's retry happen.
- **Fetch the template from Graph when the sibling object is missing**: AC-05 is name plus the values already on the echo. No extra HTTP and no admin token on this path.
- **Store the raw standby JSON in `context_payload`**: that copies phone numbers and the body into Postgres beside `context_text`. The agent reads `context_text`. Payload stays nil for these lines.
- **A new `COURIER_*` flag**: the user kept `EnableWACStandbyContext` as the gate for context and for the new ownership write. Log omission is not gated.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| `POST /c/wac/receive` for `field=standby` | Flag off: still HTTP 200 and `ignoring standby webhook`. Flag on: HTTP 200 when lines and ownership succeed; non-200 when a context write or the ownership Redis call returns an error. Response info strings gain `standby context recorded` and `standby status recorded`. | patch | yes — Meta still keys off the status code. Flag default remains false. No new required request field. |
| `wa_conversation_handover` pending row | Flag on may append `history` lines (`[shopper]` / `[outro atendimento]`) through the existing conflict update. Legacy summary still appends. Schema, partial unique index, and `consumed_on` are unchanged. | patch | yes — Mailroom still reads one pending `context_text` and sets `consumed_on`. |
| Channel log `request` for a standby webhook | HTTP body replaced with `[standby body omitted]` before persist, flag on or off. | patch | yes — the log row is still written (`Request Ignored` or `Channel Error`). The body is no longer stored. No deprecation path: keeping the body is the leak this change removes. |
| Redis `wac:standby:seen:{channel_uuid}:{message_id}` | added, TTL 24h, Courier-internal | none | not a public contract under Principle IV. No other service reads it. |
| Redis `wac:thread_owner:*` | flag on may `set` `not_owner` from standby. Flag off does not write it from standby. | none | internal, same key as spec 005. Default off leaves the PR 335 standby behavior. |
| `COURIER_ENABLE_WAC_STANDBY_CONTEXT` | meaning, when true, grows from "persist renderable context" to "also append transcript lines and set `not_owner`". Default false. | minor | yes — off preserves context and ownership. On is opt-in and additive. Send blocking still requires `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK`. |

No breaking change, so no deprecation path. `EnableWACSendOwnershipBlock` and the Graph send URL are unchanged.

**Consumers affected**: Courier operators (channel log no longer shows standby bodies; turning the existing flag on now also records transcript lines and `not_owner`). Mailroom and RapidPro keep the current pending-row read. Meta still receives 200 on success and non-200 when a flagged standby write fails. Live Desk is not called.

## Data Model and Migrations

- **Pending context**: one unconsumed `wa_conversation_handover` row per `channel_id`+`contact_id`, already enforced by `uq_wa_conv_handover_pending`. Transcript lines set `context_type=history` and `context_text` to the rendered line. `context_payload` is nil on these writes. `contactForURN` inside the existing writer may insert the contact so the row has a `contact_id`. That insert does not queue a msg and does not open a Live Desk attendance.
- **Dedup**: Redis string `wac:standby:seen:{channel_uuid}:{message_id}`, value `1`, TTL 86400 seconds from the successful claim. Not in Postgres.
- **Ownership**: existing hash `wac:thread_owner:{channel_uuid}:{urn.Identity()}`, `state=not_owner`, `ts` = the applied Unix seconds, TTL refreshed only when the Lua script applies (not when `stale`).

**Migrations**: none. `chk_wa_conv_handover_context_type` already allows `history`. The previous Courier binary uses the same table and ignores the new Redis keys. A pending row that already contains transcript lines is a legal `history` row for that binary. Rollback does not need a down migration. Dedup and ownership keys expire within 24 hours.

## Non-Functional Requirements

### Peak Load

Same measured Courier inbound peak as specs 005 and 006, stated as a peak:

- **Service-wide inbound**: peak of **242.9 req/s** (1-minute rate) at 2026-10-28 14:06 UTC, from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}` over 2026-10-07 to 2026-11-06. Istio has no path label, so `/c/wac/receive` cannot be isolated. Per-route peak is this service peak.
- **Added work when the flag is on**, if every request at that peak were one standby message or echo: **242.9** Redis `EVAL`s/s on `wac:thread_owner:*`, **242.9** `SET NX`s/s on `wac:standby:seen:*`, and **242.9** pending-row upserts/s. A webhook with several items does that once per item. Statuses add the `EVAL` only. No Graph media GET and no new fan-out.
- **Flag off**: no new Redis command and no new Postgres write. The body replacement is in-process on the dump that already exists.
- No new process memory that outlives the request, and no new local-disk state. The existing spool path is unchanged.

### Observability

Structured `logrus` fields. Do not log `context_text`, template parameters, contact name, phone numbers, or URN paths (Principle III). HTTP info strings listed in the flow are the only request-scoped text added, and they do not include that data. The channel-log response is those strings, not the rendered line.

| Event | Level | When | Fields |
|-------|-------|------|--------|
| `wac_thread_owner_applied` | INFO (existing) | ownership Lua returns | existing fields: `event`, `channel_uuid`, `outcome`, `state`, `source=standby` |
| `wac_thread_owner_update_failed` | not used on this path | Redis error is returned to Meta instead of logged-and-continued | — |
| `wac_standby_context_write_failed` | ERROR (logrus Sentry hook) | `WriteWAConversationHandover` returns an error for a transcript line | `event`, `channel_uuid`, `external_id` (the message id). Error via `WithError`. No line text and no URN. |
| `wac_standby_skipped` | INFO | item skipped for missing URN, empty message id, or unparseable timestamp | `event`, `channel_uuid`, `external_id` when the id is present, `reason` (`no_urn`, `no_message_id`, `bad_timestamp`) |

**Correlation identifier**: `channel_uuid` plus `external_id` when the payload has a message id. A contact UUID is not known in the handler before the writer runs; it is not added.

Success of a line adds no log line. The legacy summary path keeps its current handover-received log; this spec does not add `context_text` to it. `writeWAConversationHandover` still logs `contact_urn` on a database error. This spec does not extend that line and does not remove it.

### Failure Handling

- **Timeout**: `WriteWAConversationHandover` already uses `context.WithTimeout(ctx, backendTimeout)` with `backendTimeout = 20s`. The inbound handler context remains 30s. Redis uses the existing pool, same as `applyWACThreadOwner`, with no new command deadline and no new HTTP client. `http.DefaultClient` is not used. Media is not fetched.
- **Ownership Redis error**: one attempt, return the error, HTTP non-200. The dedup key is not claimed before this call. Meta redelivers. The Lua script makes the retry idempotent (`stale` when `ts` was already applied).
- **Context write returns an error** (database and spool both failed): `DEL` the dedup key if this item claimed it, log `wac_standby_context_write_failed`, return the error. No Courier retry loop. Bound is Meta redelivery. The retry finds no key and appends (AC-11).
- **Database fails and the spool write succeeds**: `writeWAConversationHandover` returns nil. The dedup key stays. The ERROR event does not fire. Spool replay uses the same append SQL.
- **Duplicate delivery**: `SET NX` fails, the append is skipped, HTTP 200. Ownership CAS still runs and is `stale` for an equal timestamp.
- **Concurrent double delivery**: `SET NX` has one winner. If the winner then fails and deletes the key, a loser that already skipped is answered 200; the winner's non-200 is what Meta redelivers, and that retry can claim the key.
- **Flag off and status-only success**: HTTP 200, no write to fail.
- **Idempotency**: the dedup key is the idempotency key for transcript lines. The partial unique index remains the guarantee of one pending row. Legacy `conversation_context` on standby is still not deduped (spec 006). Status ownership is idempotent via the timestamp compare, not via the seen key.
- **Empty or non-rendering item**: no append, no key, HTTP 200.

### Security and Authorization

- **Trust boundary**: Meta `POST /c/wac/receive`. `receiveEvent` verifies `X-Hub-Signature` before `processCloudWhatsAppPayload`, except the existing `X-Router-Token` path, which this spec does not change. Standby lines and `not_owner` are taken only from that body.
- **Authorization**: the router resolves the channel from the UUID in the path and rejects an unknown channel before the handler. No new credential. The contact id is the one `contactForURN` resolves from the URN on the signed payload, not a client-supplied row id. Redis keys are internal.
- **Validation**: message id, URN, and timestamp are checked per item before a write. `context_type` written for these lines is `history`, which the existing check constraint allows. Template and flow text are rendered from the payload; unmatched placeholders are left in place rather than filled from anywhere else. Media ids are not sent to Graph.
- **Secrets**: none added. The WhatsApp admin token is not used on this path. The flag is not a secret.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| AC-01 | `TestReceiveWACStandbyTranscript` in `facebookapp_test.go`: signed `POST /c/wac/receive`, flag true, fixture `standbyShopperTextWAC.json` (`from=5678`, body `Test standby message`). HTTP 200, `LenQueuedMsgs()==0`, one `WAHandoverEvents()` entry, `ContextType=history`, `ContextText=[shopper] Test standby message`, `ContactURN` `whatsapp:5678`, `BackendCalls()` contains `wa_handover` and does not contain `msg`. |
| AC-02 | Same test, fixture `standbyEchoTextWAC.json` (`message.to=5678`, body `Hello! Your order #12345 has shipped.`). One line `[outro atendimento] Hello! Your order #12345 has shipped.`, zero queued msgs. |
| AC-03 | `wa_standby_test.go` table: the documented flow payload renders `Book Appointment — Schedule your visit with us. — Tap below to continue — Book Now`; header-only keeps that header; empty texts with `flow.name` render the name; empty texts and empty name render `<flow>`. One flow test posts `standbyEchoFlowWAC.json` and asserts the full line and zero queued msgs. |
| AC-04 | Renderer test and fixture `standbyEchoTemplateWAC.json` (the documented `summer_sale_2026` echo). `ContextText` is `[outro atendimento] <image>\nHi Maria, enjoy 25% off!\nLimited time\nShop Now: https://example.com/summer2026`. A body placeholder with no parameter stays `{{n}}`. |
| AC-05 | Fixture `standbyEchoTemplateNoStructureWAC.json`: same send-time components, no sibling `template`. Line `[outro atendimento] template:summer_sale_2026 <image> \| Maria \| 25% \| summer2026`. Name only, when the components array is empty: `[outro atendimento] template:summer_sale_2026`. |
| AC-06 | Fixtures `standbyShopperImageWAC.json` and `standbyShopperAudioWAC.json`. Lines `[shopper] <image>` and `[shopper] <audio>`. HTTP 200 with no media `httptest` server running, so a `resolveMediaURL` call would fail the test. An echo image is `[outro atendimento] <image>`. |
| AC-07 | Fixture `standbyStatusWAC.json` (`recipient_id=5678`, `status=delivered`). HTTP 200, `WAHandoverEvents()` empty, `GetLastMsgStatus` returns an error, `LenQueuedMsgs()==0`, Redis `state=not_owner`. |
| AC-08 | In `TestReceiveWACStandbyTranscript`: seed `owner` at ts `200`, post a shopper message with timestamp `100`, assert Redis stays `owner` / `200` and the line is still appended. Seed nothing, post timestamp `150`, assert `not_owner` / `150` (`created`). Seed `owner` at `100`, post timestamp `150`, assert `not_owner` / `150`. Post a second message id with timestamp `150` and assert `state` and `ts` stay `not_owner` / `150` while that second line is still appended. `TestReceiveWACStandbyContext` flag-on (`handoverStandbyContextWAC.json`) now also asserts `not_owner` for sender `5678` at `1454119029`. |
| AC-09 | Every standby case above asserts `LenQueuedMsgs()==0`, `BackendCalls()` without `msg`, `GetLastMsgStatus` error, and `GetLastChannelEvent` error. |
| AC-10 | Post `standbyShopperTextWAC.json` twice on `testChannelsWAC[0]`. One handover event after the second post. Redis `TTL` of `wac:standby:seen:{channel_uuid}:{message_id}` is in `(0, 86400]`. The same body posted to a second WAC channel appends one event on that channel. A status fixture does not create a `wac:standby:seen:*` key. |
| AC-11 | `SetErrorOnWAHandover(true)` on the shopper fixture: HTTP 400, zero events, the seen key is absent (`EXISTS` 0). Clear the error flag and post the same body: HTTP 200 and one line. The legacy summary writer-error case in `TestReceiveWACStandbyContext` stays HTTP 400. |
| AC-12 | After the flag-off and flag-on shopper posts, `GetLastChannelLog().Request` contains `[standby body omitted]` and does not contain `Test standby message` or `5678`. Force the writer error and assert the logrus `request` field and the channel log also omit the body. `helloWAC.json` (`field=messages`) still has `Hello World` in `GetLastChannelLog().Request`. |
| AC-13 | `TestReceiveWACStandbyContext` flag-off: HTTP 200, `ignoring standby webhook`, zero handover events, zero queued msgs, Redis hash unchanged when pre-seeded `owner` at ts `200`. `GetLastChannelLog().Request` contains the placeholder and not the summary text. |

**Flow coverage**: signed `POST /c/wac/receive` with `field=standby` goes through `channelHandleWrapper` and `processCloudWhatsAppPayload` to a pending-context event and/or a Redis hash, with zero queued msgs. Shopper text, text echo, flow, both template shapes, image, audio, and status each have one such request. Flag off is the same request with the flag left at its default.

**Failure paths covered**: `SetErrorOnWAHandover(true)` returns HTTP 400, writes no line, and leaves the seen key absent, then the same body succeeds after the flag is cleared. Closing the mock Redis pool and posting the shopper fixture returns HTTP 400 (`WriteError` maps a returned error to 400) and writes no handover event. An item timestamp that is not an integer leaves a seeded `owner` unchanged and still appends the line with `OccurredOn` from `entry.time`. Blank text does not append. `WCD` with the flag on and `standbyShopperTextWAC.json` queues nothing and writes no handover event. Missing `X-Hub-Signature` stays the existing 401 from `receiveEvent`.

**Stubbed at the boundary**: Meta is the fixture body plus `addValidSignatureWAC`. Handler tests use `courier.MockBackend` and its real local Redis. No live Cloud API, no media GET, and no Mailroom process.

## Rollout and Rollback

Deploy Courier only. No Mailroom or RapidPro release. `EnableWACStandbyContext` stays false at deploy, so transcript lines and standby `not_owner` do not start. Body omission starts immediately for `field=standby`.

1. Deploy with the flag false. Confirm channel logs for standby show `[standby body omitted]` and that `field=messages` logs still contain the inbound body. Confirm `control_passed` / `control_taken` ownership is unchanged.
2. Enable `COURIER_ENABLE_WAC_STANDBY_CONTEXT` when transcript context is wanted. `not_owner` writes start with it. Sends are blocked only if `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` is also true and the key is `not_owner`.
3. Ship a `WENI-CHANGELOG.md` entry on the release tag: standby transcript and `not_owner` behind the existing flag, and standby body omitted from the channel log. `CHANGELOG.md` (upstream) stays untouched.

**Rollback**: redeploy the previous Courier tag, or turn the flag off. The flag off stops new lines and new standby ownership writes; it does not restore bodies into future logs until the previous binary is deployed. The previous binary ignores `wac:standby:seen:*` and any `not_owner` keys this version created; those keys expire within 24 hours. Pending rows already appended stay valid `history` text. No schema migration to reverse. Turning the flag off on this binary is the immediate stop for context and ownership without a redeploy; log omission remains until rollback of the binary.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| II. Secrets and Supply Chain | No new secret and no new `COURIER_*` variable. The admin token is not used. | none |
| III. Structured, Privacy-Safe Observability | New logs use `channel_uuid`, `external_id`, `outcome`, `reason`. The standby body is removed from the channel log and from the wrapper's `request` field. Rendered text is not logged. | `writeWAConversationHandover` already logs `contact_urn` on database error. This change does not extend that line. Removing it is a separate Principle XVI change, as specs 005 and 006 left it. |
| IV. Versioned Contracts | Webhook and channel-log changes are patch. The flag's opt-in behavior is minor and default off. Redis keys are internal. | none |
| V. Specification Traceability | Opens with `## Origin Without Product Spec` in the mandated format. Acceptance criteria are the origin of the change. | none |
| VI. No Silent Divergence | No Product Spec, so nothing is inherited to diverge from. | none — principle applies only to derived specs |
| VIII. Release Changelog | `WENI-CHANGELOG.md` entry on the shipping tag. Upstream `CHANGELOG.md` is not edited. | none |
| IX. Never Trust the Client | Lines and `not_owner` are taken only after the signature check, from the parsed standby object. Missing URN or id does not write. | `X-Router-Token` keeps today's signature skip (unchanged, Principle XVI). |
| X. Fail Gracefully and Predictably | Writer and ownership errors return to Meta. No new panic path. Media is not fetched, so there is no unbounded download. | none |
| XI. Bounded Retry Over REST | No new REST hop. A failed context write deletes the dedup key and returns non-200 so Meta redelivers once per its own schedule. The seen key makes the successful line idempotent. No unbounded Courier retry loop. | none |
| XII. Stateless, Peak-Sized Services | Dedup and ownership live in Redis with TTL. Pending text lives in Postgres. Peak declared above as 242.9 req/s over the stated window. No new local-disk state. | Per-route peak is unavailable (Istio has no path label); the service-wide peak is the declared peak, as in specs 005 and 006. The existing spool exception is not extended. |
| XIII. Diagnosable Errors | Sentry fields on the new ERROR: `channel_uuid`, `event`, `external_id`. | No contact UUID in the handler before the writer. Channel UUID is the account locator; `external_id` is the correlation id. URN and line text must not be attached. |
| XIV. Tests Exercise Flows | Webhook → context event and/or Redis, including flag off, writer failure then retry, stale timestamp, and the channel log. Renderer tests cover the template and flow variations. | none |
| XV. Explicit Over Clever | Named constants for the seen-key prefix, 24h TTL, origin labels, log placeholder, and the ` — ` / ` \| ` separators. The Lua `<=` rule is the existing script, not a second compare in Go. | none |
| XVI. Contained Changes | Standby branch, a new file for the renderer and dedup, the log-omission hook, fixtures, and tests. No `field=messages` change, no send-path change, no FBA/IG transcript, no Mailroom edit. | `TestReceiveWACStandbyContext` flag-on gains a `not_owner` assertion because AC-08 applies to that payload too. The test is updated; the messages and handover tests are not. |

## Implementation Decisions

- **`not_owner` uses mode `set`**, source `standby`, state `not_owner`. The PR 335 ordering rule is the existing Lua script: `incoming <= stored` returns `stale` and does not refresh TTL. Flag off does not call it, which is the PR 335 standby rule.
- **Context is appended even when ownership is `stale`.** The timestamp rule protects `state` and `ts` only.
- **URN preference** matches the inbound path: shopper phone (`from`) over BSUID (`from_user_id`); echo `message.to` over `message.recipient`; status `recipient_id` over `recipient_user_id`. The Redis ownership key is that URN's `Identity()`, so it is the same key the send path reads.
- **One append per message id**, `context_type=history`, nil `context_payload`. The 006 conflict update concatenates and promotes the row to `history`.
- **Dedup claim sits after a successful ownership call and before the context write.** `SET NX EX 86400`. Error from the write deletes the key. Nil (including spool success) keeps it. Statuses never claim it. An empty message id never claims it and does not append.
- **Legacy `conversation_context` remains** and runs after the item loop. It is not given a synthetic message id. Flag-on writer error on that path stays HTTP 400.
- **Unparseable item timestamp** does not enter the Lua script. The line still appends with `OccurredOn=entry.time` when there is an id and a URN. Empty timestamp uses `entry.time` for both the line and the Lua script.
- **Template substitution is 1-based and positional**, plus `{{parameter_name}}` when the parameter has that field. No Graph call. Media parameters are markers, not ids.
- **Flow separator is ` — `** (space, em dash, space). Template-without-structure separator is ` | `. Button with a URL is `{label}: {url}`.
- **Body omission is a pointer on the handler context**, set by `MarkChannelLogBodyOmitted` at the start of the standby branch. `server.go` does not parse WhatsApp JSON. Headers stay; the body is `[standby body omitted]`.
- **WAC only for lines and `not_owner`.** `WCD` keeps today's flag-gated `persistWAHandoverContext` and gets body omission because it shares the branch that sets the flag.
- **Contact insert inside `WriteWAConversationHandover` is allowed.** The pending row needs `contact_id`. It is not `WriteMsg` and it does not open an attendance.

## Open Questions

None.
