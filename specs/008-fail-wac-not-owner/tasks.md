# Tasks: Fail WAC Not-Owner Sends

**Input**: Design documents from `/specs/008-fail-wac-not-owner/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-send-not-owner.md](./contracts/wac-send-not-owner.md), [quickstart.md](./quickstart.md)

**Tests**: Included. Spec Test Strategy maps AC-01…AC-07. Write each story’s new assertions first and confirm they fail before that story’s implementation task.

**Stories**: Spec has acceptance criteria, not product user stories. Mapped to independently testable increments:

| Story | Priority | Spec ACs | Increment |
|-------|----------|----------|-----------|
| US1 | P1 (MVP) | AC-01, AC-02, AC-03, AC-06 (local block), AC-07 | Service `not_owner` → `F`+`H` no Graph; allowed templates and DirectSend still send; flag off / missing key / Redis read error unchanged |
| US2 | P2 | AC-04, AC-06 (131070 log) | Parse Graph `error.code`; WAC+flag 131070 → `F`+`H`+Redis `not_owner`; WCD and flag off unchanged |

No new `COURIER_*` flag. Production migration is RapidPro’s. Phase 1 has no tasks. Phase 2 lands `failed_reason` on `MsgStatus` and the status UPDATEs (AC-05) so US1 can compile `SetFailedReason`.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US2 on story-phase tasks only
- Every task includes an exact file path

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Existing Go module, `EnableWACSendOwnershipBlock`, Redis ownership, and `TestSendWACThreadOwner` already exist. No new package.

**Checkpoint**: No setup files to create. Phase 2 can start.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: `FailedReasonOtherOwner = "H"` (`varchar(1)`), `MsgStatus` getters/setters, test schema column, and the three status UPDATEs with `CASE WHEN :failed_reason != '' THEN :failed_reason ELSE failed_reason END` (AC-05). Empty reason leaves the stored value. `F` still does not increment `error_count`.

**⚠️ CRITICAL**: US1 cannot call `SetFailedReason` until this phase compiles.

### Tests for Phase 2

> Write T001 first. It fails to compile until T002, then fails the SQL assertions until T003.

- [ ] T001 Add a subtest at the end of `TestMsgStatus` in `backends/rapidpro/backend_test.go`. After resetting msg `10001` to `status='Q'` and `failed_reason` NULL: `NewMsgStatusForID` `MsgFailed`, `SetFailedReason("H")`, `WriteMsgStatus`. `QueryRow` `SELECT failed_reason, error_count FROM msgs_msg WHERE id=10001` equals `"H"` and the previous `error_count`. Then `NewMsgStatusForID` `MsgWired` with no `SetFailedReason` (empty); after `WriteMsgStatus`, `failed_reason` is still `"H"`. Do not add a production migration. Must fail until T003.

### Implementation for Phase 2

- [ ] T002 [P] In `status.go` add `FailedReasonOtherOwner = "H"` next to the `MsgStatusValue` constants. On interface `MsgStatus` add `FailedReason() string` and `SetFailedReason(string)`. Implement both on `mockMsgStatus` in `test.go`. Implement both on `DBMsgStatus` in `backends/rapidpro/status.go` with field `FailedReason_ string \`json:"failed_reason,omitempty" db:"failed_reason"\``. Empty string means the SQL CASE must leave the column unchanged.
- [ ] T003 In `backends/rapidpro/schema.sql` add nullable `failed_reason character varying(1)` on `msgs_msg`. Do not change `testdata.sql` INSERTs (column defaults NULL). In `backends/rapidpro/status.go` add the `failed_reason` CASE above to `updateMsgID`, `updateMsgExternalID`, and `bulkUpdateMsgStatusSQL`, and add `:failed_reason` to the bulk `VALUES` / `s(...)` list. Do not write `failed_reason` from any handler yet.

**Checkpoint**: `go test ./backends/rapidpro/ -count=1 -run TestMsgStatus` passes. `go test ./handlers/facebookapp/ -count=1 -run TestSendWACThreadOwner` still passes (no send behaviour change).

---

## Phase 3: User Story 1 - Block service sends, allow templates (Priority: P1) 🎯 MVP

**Goal**: With `EnableWACSendOwnershipBlock` true, WAC `not_owner` skips Graph for a **service** message, sets `MsgFailed` and `FailedReasonOtherOwner`, logs `wac_send_blocked` `reason=not_owner`. Exempt when `getTemplate` returns a category in `{MARKETING, UTILITY, AUTHENTICATION}` (`strings.ToUpper`) or `msg.DirectSend()`. Empty/unknown category is service. Missing key, `owner`, Redis read error, and flag false still send (AC-01, AC-02, AC-03, AC-06, AC-07).

**Independent Test**: `go test ./handlers/facebookapp/ -count=1 -run TestSendWACThreadOwner`. Plain text `not_owner` → 0 Graph hits, `F`, `FailedReason()=="H"`. MARKETING template → 1 hit, `W`. Flag off still wires.

### Tests for User Story 1

> Write these tests FIRST. New assertions fail until T005. Existing flag-off / missing-key / owner / Redis-closed cases must stay green.

- [ ] T004 [US1] Extend `TestSendWACThreadOwner` in `handlers/facebookapp/facebookapp_test.go`. Keep `runSend`. Add helpers that set `msg.WithMetadata` before `SendMsg`: templating JSON `{ "templating": { "template": { "name": "t", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3", "category": "<CAT>" }, "language": "eng" } }` and DirectSend `{ "direct_send": true, "direct_send_template_name": "util_t" }`. Cases, on `testChannelsWAC[0]` URN `whatsapp:250788123123`: (1) existing flag on + `not_owner` + plain text: also `status.FailedReason()=="H"` (fails until T005); (2) flag on + `not_owner` + category MARKETING, UTILITY, AUTHENTICATION (three subtests) → Graph hits 1, `MsgWired`, `FailedReason()==""`; (3) flag on + `not_owner` + DirectSend metadata → hits 1, `W`; (4) flag on + `not_owner` + templating `category` `""` → hits 0, `F`, `H` (service). Keep `assertNoWACOwnershipPII` on the block case. Do not add WCD cases. Do not assert 131070 here.

### Implementation for User Story 1

- [ ] T005 [US1] In `handlers/facebookapp/facebookapp.go` `sendCloudAPIWhatsappMsg`, call `getTemplate` **before** the ownership gate. Decode error still `return nil, errors.Wrapf(...)`. Pass the templating into `blockWACSendIfNotOwner`. In `handlers/facebookapp/wa_thread_owner.go`, change `blockWACSendIfNotOwner` to accept `*MsgTemplating` (or a bool `allowTemplate`). After a successful Redis read of `not_owner`, if `msg.DirectSend()` or (templating != nil and `strings.ToUpper(templating.Template.Category)` is `MARKETING`, `UTILITY`, or `AUTHENTICATION`), return `(false, nil)`. Else `SetStatus(MsgFailed)`, `SetFailedReason(courier.FailedReasonOtherOwner)`, existing INFO log `reason=not_owner`. Named constants for the three category strings. Reuse the existing `getTemplate` result for `isMarketingTemplate` later in the function (do not decode twice). WAC-only gate unchanged. Do not parse Graph errors in this task.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run TestSendWACThreadOwner` passes. Marketing templates send while `not_owner`. Session text does not. 131070 is still `E`.

---

## Phase 4: User Story 2 - Graph 131070 is terminal (Priority: P2)

**Goal**: `requestWAC` unmarshals `rr.Body` even when `MakeHTTPRequest` returns non-2xx. It does **not** write Redis. After every `requestWAC` return in `sendCloudAPIWhatsappMsg`, if channel `WAC` and the send flag is on and `resp.Error.Code == 131070`, helper applies Lua `set` `not_owner` `ts=time.Now().Unix()` `source=graph_131070`, `MsgFailed`+`H`, log `wac_send_blocked` `reason=131070`, and that send attempt stops. Flag off or `WCD`: body may parse but status stays today’s `E`. Other codes (e.g. 131047) stay `E`. Redis write error still `F`+`H` (AC-04, AC-06).

**Independent Test**: `go test ./handlers/facebookapp/ -count=1 -run TestSendWACThreadOwner`. Flag on + Graph 400 131070 → one hit, `F`+`H`, Redis `not_owner`. Flag off → `E`, Redis unchanged.

### Tests for User Story 2

> Write these tests FIRST. They fail until T007.

- [ ] T006 [US2] Extend `TestSendWACThreadOwner` in `handlers/facebookapp/facebookapp_test.go`. Add `runSendGraph` that takes HTTP status and body for the `httptest` handler (default remains 201 Wired JSON). Cases: (1) flag **false**, seed `owner` ts `1`, Graph 400 body `{"error":{"code":131070,"message":"x"}}` → hits 1, `MsgErrored`, `FailedReason()==""`, Redis `HGET state` still `owner`; (2) flag true, no Redis key, same 400 131070 → hits 1, `F`, `H`, Redis `not_owner`, log `wac_send_blocked` `reason=131070`, `assertNoWACOwnershipPII`; (3) flag true, seed `owner`, 400 `{"error":{"code":131047}}` → hits 1, `E`, Redis still `owner`; (4) flag true, 400 non-JSON body → `E`. Do not close Redis in these subtests (a closed pool fail-opens on `HGET` and never reaches the 131070 apply). (5) In `handlers/facebookapp/wa_thread_owner_test.go` add `TestFailWACSendOnGraph131070RedisWrite`: `handlers.NewTestServer` + `testChannelsWAC[0]`, `EnableWACSendOwnershipBlock=true`, `RedisPool().Close()` **before** the helper, `NewMsgStatusForID` starting at `MsgErrored`, `wacMTResponse` with `Error.Code=131070`. Call `failWACSendOnGraph131070`. Assert return `true`, `MsgFailed`, `FailedReason()=="H"`, log `wac_thread_owner_update_failed` `source=graph_131070`, log `wac_send_blocked` `reason=131070`, `assertNoWACOwnershipPII`. No Graph `httptest`. (6) Do not POST 131070 through a WCD handler; `SendTestCasesWAC` stay flag-default-false.

### Implementation for User Story 2

- [ ] T007 [US2] In `handlers/facebookapp/facebookapp.go` extend `wacMTResponse` with `Error *struct { Code int \`json:"code"\` } \`json:"error,omitempty"\``. Named constant `wacGraphErrorThreadNotOwned = 131070`. Change `requestWAC` so that after `AddLog`, if `rr != nil && len(rr.Body) > 0`, always `processResponse(rr.Body)` (even when `err != nil`). Do not set Wired if `Error.Code != 0`. Do not import Redis in `requestWAC`. Add `failWACSendOnGraph131070(msg, status, resp) bool` on `handler` in `handlers/facebookapp/wa_thread_owner.go`: return false unless `ChannelType()=="WAC"` and `EnableWACSendOwnershipBlock` and `resp != nil && resp.Error != nil && resp.Error.Code == wacGraphErrorThreadNotOwned`. Then `applyWACThreadOwner` mode `wacThreadOwnerModeSet`, state `wacThreadOwnerStateNone`, `ts=time.Now().Unix()`, `source=graph_131070`; on apply error log ERROR `event=wac_thread_owner_update_failed` `channel_uuid` `msg_uuid` `source=graph_131070` (no URN). Always `SetStatus(MsgFailed)`, `SetFailedReason(FailedReasonOtherOwner)`, INFO `wac_send_blocked` `reason=131070`. Return true. After **each** of the five `requestWAC` call sites in `sendCloudAPIWhatsappMsg` (audio, main loop, and the three later loops around lines 3297, 3417, 3522, 3615, 3637), if the helper returns true, `return status, nil` (do not apply contact URN updates, do not continue the part loop). If `requestWAC` returns a marshal/HTTP-prep `err` with empty body, keep returning that error. Do not change `SendMsg` WCD branch.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run 'TestSendWACThreadOwner|TestFailWACSendOnGraph131070RedisWrite|TestWACThreadOwner'` passes. Flag-off 131070 stays `E`.

---

## Phase 5: Polish & Cross-Cutting Concerns

- [ ] T008 [P] Add bullets to `WENI-CHANGELOG.md` under the newest existing Weni version heading (create `1.79.1` above `1.79.0` only if `1.79.0` is still newest): send-block flag now allows MARKETING/UTILITY/AUTHENTICATION and DirectSend; Graph 131070 on WAC is `F`+`failed_reason=H`; RapidPro must allow `H`. Do not edit upstream `CHANGELOG.md`.
- [ ] T009 Run [quickstart.md](./quickstart.md): `go test ./handlers/facebookapp/ -count=1 -run TestSendWACThreadOwner` and `go test ./backends/rapidpro/ -count=1 -run TestMsgStatus`.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: empty
- **Foundational (Phase 2)**: T001 → T002/T003 (T002 can overlap T001’s compile gap). Blocks US1
- **US1 (Phase 3)**: T004 then T005. Does not parse Graph errors
- **US2 (Phase 4)**: after US1 so the send function already has templating-before-gate. T006 then T007
- **Polish**: after US2

### User Story Dependencies

- **US1 (P1)**: after Phase 2. MVP
- **US2 (P2)**: after US1 (`sendCloudAPIWhatsappMsg` call sites)

### Parallel Opportunities

- T002 (interface) in parallel with drafting T001
- T008 changelog while T009 tests run after T007

### Parallel Example: Phase 2

```bash
# After T001 exists:
# T002 status.go + test.go + DBMsgStatus field
# T003 schema.sql + three UPDATE strings (same status.go as T002 — do not parallel T002 and T003)
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 2 (`H` on status SQL)
2. Phase 3 (local block + template allow)
3. **STOP**: `TestSendWACThreadOwner` without 131070 cases
4. Deployable with flag on: session messages stop, templates still go; Meta 131070 still retries until US2

### Incremental Delivery

1. Phase 2 → column write ready, send unchanged
2. US1 → local block correct
3. US2 → 131070 terminal
4. T008 changelog

### Parallel Team Strategy

One handler file for US1+US2 — sequential. Backend SQL (Phase 2) can land while send tests are written.

---

## Notes

- Constraints from [data-model.md](./data-model.md): `failed_reason` nullable `varchar(1)`; Courier writes only `H`; allowed categories exactly MARKETING, UTILITY, AUTHENTICATION plus DirectSend; 131070 Lua mode `set`, `source=graph_131070`
- `requestWAC` never takes a Redis connection
- Commit after each task or story
- Verify tests fail before T003 / T005 / T007
