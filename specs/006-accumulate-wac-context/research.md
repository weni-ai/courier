# Research: Accumulate WAC Context

All Technical Context items were already decided in [spec.md](./spec.md). This file records those decisions in the plan format and the repository patterns they reuse. No `NEEDS CLARIFICATION` remains.

## Decision 1: Append inside `ON CONFLICT DO UPDATE`

- **Decision**: Change the assignments in `upsertWAConversationHandoverSQL` (`backends/rapidpro/wa_conversation_handover.go`). Keep one `INSERT ... ON CONFLICT (channel_id, contact_id) WHERE consumed_on IS NULL DO UPDATE`. The combined `context_text` is computed in that `UPDATE`, then tail-truncated in the same expression. `writeWAConversationHandoverToDB` still builds the row and calls `NamedExecContext`. `flushWAConversationHandoverFile` keeps calling `writeWAConversationHandoverToDB`, so a spooled event appends too.
- **Rationale**: The partial unique index is already the serialization point. A `SELECT` plus a later `NamedExec` lets two webhooks both read the same text and drop a line. AC-08 allows either place; the single statement is the one that keeps both texts. Mailroom still sees one pending row.
- **Alternatives considered**: Read-modify-write in Go (lost updates); a child table of lines or a new row per write (breaks `uq_wa_conv_handover_pending` and forces a Mailroom change); merging `context_payload` JSON (no reader in this repository; the agent reads `context_text`).

## Decision 2: Concatenation, type, and truncation rules

- **Decision**:
  - Combined text: stored `''` → new text; new `''` → stored text; otherwise `stored || chr(10) || new`. Do not trim. Only the empty string skips the newline.
  - Then: `char_length(combined) <= 32000` stores `combined`; otherwise `'[truncated]' || right(combined, 32000)`. The prefix sits outside the cap, matching `truncateWAHandoverContextText` and `TestTruncateWAHandoverContextText`. Use `char_length` / `right` (PostgreSQL characters), not `octet_length`. Go strings written here are valid UTF-8, so characters match runes.
  - `context_type = 'history'` when the stored type or `EXCLUDED.context_type` is `history`; otherwise `'summary'`. That stays inside `chk_wa_conv_handover_context_type`.
  - The cap is a named Go constant in this file, `maxWAConversationHandoverContextRunes = 32000`, with a comment that it must match `handlers/facebookapp.maxWAHandoverContextTextLen`. Interpolate it into the SQL string (`fmt.Sprintf` into a `var`). Do not move the handler constant.
- **Rationale**: AC-01. The handler already truncates each rendered piece; the writer truncates the sum, and also a direct `WriteWAConversationHandover` that bypasses the handler. Principle XV forbids a bare `32000` in the query.
- **Alternatives considered**: Truncate only in the handler (a raw backend write and the sum of two 32000-rune pieces would exceed the cap); cap the prefix inside the 32000 (would disagree with `TestTruncateWAHandoverContextText`); share one constant via package `courier` (a drive-by move of the handler constant, Principle XVI).

## Decision 3: Other column assignments on conflict

- **Decision**:
  - `context_payload = EXCLUDED.context_payload` (latest JSON, including NULL). Do not `COALESCE`.
  - `previous_owner_app_id`, `previous_owner_app_role`, `previous_owner_business_id`, `handover_metadata`: `COALESCE(EXCLUDED.<col>, wa_conversation_handover.<col>)`. `writeWAConversationHandoverToDB` already leaves those `null.String` fields unset (NULL) when the event string is empty, and sets them when `control_passed` fills them.
  - `contact_urn = EXCLUDED.contact_urn`.
  - `occurred_on = EXCLUDED.occurred_on`, `created_on = NOW()`.
  - `org_id`, `channel_id`, `contact_id` stay the conflict key and are not updated.
- **Rationale**: Spec Implementation Decisions. A standby or messages write must not NULL out `previous_owner_*` stored by an earlier `control_passed`. The latest payload and timestamp describe the newest line.
- **Alternatives considered**: Replace every column with `EXCLUDED` (today’s bug for owner metadata); keep the old `occurred_on` (the spec says latest write wins for freshness).

## Decision 4: Shared helper, then the messages hook

- **Decision**: Add `wacHandoverContactName(contacts []wacHandoverContact, urn urns.URN) string` (match `WaID` or `UserID` to `urn.Path()`, return `Profile.Name`) and `(h *handler) persistRenderedWAHandover(ctx, channel, urn, contactName, contextType, contextText, conversationContext, occurredOn, controlPassed) error` in `wa_handover.go`. The method marshals `ContextPayload` when `conversationContext != nil`, calls `applyWAHandoverPreviousOwner` when `controlPassed != nil`, calls `Backend().WriteWAConversationHandover`, and returns that error. It does not write the HTTP response and does not log success.
  - `persistWAHandoverContext` keeps timestamp parse, render, and sender resolution (those still use `WriteAndLogRequestError`), then calls the helper with `controlPassed == nil` and returns the error.
  - `processMessagingHandover` keeps Redis, the no-context return, `logMessagingHandoverReceived`, and the error return. Contact name and the write go through the helper with `value.ControlPassed` set.
  - Messages path does not call `logMessagingHandoverReceived` and does not put `"wa conversation handover persisted"` on the HTTP data.
- **Rationale**: AC-04 forbids a second copy of render and contact-name logic. AC-06 forbids using `persistWAHandoverContext` as-is: it resolves `value.Sender` / `value.Timestamp` and fails the request. The messages URN and `msg.timestamp` are already parsed in the loop.
- **Alternatives considered**: Call `persistWAHandoverContext` from the messages loop (wrong URN source, fail-closed); duplicate the contact loop in `facebookapp.go` (AC-04).

## Decision 5: Where the messages hook sits

- **Decision**: In `processCloudWhatsAppPayload`, only when `channel.ChannelType() == "WAC"` and `change.Field == "messages"`, call `renderWAHandoverContextText(change.Value.ConversationContext)` once, before `for _, msg := range change.Value.Messages`. When that returns ok, keep `contextType`, `contextText`, and the context pointer. Immediately before `h.Backend().WriteMsg` (today around line 1466), if that render succeeded, call `persistRenderedWAHandover` with the message `urn`, `wacHandoverContactName` on a `[]wacHandoverContact` built once from `change.Value.Contacts`, and the `date` already parsed from `msg.Timestamp`. On error, log and fall through to `WriteMsg`. Do not write on the `continue` paths (`type=unsupported` and `type=reaction`). The unsupported-type `else` that already falls through to `WriteMsg` does get a write. Leave the 005 Redis `update_if_exists` where it is. Do not consult `CheckExternalIDSeen`.
- **Rationale**: AC-04 and AC-05. `receiveDemoEvent` and the `X-Router-Token` branch both call `processCloudWhatsAppPayload`, so the `WAC` gate has to be inside the loop or `WCD` would persist too. One render per change avoids repeating it per message. One write per queued message matches “before this inbound”. Two messages in one change can append the same line twice; that is the same accepted duplicate as redelivery.
- **Alternatives considered**: One write per change, before the loop (the second message can miss the buffer if Mailroom consumes the first); write at the top of the loop before `continue` (a reaction would leave a pending row with no inbound); gate only in `receiveEvent` (misses nothing for WAC, but the shared function is also used by WCD).

## Decision 6: Fail-open log and unchanged fail-closed paths

- **Decision**: Messages-path error log, next to the `WriteMsg` call:

  `logrus.WithError(err).WithFields(logrus.Fields{"event": "wac_pending_context_write_failed", "channel_uuid": channel.UUID(), "external_id": msg.ID}).Error("wac pending context write failed")`

  Then `WriteMsg` as today. Do not call `handlers.WriteAndLogRequestError` for this error. Standby and `control_passed` still `return nil, nil, err` / `return "", err`. When the database fails and `writeWAConversationHandover` spools successfully, it returns nil, so this log does not fire (existing spool contract).
- **Rationale**: AC-06, same shape as the 005 messages Redis failure log. `msg.ID` is the opaque WhatsApp id already on the payload. A contact UUID does not exist yet. Principle III: no context text, contact name, or URN field.
- **Alternatives considered**: Non-200 on messages (duplicates `WriteMsg`); logging `contact_urn` like `writeWAConversationHandover` already does on database error (this spec does not extend that line).

## Decision 7: No new flag; WAC only

- **Decision**: Do not add a `Config` field. Messages context persist runs whenever `renderWAHandoverContextText` succeeds, with `EnableWACStandbyContext` left at its current value. Standby persist stays behind that flag. `control_passed` persist stays ungated. `WCD`, `FBA`, `IG`, and on-premises `WA` do not gain the hook.
- **Rationale**: AC-07 and AC-09. `control_passed` already persists without a flag; a routed number is the only one that receives `conversation_context` on messages.
- **Alternatives considered**: Gate messages persist on `EnableWACStandbyContext` (would drop the Meta “inbound with AI summary” case whenever standby recording is off).

## Decision 8: Tests and pending-row isolation

- **Decision**: RapidPro cases live on `BackendTestSuite` and call `writeWAConversationHandoverToDB` directly (same as today). Each case starts by deleting `wa_conversation_handover` rows for the test channel so `TestWriteWAConversationHandover`’s pending `"Summary text"` row cannot be appended to by `TestWriteWAConversationHandoverUpsertsPending`. Scope `SELECT`s to that channel. Handler flow tests use `MockBackend`: add `SetErrorOnWAHandover(bool)` beside `SetErrorOnQueue`, and append a call-order token in `WriteWAConversationHandover` (`"wa_handover"`) and `WriteMsg` (`"msg"`), exposed for the one flow test. Extend `assertNoWACOwnershipPII` with `wac_pending_context_write_failed` (no `contact_urn`, no `sender_phone_number`, no `whatsapp:` in the entry).
- **Rationale**: Today the upsert test expects replacement, which hides the leftover pending row from the earlier test method. After append, that leftover becomes part of `context_text` and AC-03’s expected string is wrong. Testify runs `TestWriteWAConversationHandover` before `TestWriteWAConversationHandoverUpsertsPending`. The mock has no handover-error switch; `SetErrorOnQueue` is the pattern to copy. Postgres is only required for the rapidpro suite (`TestMsgSuite`, DSN in `testConfig()`).
- **Alternatives considered**: Rely on test order (breaks as soon as append lands); assert call order by sleeping or by log lines (the mock slices do not share a timeline today).
