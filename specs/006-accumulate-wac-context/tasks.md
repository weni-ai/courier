# Tasks: Accumulate WAC Context

**Input**: Design documents from `/specs/006-accumulate-wac-context/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-pending-context.md](./contracts/wac-pending-context.md), [quickstart.md](./quickstart.md)

**Tests**: Included. Spec Test Strategy maps AC-01…AC-09 to tests. Write each story's new assertions first and confirm the new failure cases fail before implementation.

**Stories**: Spec has acceptance criteria, not product user stories. Mapped to independently testable increments:

| Story | Priority | Spec ACs | Increment |
|-------|----------|----------|-----------|
| US1 | P1 (MVP) | AC-01, AC-02, AC-03, AC-08 | Pending `wa_conversation_handover` row appends `context_text` instead of replacing it |
| US2 | P2 | AC-04, AC-05, AC-06, AC-07, AC-09 | `WAC` `field=messages` persists renderable `conversation_context` before `WriteMsg`; standby copies stay ignored |

No new `COURIER_*` flag and no schema migration. Phase 1 and Phase 2 add no tasks.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US2 on story-phase tasks only
- Every task includes an exact file path

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: No new module, no `COURIER_*` flag, and no migration. `chk_wa_conv_handover_context_type` and `uq_wa_conv_handover_pending` in `backends/rapidpro/schema.sql` stay as they are.

**Checkpoint**: No setup files to create. US1 can start.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Nothing to add under both stories. `WriteWAConversationHandover`, `renderWAHandoverContextText`, `truncateWAHandoverContextText` (`maxWAHandoverContextTextLen = 32000`), and the partial unique index already exist from spec 005.

**Checkpoint**: Foundation is the current tree. User stories start at Phase 3.

---

## Phase 3: User Story 1 - Append pending handover context (Priority: P1) 🎯 MVP

**Goal**: A second `WriteWAConversationHandover` for the same pending `(channel_id, contact_id)` appends `context_text` and sets `context_type` to `history` when either side is `history`. A consumed row is not appended to. No column or index change (AC-01, AC-02, AC-03, AC-08).

**Independent Test**: `go test ./backends/rapidpro/ -count=1 -run 'TestMsgSuite/TestWriteWAConversationHandover'` against `courier_test`. Two writes yield one pending row whose `context_text` is `First summary\n[user] hi`.

### Tests for User Story 1

> Write these tests FIRST. Concatenation, summary+summary, truncation, preserved `previous_owner_app_id`, and a second write of `''` must fail on today's replace-on-conflict SQL. The consumed-row case, the "latest `occurred_on` / `context_payload`" assertions, and a first write of `''` then a non-empty text already match today's INSERT/replace and must stay green after T002.

- [X] T001 [US1] Rewrite `backends/rapidpro/wa_conversation_handover_test.go` on `BackendTestSuite`. At the start of each case, `DELETE FROM wa_conversation_handover WHERE channel_id = (SELECT id FROM channels_channel WHERE uuid = 'dbc126ed-66bc-4e28-b67b-81dc3327c95d')` so `TestWriteWAConversationHandover`'s pending `"Summary text"` row cannot leak into the next method. Scope every `SELECT` to that channel. Cases, all via `writeWAConversationHandoverToDB`: (1) `TestWriteWAConversationHandoverUpsertsPending` writes summary `"First summary"` then history `"[user] hi"` and expects one row with `consumed_on IS NULL`, `context_type = 'history'`, `context_text = "First summary\n[user] hi"` (this fails until T002); (2) summary then summary stays `context_type = 'summary'` and joins with one `\n`; (3) stored text plus new text whose `char_length` exceeds 32000 stores the prefix `[truncated]` plus the last 32000 characters (same shape as `TestTruncateWAHandoverContextText`: prefix outside the cap); (4) set `consumed_on` on the first row, write again, expect the consumed row's `context_text` unchanged and a new pending row holding only the new text; (5) first event sets `PreviousOwnerAppID`, second leaves it empty: stored app id kept, `context_payload` and `occurred_on` equal the second event; a third event with a different `PreviousOwnerAppID` overwrites that column only; (6) pending `context_text` `''` then `"hi"` → one pending row whose `context_text` is exactly `hi` (no leading newline); (7) pending `context_text` `"hi"` then `''` → one pending row whose `context_text` is still exactly `hi` (not `''` and not `"hi\n"`). Must fail on (1), (2), (3), the preserve half of (5), and (7) until T002. (6) passes on today's replace and must stay exact `hi` after T002.

### Implementation for User Story 1

- [X] T002 [US1] Change `upsertWAConversationHandoverSQL` in `backends/rapidpro/wa_conversation_handover.go` only. Keep one `INSERT ... ON CONFLICT (channel_id, contact_id) WHERE consumed_on IS NULL DO UPDATE`. Implement these assignments: combined text is `new` when stored `context_text = ''`, stored text when new `context_text = ''`, otherwise `stored || chr(10) || new` (do not trim); then if `char_length(combined) <= 32000` store `combined`, else `'[truncated]' || right(combined, 32000)` (`char_length`/`right`, not `octet_length`). `context_type` is `'history'` when stored or `EXCLUDED.context_type` is `'history'`, else `'summary'` (result stays inside `CHECK (context_type IN ('history', 'summary'))`). `context_payload = EXCLUDED.context_payload` (replace, including NULL; do not merge). `previous_owner_app_id`, `previous_owner_app_role`, `previous_owner_business_id`, `handover_metadata` use `COALESCE(EXCLUDED.<col>, wa_conversation_handover.<col>)`. `contact_urn = EXCLUDED.contact_urn`. `occurred_on = EXCLUDED.occurred_on`. `created_on = NOW()`. Do not assign `org_id`, `channel_id`, `contact_id`, `consumed_on`, or `consumed_msg_id`. Name `maxWAConversationHandoverContextRunes = 32000` in this file with a comment that it must match `handlers/facebookapp.maxWAHandoverContextTextLen`, and interpolate it with `fmt.Sprintf` into the SQL `var` so the query has no bare `32000`. Leave `writeWAConversationHandover` spool behaviour and its existing `contact_urn` log unchanged. Do not edit `backends/rapidpro/schema.sql`.

**Checkpoint**: `go test ./backends/rapidpro/ -count=1 -run 'TestMsgSuite/TestWriteWAConversationHandover'` passes. Pending text accumulates. This is the MVP deploy: standby and `control_passed` writes append, messages behaviour is still today's.

---

## Phase 4: User Story 2 - Persist messages conversation_context before WriteMsg (Priority: P2)

**Goal**: On `WAC` `field=messages`, renderable `change.Value.ConversationContext` is written with `WriteWAConversationHandover` before `WriteMsg`. Absent or blank context does not write. Writer error logs `wac_pending_context_write_failed` and still queues the msg. No new flag. Standby `Messages`/`Statuses` stay ignored (AC-04, AC-05, AC-06, AC-07, AC-09).

**Independent Test**: `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACMessagesContext|TestReceiveWACStandbyContext'`. Summary fixture: one handover event then one queued msg, with `EnableWACStandbyContext` left false. `helloWAC.json`: zero handover events, one queued msg. Standby flag-on still has `LenQueuedMsgs()==0`.

### Tests for User Story 2

> Write these tests FIRST. The one-message summary case, the two-message call order, and the messages-path writer-error log must fail until T007. `helloWAC.json`, whitespace context, the `WCD` zero-handover case, and the standby / `control_passed` writer-error cases (HTTP 400) already match today's fail-closed return once T004 exists; they must stay 400 after T006.

- [X] T003 [P] [US2] Add `handlers/facebookapp/testdata/wac/messagesContextSummaryWAC.json` by copying `handlers/facebookapp/testdata/wac/helloWAC.json` (`field=messages`, `from=5678`, `id=external_id`, `timestamp=1454119029`, text body `Hello World`, contact `wa_id=5678` name `Kerry Fisher`, `phone_number_id=12345`) and adding `value.conversation_context.summary.text` = `AI summary of the conversation.` Do not put that object on `messages[].context`. Add `handlers/facebookapp/testdata/wac/messagesContextSummaryTwoWAC.json` as the same change with two text messages in that array: first `id=external_id`, body `Hello World`; second `id=external_id_2`, body `Second`, same `from` and `timestamp`. One `conversation_context` on `value`, shared by both messages.
- [X] T004 [P] [US2] In `test.go` `MockBackend`, add `SetErrorOnWAHandover(bool)` beside `SetErrorOnQueue`. `WriteWAConversationHandover` returns an error when the flag is set and still appends a call-order token `"wa_handover"`. `WriteMsg` appends `"msg"`. Expose `BackendCalls() []string` copied under the existing mutex. Do not change production `Backend` methods.
- [X] T005 [US2] Add `TestReceiveWACMessagesContext` in `handlers/facebookapp/facebookapp_test.go` using `handlers.NewTestServer`, `testChannelsWAC[0]`, `handlers.TestHandlerRequest` on `/c/wac/receive` with `addValidSignatureWAC`, and `handlers.CaptureLogs`. Leave `EnableWACStandbyContext` false. Cases: (1) `messagesContextSummaryWAC.json` → HTTP 200, `WAHandoverEvents()` length 1 with `ContextType` summary, `ContextText` `AI summary of the conversation.`, `ContactURN` identity `whatsapp:5678`, `OccurredOn` equal to `time.Unix(1454119029, 0).UTC()`, `LenQueuedMsgs()==1`, and `BackendCalls()` contains `"wa_handover"` before `"msg"`; (2) `helloWAC.json` → 200, zero handover events, one queued msg; (3) same summary fixture with `summary.text` replaced by whitespace → 200, zero handover events, one queued msg; (4) `SetErrorOnWAHandover(true)` on the summary fixture → 200, `LenQueuedMsgs()==1`, a log entry with `event=wac_pending_context_write_failed` and `channel_uuid` set, and that entry has no `context_text` field and no URN path. Extend `assertNoWACOwnershipPII` in `handlers/facebookapp/wa_thread_owner_test.go` so `wac_pending_context_write_failed` is in the same switch (no `contact_urn`, no `sender_phone_number`, no `whatsapp:` in the entry). (5) `newWACDemoHandler("WCD", "WhatsApp Cloud Demo")` and a WCD mock channel addressed `12345`, POST the summary fixture to `/c/wcd/{channel-uuid}/receive` (WCD routes include the UUID) → one queued msg, zero handover events (`receiveDemoEvent` does not check the signature); (6) `messagesContextSummaryTwoWAC.json` on `WAC` → HTTP 200, `WAHandoverEvents()` length 2, both `ContextText` `AI summary of the conversation.` and `ContextType` summary, `LenQueuedMsgs()==2`, and `BackendCalls()` equals `["wa_handover", "msg", "wa_handover", "msg"]` (a handover write immediately before each `WriteMsg`, not both writes before either msg); (7) in `TestReceiveWACStandbyContext`, `EnableWACStandbyContext=true` and `SetErrorOnWAHandover(true)`, POST `handlers/facebookapp/testdata/wac/handoverStandbyContextWAC.json` with `addValidSignatureWAC` → HTTP 400 (`courier.WriteError`), `LenQueuedMsgs()==0`, and no log entry with `event=wac_pending_context_write_failed`; (8) in `TestReceiveWACHandover`, `SetErrorOnWAHandover(true)`, POST `handlers/facebookapp/testdata/wac/handoverSummaryWAC.json` with `addValidSignatureWAC` → HTTP 400, `LenQueuedMsgs()==0`. Each subtest builds its own `MockBackend`. Cases (1), (4), and (6) must fail until T007. Cases (7) and (8) pass once T004 exists and must still be HTTP 400 after T006.

### Implementation for User Story 2

- [X] T006 [US2] In `handlers/facebookapp/wa_handover.go`, add `wacHandoverContactName(contacts []wacHandoverContact, urn urns.URN) string` (first contact whose `WaID` or `UserID` equals `urn.Path()`, return `Profile.Name`) and `(h *handler) persistRenderedWAHandover(ctx, channel, urn, contactName, contextType, contextText, conversationContext *wacConversationContext, occurredOn time.Time, controlPassed *wacHandoverControlPassed) error`. The method marshals `ContextPayload` when `conversationContext != nil`, calls `applyWAHandoverPreviousOwner` when `controlPassed != nil`, calls `Backend().WriteWAConversationHandover`, and returns that error. It does not write an HTTP response and does not log success. Rewire `persistWAHandoverContext` and the `control_passed` branch of `processMessagingHandover` to use it. Those two callers still return the write error (standby and handover webhooks stay fail-closed). `processMessagingHandover` still calls `logMessagingHandoverReceived` itself. Do not change Redis ownership calls.
- [X] T007 [US2] In `handlers/facebookapp/facebookapp.go` `processCloudWhatsAppPayload`, when `channel.ChannelType() == "WAC"` and `change.Field == "messages"`, call `renderWAHandoverContextText(change.Value.ConversationContext)` once before `for _, msg := range change.Value.Messages`. When it returns ok, immediately before `h.Backend().WriteMsg` (the call that is today the end of the message loop) call `persistRenderedWAHandover` with that message's resolved `urn` (phone URN when both `from` and `from_user_id` are set), `wacHandoverContactName` on one `[]wacHandoverContact` built from `change.Value.Contacts`, the rendered type and text, the context pointer, and the `date` already parsed from `msg.Timestamp`. Pass `controlPassed` nil. On error, `logrus.WithError(err).WithFields(logrus.Fields{"event": "wac_pending_context_write_failed", "channel_uuid": channel.UUID(), "external_id": msg.ID}).Error("wac pending context write failed")` and continue into `WriteMsg`. Do not call `handlers.WriteAndLogRequestError` for that error. Do not call `logMessagingHandoverReceived`. Do not append `"wa conversation handover persisted"` to the messages response data. Do not write on the `continue` paths for `type=unsupported` and `type=reaction`. Leave the 005 Redis `update_if_exists` where it is. `WCD` must not enter this block. Do not add a `Config` field.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACMessagesContext|TestReceiveWACStandbyContext|TestReceiveWACHandover'` passes. Two messages in one change record handover, msg, handover, msg. Standby and `control_passed` writer errors stay HTTP 400 with zero queued msgs. Standby success still queues no msg.

---

## Phase 5: Polish & Cross-Cutting Concerns

**Purpose**: Release note and the quickstart suite

- [X] T008 [P] Add `fix` bullets to `WENI-CHANGELOG.md` for pending-buffer append and `field=messages` `conversation_context` persist. Read every version heading in that file. If any heading is newer than `1.79.0`, add the bullets under that existing heading and do not create another version section. Create section `1.79.1` above `1.79.0` only when `1.79.0` is the newest heading. Do not edit upstream `CHANGELOG.md`.
- [X] T009 [P] Run [quickstart.md](./quickstart.md) from repo root: `go test ./backends/rapidpro/ -count=1 -run 'TestMsgSuite/TestWriteWAConversationHandover'` and `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACMessagesContext|TestReceiveWACStandbyContext|TestReceiveWACHandover'`. Fix rename drift so those tests exist and pass. Confirm `backends/rapidpro/schema.sql` still has `CONSTRAINT chk_wa_conv_handover_context_type CHECK (context_type IN ('history', 'summary'))` and `CREATE UNIQUE INDEX uq_wa_conv_handover_pending`.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No tasks
- **Foundational (Phase 2)**: No tasks
- **US1 (Phase 3)**: Starts immediately — MVP
- **US2 (Phase 4)**: Handler tests use `MockBackend` and can be written without US1. Production append of a messages write onto a standby/`control_passed` row needs US1's SQL. Land US1 first.
- **Polish (Phase 5)**: After US1 and US2

### User Story Dependencies

- **US1 (P1)**: No other story. `backends/rapidpro/wa_conversation_handover.go` only.
- **US2 (P2)**: After US1 for the real buffer. T003 and T004 have no dependency on each other. T005 after T003 and T004. T006 after T005 (tests exist). T007 after T006 (messages path must call `persistRenderedWAHandover`, not a second copy of render/contact-name logic).

### Within Each User Story

- Tests fail before the implementation task
- US1: test file, then SQL
- US2: fixture and mock seam, then flow test, then helper, then the messages hook

### Parallel Opportunities

- T003 (`testdata/wac/messagesContextSummaryWAC.json`) and T004 (`test.go`) in parallel
- T008 (`WENI-CHANGELOG.md`) and T009 (test run) in parallel after T007
- Do not parallel T001 with T002 (same behaviour, test first)
- Do not parallel T006 with T007 (`facebookapp.go` must call the helper from `wa_handover.go`)
- Do not parallel US2's `facebookapp.go` edit with any other edit to `processCloudWhatsAppPayload`

### Parallel Example: User Story 2 tests

```text
T003: handlers/facebookapp/testdata/wac/messagesContextSummaryWAC.json and messagesContextSummaryTwoWAC.json
T004: test.go SetErrorOnWAHandover + BackendCalls
```

Then T005 → T006 → T007, in that order.

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. T001 failing rapidpro tests
2. T002 SQL append
3. **STOP**: `TestMsgSuite/TestWriteWAConversationHandover` green. Deploy: stacked standby and `control_passed` context survives. Messages still drop `conversation_context`.

### Incremental Delivery

1. US1 → pending buffer appends (MVP)
2. US2 → routed `WAC` messages with an AI summary persist that summary before the inbound msg, fail-open on writer error
3. Polish: changelog + quickstart commands

### Parallel Team Strategy

1. One person: T001–T002 (Postgres writer)
2. Another can draft T003–T004 while US1 SQL is in review (different files)
3. T005–T007 stay with one person (`wa_handover.go` then `facebookapp.go`)
4. T008 and T009 together at the end

---

## Notes

- [P] = different files, no incomplete dependency
- Tests first. Concatenation, the empty-string keep (T001 case 7), the one-message summary, and the two-message call order must fail before their implementation tasks. Standby and `control_passed` writer errors must stay HTTP 400
- Constraints implementers must not invent, from `data-model.md`: `context_text` is `text NOT NULL`; `context_type` is only `'history'` or `'summary'`; cap is 32000 characters with `[truncated]` outside the cap; pending identity is one row with `consumed_on IS NULL` per `(channel_id, contact_id)` (`uq_wa_conv_handover_pending`); `consumed_on` is Mailroom's column
- Do not log `context_text`, contact name, or URN paths on `wac_pending_context_write_failed`
- Mailroom is not in this repository. Do not change how it reads one pending row
- Commit after each task or story checkpoint (Conventional Commits, Principle VII)
- Changelog (T008) comes after the behaviour is in
