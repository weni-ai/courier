# Engineering Specification: Accumulate WAC Context

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `[006-accumulate-wac-context]`

**Created**: 2026-11-12

**Status**: Draft

## Origin Without Product Spec

- Product Spec: none
- Problem: The pending `wa_conversation_handover` row replaces `context_text` on conflict, so a later write drops the earlier summary or history, and `field=messages` unmarshals `conversation_context` but never persists it before `WriteMsg`.
- Acceptance criteria:
  - **AC-01**: When a pending `wa_conversation_handover` row already exists (`consumed_on IS NULL`) for the same `channel_id`+`contact_id`, a new `WriteWAConversationHandover` appends the new `context_text` as a new line after the stored text. It does not replace it. `context_type` becomes `history` if either side is `history`; otherwise it stays `summary`. Truncate the concatenated `context_text` with the same 32000-rune tail rule as `truncateWAHandoverContextText` in `handlers/facebookapp/wa_handover.go` (keep the most recent runes, prefix `[truncated]` when the combined text is longer than the cap).
  - **AC-02**: If there is no pending row, behaviour stays an INSERT (same unique `uq_wa_conv_handover_pending`). A row with `consumed_on` set is not appended to; a new pending row is inserted as today.
  - **AC-03**: `TestWriteWAConversationHandoverUpsertsPending` is rewritten to expect concatenation (`First summary\n[user] hi`), not replacement. Add a case for insert-when-previous-was-consumed.
  - **AC-04**: On `field=messages` for channel type `WAC`, if `change.Value.ConversationContext` renders via `renderWAHandoverContextText`, Courier calls `WriteWAConversationHandover` (same event shape as `persistWAHandoverContext`: `ChannelUUID`, `ContactURN` from the message sender, `ContactName`, `ContextType`, `ContextText`, `ContextPayload`, `OccurredOn` from `msg.timestamp`) before `Backend.WriteMsg`, so Mailroom can consume the pending row on this inbound. Reuse `persistWAHandoverContext` or extract a shared helper; do not duplicate render or contact-name logic.
  - **AC-05**: If `conversation_context` is absent or does not render, the messages loop is unchanged (no extra `WriteWAConversationHandover`, existing `WriteMsg` path). This is the inbound-without-prior-context case and must keep working.
  - **AC-06**: `WriteWAConversationHandover` failure on the messages path must not drop the inbound message: log ERROR with `channel_uuid` and `event=wac_pending_context_write_failed`, then continue `WriteMsg` (same fail-open idea as 005’s Redis `update_if_exists` on messages). Handover and standby paths keep today’s behaviour: a persist error still fails the webhook so Meta redelivers.
  - **AC-07**: No new `COURIER_*` flag. `control_passed` persist stays ungated (1.68.0 / 005). Standby persist stays behind `EnableWACStandbyContext`. `field=messages` `conversation_context` persist runs whenever the payload has renderable context (same as `control_passed`: the numbers that receive it are the routed ones).
  - **AC-08**: No schema migration. Do not change `chk_wa_conv_handover_context_type`, `uq_wa_conv_handover_pending`, or column types. Concatenation happens in the writer SQL (or in Go immediately before `NamedExec`) so RapidPro and Mailroom keep consuming one pending row as they do today. Mailroom is out of this repository and must not change.
  - **AC-09**: `field=standby` message copies (`change.Value.Messages` / `Statuses`) remain ignored. This spec only changes how existing `WriteWAConversationHandover` writes compose, plus `conversation_context` on `field=messages`.

## Scope in This Repository

**This repository implements**:

- Pending-row composition in `backends/rapidpro/wa_conversation_handover.go`: append `context_text`, `history`-if-either `context_type`, 32000-rune tail truncation, latest `context_payload`, and the metadata / freshness column rules in Implementation Decisions.
- A `WAC` `field=messages` hook in `handlers/facebookapp/facebookapp.go` that persists renderable `change.Value.ConversationContext` before `WriteMsg`, fail-open on writer error.
- A shared persist helper in `handlers/facebookapp/wa_handover.go` so `persistWAHandoverContext`, the `control_passed` branch, and the messages hook share render and contact-name lookup.
- Backend and handler tests listed in Test Strategy, including rewriting `TestWriteWAConversationHandoverUpsertsPending`.

**Out of scope here**:

- Redis `wac:thread_owner:*` (spec `005-wac-thread-ownership`).
- `EnableWACSendOwnershipBlock` and Graph error `131070`.
- Ingesting `field=standby` shopper text, partner echo, template echo, or media as transcript lines. A later spec owns that. Copies stay ignored, as 005 already requires.
- Accumulating Courier’s own inbound or outbound `msgs_msg` history into the buffer.
- Channel-log redaction of standby bodies.
- RapidPro Celery trim of consumed rows, and RapidPro `failed_reason`.
- Mailroom `consumeHandoverContext` formatting (`{text}; Context: {context_text}`). Mailroom (other repository) keeps selecting one pending row and setting `consumed_on`.
- Channel types other than `WAC` (`WCD`, `FBA`, `IG`, on-premises `WA`).
- A new `COURIER_*` flag, and any change to `chk_wa_conv_handover_context_type`, `uq_wa_conv_handover_pending`, or column types.

**Depends on**:

- Spec `005-wac-thread-ownership`, already in this tree: `persistWAHandoverContext`, `renderWAHandoverContextText`, `WriteWAConversationHandover`, and the unique pending row.
- Mailroom (other repository) already `SELECT`s the pending row and sets `consumed_on` on inbound. No contract change while Courier still writes one pending row; `context_text` may be longer.
- Meta `conversation_context` on `messages` and `messaging_handovers` webhooks. Courier does not call a Meta API to fetch context.

## Current State

- `backends/rapidpro/wa_conversation_handover.go` `upsertWAConversationHandoverSQL` (lines 15–35) inserts into `wa_conversation_handover` and on `ON CONFLICT (channel_id, contact_id) WHERE consumed_on IS NULL` sets `context_text = EXCLUDED.context_text` (and the same replacement for `context_type` and `context_payload`). A second write for the same pending pair replaces the first. `previous_owner_*` and `handover_metadata` are also replaced, so a follow-up write that leaves them unset stores NULL over a `control_passed` value. `occurred_on` and `created_on` already take the new event.
- `writeWAConversationHandover` (lines 52–59) logs `contact_urn` identity on a database error, then spools the event. `WriteWAConversationHandover` in `backends/rapidpro/backend.go` (lines 581–586) bounds the call with `backendTimeout` (20s, line 37). Spool replay goes through `flushWAConversationHandoverFile` → `writeWAConversationHandoverToDB`, so it uses the same SQL.
- `backends/rapidpro/wa_conversation_handover_test.go` `TestWriteWAConversationHandoverUpsertsPending` (lines 40–59) writes `"First summary"` then `"[user] hi"` and asserts the pending row’s `context_text` is only `"[user] hi"`.
- `backends/rapidpro/schema.sql` (lines 157–177) defines `wa_conversation_handover` with `context_text text NOT NULL`, `CONSTRAINT chk_wa_conv_handover_context_type CHECK (context_type IN ('history', 'summary'))`, and `CREATE UNIQUE INDEX uq_wa_conv_handover_pending ON wa_conversation_handover (channel_id, contact_id) WHERE consumed_on IS NULL`.
- `handlers/facebookapp/wa_handover.go`: `maxWAHandoverContextTextLen = 32000` (line 25). `truncateWAHandoverContextText` (lines 220–227) returns the text when `utf8.RuneCountInString` is at most 32000, otherwise `"[truncated]"` plus the last 32000 runes (the prefix sits outside the cap; `TestTruncateWAHandoverContextText` asserts that length). `renderWAHandoverContextText` truncates each rendered piece before the write. `persistWAHandoverContext` (lines 425–473) is the standby writer: render, resolve sender URN, contact name from `Contacts` where `WaID` or `UserID` equals `urn.Path()`, marshal `ContextPayload`, `WriteWAConversationHandover`, and return the error so the webhook fails. The `control_passed` branch in `processMessagingHandover` (lines 383–420) builds the same event, adds `previous_owner_*` / `handover_metadata`, logs via `logMessagingHandoverReceived`, and also returns the write error.
- `handlers/facebookapp/facebookapp.go` `moPayload` already unmarshals `change.Value.ConversationContext` (line 317) into `*wacConversationContext`. `processCloudWhatsAppPayload` (line 1124):
  - `field=standby` (lines 1155–1182) returns before the messages loop. With `EnableWACStandbyContext` it calls `persistWAHandoverContext` and never `WriteMsg`. Copies in `Messages` / `Statuses` are not read.
  - `field=messaging_handovers` (lines 1185–1207) calls `processMessagingHandover` and returns.
  - Any other field, including `field=messages`, falls through to `for _, msg := range change.Value.Messages` (line 1230). The loop copies `msg.Context` (quote/reply) into message metadata (lines 1455–1464) and calls `WriteMsg` (line 1466). It never reads `change.Value.ConversationContext`.
  - `WAC` `field=messages` already fail-opens the Redis ownership update (lines 1281–1291, spec 005) before the message is built.
- `receiveDemoEvent` (line 1042) and the `X-Router-Token` branch also call `processCloudWhatsAppPayload`, so `WCD` shares this loop. The new persist must be gated on `channel.ChannelType() == "WAC"` inside that loop.
- `courier.MockBackend.WriteWAConversationHandover` (`test.go`) always succeeds and appends to a slice exposed as `WAHandoverEvents()`. `SetErrorOnQueue` exists for `WriteMsg`; there is no equivalent for the handover write. Handler tests use `handlers.CaptureLogs` and `handlers.TestHandlerRequest` against `/c/wac/receive`.
- Spec `005-wac-thread-ownership` records Redis ownership and, when `EnableWACStandbyContext` is on, persists renderable context from `field=standby`. It left the schema and the replace-on-conflict writer unchanged, and it does not read `conversation_context` on `field=messages`.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `backends/rapidpro/wa_conversation_handover.go` | modified | Append and truncate inside the existing `ON CONFLICT DO UPDATE` so one statement keeps both texts. |
| `backends/rapidpro/wa_conversation_handover_test.go` | modified | Replacement assertion becomes concatenation; consumed-row, truncation, payload, and metadata cases. |
| `handlers/facebookapp/wa_handover.go` | modified | One helper builds the event (render, contact name, payload) and calls `WriteWAConversationHandover`. Standby and `control_passed` keep returning that error. |
| `handlers/facebookapp/facebookapp.go` | modified | `WAC` `field=messages` calls the helper before `WriteMsg` when context renders; log and continue on error. |
| `handlers/facebookapp/facebookapp_test.go` | modified | Messages fixtures with and without renderable context; writer-failure still queues the msg. |
| `handlers/facebookapp/testdata/wac/` | added | `field=messages` body with `conversation_context.summary`, based on `helloWAC.json`. |
| `test.go` | modified | Test-only `SetErrorOnWAHandover` (same shape as `SetErrorOnQueue`) and a call-order record so the flow test can see handover-then-`WriteMsg`. |

`courier.WAConversationHandoverEvent`, `Backend.WriteWAConversationHandover`, and `config.go` stay as they are. No new Redis key.

### Flow

**Pending buffer (every caller of `WriteWAConversationHandover`)**:

1. `writeWAConversationHandoverToDB` still resolves the channel and contact, then `NamedExec`s one INSERT.
2. No pending row for that `channel_id`+`contact_id`: the INSERT lands. A consumed row does not match the partial unique index, so this INSERT is a new pending row and the consumed row is left as-is.
3. A pending row exists: `ON CONFLICT DO UPDATE` runs in that same statement.
   - `context_text` becomes the stored text, then a single newline (U+000A), then the new text. When the stored text is `''`, the result is the new text with no added newline. When the new text is `''`, the stored text is kept.
   - The combined string is then truncated with the handler rule: `char_length` ≤ 32000 keeps it; otherwise store `'[truncated]' || right(combined, 32000)`. The prefix is outside the 32000, matching `truncateWAHandoverContextText`. Use `char_length` / `right` (characters), not `octet_length`.
   - `context_type` is `history` when the stored type or the new type is `history`; otherwise `summary`. The result stays inside `chk_wa_conv_handover_context_type`.
   - Column rules in Implementation Decisions apply to the other UPDATE assignments.
4. Database error still logs and spools as today. Spool flush uses the same SQL, so a replayed event appends to whatever pending row exists at flush time, or inserts when that row was consumed.

**`field=messages` (new hook)**:

1. Entry is unchanged: `POST /c/wac/receive` → signature check in `receiveEvent` (or the existing `X-Router-Token` skip) → `processCloudWhatsAppPayload`.
2. Standby and `messaging_handovers` still `continue` before the messages loop. Their persist errors still fail the webhook.
3. For `channel.ChannelType() == "WAC"` and `change.Field == "messages"`, render `change.Value.ConversationContext` once per change through `renderWAHandoverContextText`. The context object lives on the change, not on the message.
4. When render returns not ok (nil, empty summary, empty history), the loop body is the code that exists today, including the 005 Redis `update_if_exists`.
5. When render returns ok, then for each message that reaches `WriteMsg` (not the `continue` paths for `type=unsupported` and `type=reaction`), call the shared helper immediately before `h.Backend().WriteMsg`. URN is the `urn` already resolved for that message (phone URN when both `from` and `from_user_id` are present). `OccurredOn` is the `date` already parsed from `msg.Timestamp`. Contact name comes from the shared contact lookup, not a second map walk. `ContextPayload` is `json.Marshal` of the conversation context. `previous_owner_*` and `handover_metadata` stay empty on this path.
6. If the helper’s `WriteWAConversationHandover` returns an error, log the failure described in Observability and continue into `WriteMsg`. Do not call `handlers.WriteAndLogRequestError` for that error.
7. `WriteMsg` failure still returns the error, as today.

`WCD` reaches `processCloudWhatsAppPayload` through `receiveDemoEvent`. The channel-type gate keeps `WCD` on today’s messages path.

### Rejected Alternatives

- **Read the pending row in Go, concatenate, then `NamedExec`**: two concurrent webhooks can both read the same text and drop a line. The conflict target already serializes the write; the append belongs in the `DO UPDATE` expression. AC-08 allows either place; the single statement is the one that keeps both texts.
- **A new pending row per write, or a child table of context lines**: breaks `uq_wa_conv_handover_pending` and forces Mailroom to change how it selects one buffer. AC-08 forbids that.
- **Merge `context_payload` JSON**: the agent reads `context_text`. The latest payload is the source of the newest line; merging blobs has no reader in this repository.
- **A new `COURIER_*` flag around the messages hook**: AC-07. `control_passed` already persists without a flag; messages context is the same class of routed-number payload.
- **Fail the messages webhook when the context write fails**: Meta would redeliver and `WriteMsg` would run again. 005 already fail-opens the Redis update on this path for that reason.
- **Call `persistWAHandoverContext` unchanged from the messages loop**: it resolves `value.Sender` and `value.Timestamp` / `entry.time`, and on error it fails the request via `WriteAndLogRequestError`. Messages need the message URN, `msg.timestamp`, and fail-open. The shared helper is the part both paths call.
- **Ingest standby `messages` / `statuses` as lines**: AC-09. That is the next spec.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| `wa_conversation_handover` pending row consumed by Mailroom | `context_text` on conflict is appended and tail-truncated; `context_type` is `history` if either side is `history`; `context_payload` is the latest JSON; owner metadata is preserved unless the new event sets it | patch | yes — one pending row, same columns, same partial unique index. Mailroom still reads `context_text` and sets `consumed_on`. A longer string still fits `{text}; Context: {context_text}`. |
| `POST /c/wac/receive` for `WAC` `field=messages` | When `conversation_context` renders, Courier writes the pending row before the inbound msg. HTTP status stays 200 when that write fails and `WriteMsg` succeeds. Response body for a normal inbound stays the message-receive data. | patch | yes — a payload without renderable context keeps today’s loop. No new route, no new request field required from callers. |
| `Backend.WriteWAConversationHandover` | signature unchanged; RapidPro `ON CONFLICT` assignment changes as above | patch | yes — callers still pass `WAConversationHandoverEvent`. The mock still records the event. |

No breaking change, so no deprecation path. `COURIER_*` and Redis `wac:thread_owner:*` are unchanged.

**Consumers affected**: Mailroom keeps its current `SELECT` of the unconsumed row. It will see a longer `context_text` when more than one context write landed before consume. RapidPro is the schema owner and already created the table Courier writes; this spec does not change RapidPro application code. Meta still receives 200 on the messages path and a non-200 when standby or `control_passed` persist fails.

## Data Model and Migrations

- **`wa_conversation_handover`**: one unconsumed row per `channel_id`+`contact_id`, enforced by `uq_wa_conv_handover_pending`. `context_text` holds the concatenated buffer. `context_type` is `summary` or `history`. `context_payload` is the JSON of the latest context object. `consumed_on` / `consumed_msg_id` stay Mailroom’s. Courier does not set them.

**Migrations**: none. `chk_wa_conv_handover_context_type`, `uq_wa_conv_handover_pending`, and column types stay as in `backends/rapidpro/schema.sql`. The previously deployed Courier binary uses the same table. A row written by this version (concatenated `context_text`, legal `context_type`) is a valid row for that binary. Rollback does not need a down migration.

The 32000-rune cap is application logic. The column is `text` and already accepts the `[truncated]` prefix plus 32000 characters, which `truncateWAHandoverContextText` can produce today.

## Non-Functional Requirements

### Peak Load

Same measured Courier inbound peak as spec 005, stated as a peak:

- **Service-wide inbound**: peak of **242.9 req/s** (1-minute rate) at 2026-10-28 14:06 UTC, from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}` over 2026-10-07 to 2026-11-06. Istio has no path label, so `/c/wac/receive` cannot be isolated. Per-route peak is therefore this service peak.
- **Added Postgres work**: one `INSERT` or one `INSERT ... ON CONFLICT DO UPDATE` per context-bearing webhook (standby, `control_passed`, or a `WAC` `messages` change that renders). Worst case, if every request at that peak carried context: **242.9 upserts/s** on `wa_conversation_handover`, beside the `WriteMsg` that already happens for messages. A `messages` change with several queued messages writes once per queued message (see Implementation Decisions), still on the order of that peak, not a new fan-out.
- **Redis**: no new key and no new command. The 005 `update_if_exists` on messages is unchanged.
- No new process memory and no new local-disk state. The existing spool path is unchanged.

### Observability

Structured `logrus` fields. Do not log `context_text`, contact name, or URN paths (Principle III).

| Event | Level | When | Fields |
|-------|-------|------|--------|
| `wac_pending_context_write_failed` | ERROR (existing logrus Sentry hook) | `WAC` `field=messages` and `WriteWAConversationHandover` returns an error | `event`, `channel_uuid`, `external_id` (`msg.ID`, the opaque WhatsApp message id). Error attached with `WithError`. No `context_text`, contact name, or URN field. |

**Correlation identifier**: `channel_uuid` plus `external_id`. A contact UUID is not available on this path until `WriteMsg` creates the contact; it is not added.

Success on the messages path adds no log line and does not call `logMessagingHandoverReceived` (that helper already attaches `contact_urn` and sender phone fields). Standby and `control_passed` keep today’s `"wa conversation handover persisted"` response data and the existing handover-received log. Those logs must not gain `context_text`.

`writeWAConversationHandover` already logs `contact_urn` on a database error. This spec does not extend that line and does not remove it (Principle XVI; same stance as 005 on `messagingHandoverLogFields`).

### Failure Handling

- **Timeout**: `WriteWAConversationHandover` already uses `context.WithTimeout(ctx, backendTimeout)` with `backendTimeout = 20s`. No new client and no new deadline.
- **Messages path, writer returns an error** (database and spool both failed, or the mock error in tests): one attempt, no Courier retry loop. Log `wac_pending_context_write_failed` and `WriteMsg`. HTTP 200 when `WriteMsg` succeeds. The context line is not recoverable from this request; the inbound message is. A non-200 would make Meta redeliver the message.
- **Messages path, database fails and the spool write succeeds**: `writeWAConversationHandover` returns nil, as today. The new ERROR event does not fire. The event replays through the spool into the same SQL.
- **Standby and `control_passed`**: persist error still returns from the handler, so Meta sees a non-200 and redelivers. No new retry loop inside Courier. Bound is Meta’s redelivery plus the existing spool.
- **`WriteMsg` after a successful context write**: unchanged. Failure still fails the webhook.
- **Idempotency**: the partial unique index remains the guarantee of one pending buffer per contact. Append is not idempotent under Meta redelivery of the same `control_passed`, standby context, or messages payload: a second delivery can append the same line again. That duplicate is accepted in this spec. Dedup of standby copies is a later spec. See the Principle XI row.
- **Empty or non-rendering context**: no write, no error. The messages loop continues.

### Security and Authorization

- **Trust boundary**: Meta `POST /c/wac/receive`. `receiveEvent` verifies `X-Hub-Signature` before `processCloudWhatsAppPayload`, except the existing `X-Router-Token` path, which this spec does not change. `receiveDemoEvent` (`WCD`) is outside the new write because of the `WAC` gate.
- **Authorization**: the courier router resolves the channel from the UUID in the path and rejects an unknown channel before the handler. No new credential and no new client-supplied owner or contact id. The contact is the one `contactForURN` already resolves from the message sender.
- **Validation**: `renderWAHandoverContextText` is the gate. Nil context, blank summary, and empty history do not write. `context_type` written by the SQL stays `summary` or `history`. Message timestamp parsing and sender URN checks stay where they are and still fail the webhook when invalid, before the new hook.
- **Secrets**: none added. No new `COURIER_*` variable.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| AC-01 | Rewrite `TestWriteWAConversationHandoverUpsertsPending`: summary `"First summary"` then history `"[user] hi"` yields one pending row, `context_type=history`, `context_text="First summary\n[user] hi"`. Sibling case in the same file: summary then summary stays `summary` and concatenates with one newline. Sibling case: stored text plus a new text whose combination exceeds 32000 runes stores `[truncated]` plus the last 32000 runes (same shape as `TestTruncateWAHandoverContextText`). Assert latest `context_payload`, `occurred_on` of the second event, preserved `previous_owner_*` when the second event leaves them empty, and overwrite when the second event sets them. |
| AC-02 | In `wa_conversation_handover_test.go`, set `consumed_on` on the first row, call `writeWAConversationHandoverToDB` again, and assert two rows: the consumed row keeps its original `context_text`, and the new pending row contains only the new text. |
| AC-03 | The rewritten test above is `TestWriteWAConversationHandoverUpsertsPending`. The consumed-row case is the insert-when-previous-was-consumed case in that file. |
| AC-04 | New fixture `handlers/facebookapp/testdata/wac/messagesContextSummaryWAC.json` (helloWAC shape, `field=messages`, `conversation_context.summary.text` set). `TestReceiveWACMessagesContext` in `facebookapp_test.go`: HTTP 200, one `WAHandoverEvents()` entry with that summary, `ContactURN` from `messages[].from`, `OccurredOn` from `messages[].timestamp`, and one queued msg. Mock call order records `WriteWAConversationHandover` before `WriteMsg`. Same payload on channel type `WCD` queues the msg and writes zero handover events. |
| AC-05 | Same test, body `helloWAC.json` (no `conversation_context`): HTTP 200, zero handover events, one queued msg. Second body with `conversation_context.summary.text` whitespace-only: zero handover events, one queued msg. |
| AC-06 | `SetErrorOnWAHandover(true)` with the summary fixture: HTTP 200, `LenQueuedMsgs()==1`, log entry `event=wac_pending_context_write_failed` and `channel_uuid` set, and no log field holding `context_text` or a URN path. Standby flag-on persist still returns the writer error (existing `persistWAHandoverContext` contract); do not switch that path to fail-open. |
| AC-07 | Tests do not set a new config field. `TestReceiveWACMessagesContext` runs with `EnableWACStandbyContext` left false and still persists messages context. Existing `TestReceiveWACStandbyContext` still requires the flag for standby and still sees `LenQueuedMsgs()==0`. |
| AC-08 | No migration file. Backend tests run against the existing schema. The upsert test still sees one pending row after two writes (`uq_wa_conv_handover_pending`). |
| AC-09 | Existing `TestReceiveWACStandbyContext` with `handoverStandbyContextWAC.json`: flag on persists the summary and `LenQueuedMsgs()==0`. This spec does not add a standby `messages` array reader. |

**Flow coverage**: signed `POST /c/wac/receive` with `field=messages` and a summary, through `processCloudWhatsAppPayload` to one handover event and one queued msg, in that order. Signed `helloWAC.json` still queues a msg with zero handover events. Two `writeWAConversationHandoverToDB` calls against Postgres prove the pending buffer text. Standby fixture still persists context without an inbound msg.

**Failure paths covered**: messages writer error still queues the msg and logs `wac_pending_context_write_failed`; whitespace `conversation_context` does not write; `WCD` does not write; a consumed row is not appended to.

**Stubbed at the boundary**: Meta is the fixture body plus `addValidSignatureWAC`. Handler tests use `courier.MockBackend`. Backend tests use the suite Postgres. No live Cloud API and no Mailroom process.

## Rollout and Rollback

Deploy Courier only. There is no flag and no Mailroom or RapidPro release in front of this one. The next context-bearing webhook appends; rows already pending are not rewritten until the next write.

Ship a `WENI-CHANGELOG.md` entry on the release tag that describes the pending-buffer append and `field=messages` context persist. `CHANGELOG.md` (upstream) stays untouched.

**Rollback**: redeploy the previous Courier tag. The schema is unchanged, so the old binary still inserts and replaces `context_text` on the same partial unique index. A pending row that already contains concatenated text remains a legal `summary` or `history` row; the old binary replaces that text on the next write. No data migration to reverse. Standby behaviour returns to whatever the previous tag did with `EnableWACStandbyContext`. Redis ownership from 005 is untouched by both deploy and rollback.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| II. Secrets and Supply Chain | No new secret and no new `COURIER_*` variable. | none |
| III. Structured, Privacy-Safe Observability | The new ERROR log uses `channel_uuid`, `event`, and opaque `external_id`. It does not attach `context_text`, contact name, or URN paths. Messages success does not call `logMessagingHandoverReceived`. | `writeWAConversationHandover` already logs `contact_urn` on database error. This change does not extend that line. Removing it is a separate Principle XVI change, as 005 left `messagingHandoverLogFields`. |
| IV. Versioned Contracts | Both changed surfaces are patch and backward compatible. No new route and no schema break. | none |
| V. Specification Traceability | Opens with `## Origin Without Product Spec` in the mandated format. Acceptance criteria are the origin of the change. | none |
| VI. No Silent Divergence | No Product Spec, so nothing is inherited to diverge from. | none — principle applies only to derived specs |
| VIII. Release Changelog | `WENI-CHANGELOG.md` entry on the shipping tag. Upstream `CHANGELOG.md` is not edited. | none |
| IX. Never Trust the Client | Context is taken only after the existing signature check, and only when `renderWAHandoverContextText` accepts it. Contact id comes from `contactForURN`, not from a client-supplied row id. | `X-Router-Token` keeps today’s signature skip (unchanged, Principle XVI). |
| X. Fail Gracefully and Predictably | Messages context failure is logged and the inbound msg is still written. Standby and handover persist errors still fail the webhook. No new panic path. | none |
| XI. Bounded Retry Over REST | No new REST hop. Messages context write is one attempt. Standby and `control_passed` still fail the webhook so Meta redelivers, bounded by Meta’s redelivery, with spool as the existing local fallback. | Two bounded departures, both required by this spec: (1) Messages-path failure is logged and not made redeliverable, because a non-200 would duplicate `WriteMsg`. The recoverable artifact on that path is the inbound message. (2) Append is not idempotent under redelivery of the same context payload; duplicate lines are accepted. A dedup key would be a schema or payload change this spec forbids (AC-08, AC-09). The partial unique index still guarantees one pending row per contact. |
| XII. Stateless, Peak-Sized Services | Pending text lives in Postgres. Peak declared above as 242.9 req/s over the stated window, with at most one upsert per context-bearing webhook at that peak. No new local-disk state. | Per-route peak is unavailable (Istio has no path label); the service-wide peak is the declared peak, as in spec 005. The existing spool exception is not extended. |
| XIII. Diagnosable Errors | Sentry fields on the new ERROR: `channel_uuid`, `event`, `external_id`. | No contact UUID before `WriteMsg`. Channel UUID is the account locator; `external_id` is the correlation id. URN and context text must not be attached. |
| XIV. Tests Exercise Flows | Webhook → handover event → queued msg, and Postgres upsert of the pending buffer, including the failure and consumed-row paths in the table above. | none |
| XV. Explicit Over Clever | The 32000 cap in the writer is a named constant in `wa_conversation_handover.go`, commented as matching `maxWAHandoverContextTextLen`. The newline and empty-stored-text rule are the SQL expression, not an implicit trim. | none |
| XVI. Contained Changes | Writer SQL, the messages hook, the shared helper, tests, and the changelog entry. No Redis, no flag, no standby message ingestion, no Mailroom edit, no FBA/IG/`WCD` behaviour. | none |

## Implementation Decisions

- **Append inside `ON CONFLICT DO UPDATE`**, not a prior `SELECT`. The partial unique index makes that statement the serialization point. `flushWAConversationHandoverFile` needs no special case.
- **Newline rule**: U+000A between stored and new text. Stored text `''` yields the new text only. New text `''` keeps the stored text, so a blank event cannot wipe the buffer or add a trailing blank line. Do not trim whitespace; only `''` is empty.
- **Truncation of the combination**: apply the tail rule to the concatenated string. Each rendered piece may already have been truncated by `truncateWAHandoverContextText`; the writer truncates again so a direct `WriteWAConversationHandover` of a long string, and the sum of two legal pieces, both stay at the cap. `'[truncated]'` is outside the 32000, matching the handler. The writer constant is 32000 and its comment points at `handlers/facebookapp.maxWAHandoverContextTextLen`. Do not move that handler constant in this change.
- **`context_payload`**: replace with the new event’s JSON. Do not merge objects. The agent reads `context_text`.
- **`previous_owner_app_id`, `previous_owner_app_role`, `previous_owner_business_id`, `handover_metadata`**: keep the stored value when the new event leaves the field empty (Go already sends NULL in that case; `COALESCE(EXCLUDED, existing)`). When the new event sets the field (`control_passed`), overwrite that column only.
- **`contact_urn`**: still replaced by the new event’s URN. The pending key is `channel_id`+`contact_id`; the URN column should match the latest write.
- **`occurred_on` / `created_on`**: set from the new event (`occurred_on = EXCLUDED.occurred_on`, `created_on = NOW()`). Latest write wins for freshness.
- **`org_id`, `channel_id`, `contact_id`**: not updated on conflict.
- **Messages hook placement**: render once per `WAC` `field=messages` change. Call the writer once per message that reaches `WriteMsg`, immediately before that call, after the `continue` branches for `unsupported` and `reaction`. A change with two queued messages appends the same rendered text twice. That matches “before this inbound” for each message Mailroom will see, and the duplicate line is the same accepted cost as redelivery.
- **Shared helper**: event build (render, contact name where `WaID` or `UserID` equals `urn.Path()`, payload marshal, `WriteWAConversationHandover`) lives in `wa_handover.go`. `persistWAHandoverContext` and the `control_passed` branch call it and still return the error. The messages path passes the message URN and `msg.timestamp` and does not return the error. `control_passed` still applies `previous_owner_*` before the write.
- **Messages success is quiet**: do not add `"wa conversation handover persisted"` to the messages HTTP data, and do not log the handover-received fields on this path.
- **`CheckExternalIDSeen`**: the messages hook does not consult it. A redelivery that still reaches `WriteMsg` may append again. Accepted with the idempotency note above.
- **No new flag**: messages context follows `control_passed`. Standby stays behind `EnableWACStandbyContext`.

## Open Questions

None.
