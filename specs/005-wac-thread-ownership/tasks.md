# Tasks: WAC Thread Ownership

**Input**: Design documents from `/specs/005-wac-thread-ownership/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-thread-ownership.md](./contracts/wac-thread-ownership.md)

**Tests**: Included. Spec Test Strategy maps AC-01…AC-08 to tests; write each story's tests first and confirm they fail before implementation.

**Stories**: Spec has acceptance criteria, not product user stories. Mapped to independently testable increments:

| Story | Priority | Spec ACs | Increment |
|-------|----------|----------|-----------|
| US1 | P1 (MVP) | AC-04, AC-05 | Record ownership from `messaging_handovers` (`control_passed` / `control_taken`) |
| US2 | P2 | AC-06 | `field=messages` updates Redis only if the key already exists |
| US3 | P3 | AC-07 (standby) | Persist standby conversation context when `EnableWACStandbyContext` is true |
| US4 | P3 | AC-08, AC-07 (send) | Skip Graph send when flag on and Redis `state` is `not_owner`; fail-open otherwise |

AC-01 (flags default off), AC-02 (Redis hash + TTL), AC-03 (Lua CAS) are Phase 2 foundation.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US4 on story-phase tasks only
- Every task includes an exact file path

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add the two global flags so later wiring cannot enable send-block or standby persist by accident.

- [ ] T001 Create `config_test.go` with `TestNewConfigWACThreadOwnerFlags` asserting `courier.NewConfig()` has `EnableWACStandbyContext == false` and `EnableWACSendOwnershipBlock == false` (AC-01; test must fail until T002)
- [ ] T002 Add `EnableWACStandbyContext bool` (help: persist renderable conversation context from WAC `field=standby` into `wa_conversation_handover`; default false → `COURIER_ENABLE_WAC_STANDBY_CONTEXT`) and `EnableWACSendOwnershipBlock bool` (help: skip WAC Graph send when Redis thread ownership is `not_owner`; default false → `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK`) to `Config` in `config.go`, both omitted from `NewConfig()` so they stay false (AC-01)

**Checkpoint**: `go test . -count=1 -run TestNewConfigWACThreadOwnerFlags` passes

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Redis ThreadOwner entity and atomic apply/get used by every story. Channel type `WAC` only at call sites, not inside Lua.

**⚠️ CRITICAL**: No user story work until this phase is complete

- [ ] T003 Create failing `TestWACThreadOwner` table tests in `handlers/facebookapp/wa_thread_owner_test.go` using `courier.NewMockBackend().RedisPool()` (flush or `DEL` the key per subtest). Cover: `mode=set` missing key → `created` with `state` `owner` or `not_owner`; `ts` newer → `updated` and TTL reset; incoming `<=` stored → `stale` and state/TTL unchanged (older **and** equal timestamps); `mode=update_if_exists` missing key → `absent` and **no key created**; `HGETALL` key `wac:thread_owner:{channel_uuid}:{urn.Identity()}` hash fields `state` (`owner` \| `not_owner`) and `ts` (Unix seconds decimal); TTL in `(0, 86400]` after apply (`wacThreadOwnerTTL` = 86400 seconds from last **applied** write). Include `get` returning `not_owner` / `owner` / missing. Tests must fail until T004.
- [ ] T004 Implement `handlers/facebookapp/wa_thread_owner.go`: named constants `wacThreadOwnerTTL = 24 * time.Hour`, key prefix, states `owner`/`not_owner`, modes `set`/`update_if_exists`, outcomes `created`/`updated`/`stale`/`absent`; key helper `wac:thread_owner:{channel_uuid}:{urn.Identity()}`; `redis.NewScript(1, ...)` Lua that `HGET` `ts`, if stored and `tonumber(incoming) <= tonumber(stored)` return `stale`, if missing and mode `update_if_exists` return `absent` (no write), else `HSET` `state`+`ts` and `EXPIRE` 86400 and return `created` or `updated`; Go `apply(conn, channelUUID, urn, ts, state, mode)` and `get(conn, channelUUID, urn)` (`HGET` `state`). Why-comment on `<=`: Meta redelivery must not refresh TTL. Do **not** add a `Backend` method (use `RedisPool()` at call sites).
- [ ] T005 Add structured apply logging in `handlers/facebookapp/wa_thread_owner.go`: `logrus` INFO `event=wac_thread_owner_applied` with fields `channel_uuid`, `outcome`, `state`, `source` only — never URN path, phone, or message text. `apply` accepts `source` (`control_passed`/`control_taken`/`messages`).

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run TestWACThreadOwner` passes. Foundation ready.

---

## Phase 3: User Story 1 - Record ownership from messaging_handovers (Priority: P1) 🎯 MVP

**Goal**: `control_passed` always writes Redis `owner` (summary still persists to `wa_conversation_handover`; no empty row without context). `control_taken` writes `not_owner`, HTTP 200, no inbound msg, no handover row (AC-04, AC-05). Missing `value.timestamp` uses `entry.time` including `0` (AC-03).

**Independent Test**: Signed POST `/c/wac/receive` with existing `handoverNoContextWAC.json` → HTTP 200, `WAHandoverEvents()` empty, Redis `owner` on `wac:thread_owner:8eb23e93-5ecb-45ba-b726-3b064e0c568c:whatsapp:5678`. Summary fixture still persists one event **and** Redis `owner`. New `control_taken` fixture → 200, `LenQueuedMsgs()==0`, no handover event, Redis `not_owner`.

### Tests for User Story 1

> Write these tests FIRST, ensure they FAIL before implementation

- [ ] T006 [P] [US1] Add `handlers/facebookapp/testdata/wac/handoverTakenWAC.json` (`field=messaging_handovers`, `type=control_taken`, `sender.wa_id=5678`, `recipient.phone_number_id=12345`, `timestamp=1454119029`) and `handlers/facebookapp/testdata/wac/handoverTakenNoTimestampWAC.json` (omit `value.timestamp`, keep `entry.time=1454119029`)
- [ ] T007 [US1] Extend `TestReceiveWACHandover` in `handlers/facebookapp/facebookapp_test.go`: after each 200, `HGETALL` Redis for the contact URN (skip early return on `eventCount==0` so no-context still asserts Redis). Cases: no-context → `owner`, `WAHandoverEvents` empty, HTTP 200; summary/history/nested → persist as today **and** `owner`; `handoverTakenWAC.json` → `not_owner`, 0 queued msgs, 0 handover events; no-timestamp taken → `not_owner` with `ts=1454119029`. Equal-timestamp redelivery of no-context does not change `state`/`ts`. Missing sender on `control_passed`/`control_taken` → 400.

### Implementation for User Story 1

- [ ] T008 [US1] Add `wacHandoverTypeControlTaken = "control_taken"` in `handlers/facebookapp/wa_handover.go`. Change `processMessagingHandover` so `control_passed` and `control_taken` resolve sender via `resolveWAHandoverSenderURN` **before** context rendering (today no-context returns first). `mode=set`: `control_passed` → `owner`, `control_taken` → `not_owner`. Lua write error returns the error (Meta non-200). Then: `control_passed` with renderable context → existing `WriteWAConversationHandover`; without → HTTP 200, no row; `control_taken` → 200, no `WriteMsg`, no `WriteChannelEvent`, no `WriteWAConversationHandover`. Other types stay `ignored_type`. Gate Redis writes on `channel.ChannelType() == "WAC"`.
- [ ] T009 [US1] Optional `ControlTaken` JSON field on `moPayload` value in `handlers/facebookapp/facebookapp.go` (discriminator remains `value.type`; inner object not required to set ownership). Pass `Type` through existing `processMessagingHandover` call in `processCloudWhatsAppPayload`.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run TestReceiveWACHandover` passes. US1 is the MVP deploy (flags still off; sends unchanged).

---

## Phase 4: User Story 2 - Messages update existing marks only (Priority: P2)

**Goal**: On `field=messages`, write `owner` only when the Redis key already exists. Contacts with no key stay unmarked (AC-06). Redis write failure does not fail the webhook.

**Independent Test**: POST `helloWAC.json` with empty Redis → "Handled", key absent. Seed `not_owner` for `whatsapp:5678` then POST `helloWAC.json` → `owner`. Invalid message timestamp still 400 with no Redis write.

### Tests for User Story 2

- [ ] T010 [US2] Add `TestReceiveWACThreadOwnerMessages` in `handlers/facebookapp/facebookapp_test.go`: (1) `helloWAC.json` on fresh `MockBackend` → HTTP 200, `HGETALL` of `wac:thread_owner:8eb23e93-5ecb-45ba-b726-3b064e0c568c:whatsapp:5678` empty; (2) `PrepBackend` seed hash `state=not_owner ts=1` then `helloWAC.json` → `state=owner` and `ts=1454119029`; (3) capture `logrus` and assert `wac_thread_owner_update_failed` is not required on the success path. Must fail until T011.

### Implementation for User Story 2

- [ ] T011 [US2] In `handlers/facebookapp/facebookapp.go` `processCloudWhatsAppPayload`, after the primary URN is resolved for each `change.Value.Messages` item and only when `channel.ChannelType() == "WAC"` and `change.Field == "messages"`, call `apply` `mode=update_if_exists` `state=owner` with `msg.Timestamp` Unix seconds. On Redis/`apply` error: `logrus` ERROR `event=wac_thread_owner_update_failed` with `channel_uuid` and `source=messages` (no URN), then **continue** existing `WriteMsg` and return HTTP 200. Do not create a key when outcome is `absent`.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACThreadOwnerMessages|TestReceiveWACHandover'` passes. Non-routing numbers still create no mark.

---

## Phase 5: User Story 3 - Standby context persist flag (Priority: P3)

**Goal**: `EnableWACStandbyContext` gates persistence of renderable conversation context from `field=standby` into `wa_conversation_handover`. Default false keeps today's ignore. Does **not** gate `control_passed` summary writes. Does **not** write Redis ownership from standby (AC-07).

**Independent Test**: Standby fixture with summary, flag false → 200, `WAHandoverEvents()` empty. Same fixture, `s.Config().EnableWACStandbyContext = true` → one handover event. No inbound msgs from standby copies.

### Tests for User Story 3

- [ ] T012 [P] [US3] Add `handlers/facebookapp/testdata/wac/handoverStandbyContextWAC.json`: `field=standby`, `phone_number_id=12345`, sender `wa_id=5678`, renderable `conversation_context.summary.text`, no `messages` copies required
- [ ] T013 [US3] Add `TestReceiveWACStandbyContext` in `handlers/facebookapp/facebookapp_test.go`: flag false (default `NewTestServer`) → HTTP 200, zero handover events, no Redis key created; flag true on `s.Config()` → one `WriteWAConversationHandover` with summary text, still no Redis ownership write, `LenQueuedMsgs()==0`. Must fail until T014.

### Implementation for User Story 3

- [ ] T014 [US3] In `handlers/facebookapp/facebookapp.go` `processCloudWhatsAppPayload` `field=standby` branch: if `!EnableWACStandbyContext`, keep `ignoring standby webhook`. If true, build `wacHandoverValue` from the change (reuse `ConversationContext`, `Sender`, `Contacts`, `Timestamp`), `renderWAHandoverContextText`; on success call `WriteWAConversationHandover` (same event shape as `control_passed`); on no renderable context HTTP 200 and no row. Never `WriteMsg` from standby. Never `apply` Redis.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run TestReceiveWACStandbyContext` passes. Summary `control_passed` tests from US1 still persist.

---

## Phase 6: User Story 4 - Send block by ownership flag (Priority: P3)

**Goal**: When `EnableWACSendOwnershipBlock` is true and Redis `state` is `not_owner`, skip Graph and mark `MsgFailed`. Missing key, `owner`, empty state, or **any read error** send as today. Flag false: no Redis read. `WCD` untouched (AC-08).

**Independent Test**: Dedicated send tests with `httptest` Graph: flag false + seeded `not_owner` still POSTs Graph status `W`; flag true + `not_owner` → no Graph request, status `F`; flag true + no key → Graph called; flag true + Redis closed → Graph called, ERROR log `wac_send_ownership_read_failed`.

### Tests for User Story 4

- [ ] T015 [US4] Add `TestSendWACThreadOwner` in `handlers/facebookapp/facebookapp_test.go` using `handlers.NewTestServer`, `testChannelsWAC[0]`, `setSendURL`/`httptest` Graph, and `PrepBackend` Redis. Cases: default flag + `not_owner` for `whatsapp:250788123123` → Graph called, status `W` (existing SendTestCasesWAC "Plain Send" body); `EnableWACSendOwnershipBlock=true` + `not_owner` → Graph request count 0, `MsgFailed` (`F`); flag true + no key → Graph called; flag true + `owner` → Graph called; flag true + `RedisPool` closed/unusable → Graph called. Assert log `wac_send_blocked` `reason=not_owner` without URN. Must fail until T016. Do not modify shared `SendTestCasesWAC` (flag stays off there).

### Implementation for User Story 4

- [ ] T016 [US4] At the start of `sendCloudAPIWhatsappMsg` in `handlers/facebookapp/facebookapp.go`, if `msg.Channel().ChannelType() == "WAC"` and `EnableWACSendOwnershipBlock`, `get` Redis state. `not_owner`: do not call Graph; `status.SetStatus(courier.MsgFailed)`; INFO `event=wac_send_blocked` fields `channel_uuid`, `msg_uuid`, `reason=not_owner`; return that status. Missing/`owner`/empty: fall through. Read/`get` error: ERROR `event=wac_send_ownership_read_failed` with `channel_uuid` and `msg_uuid`, then send as today.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run 'TestSendWACThreadOwner|TestSending'` (or existing WAC send test name) passes. Default-off send path unchanged.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Release notes and full-slice validation

- [ ] T017 Add a MINOR `WENI-CHANGELOG.md` entry (new version above `1.79.0`) listing Redis WAC thread ownership, `control_passed` without context, `control_taken`, and the two default-off flags `COURIER_ENABLE_WAC_STANDBY_CONTEXT` / `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK`
- [ ] T018 [P] Grep new log sites in `handlers/facebookapp/wa_thread_owner.go` and `handlers/facebookapp/facebookapp.go` and confirm new events never attach `contact_urn`, `sender_phone_number`, or message text; add a log-capture assertion in `handlers/facebookapp/wa_thread_owner_test.go` or `facebookapp_test.go` if missing
- [ ] T019 Run [quickstart.md](./quickstart.md) commands from repo root: `go test ./handlers/facebookapp/ -count=1 -run 'TestWACThreadOwner|TestReceiveWACHandover|TestReceiveWACThreadOwner|TestReceiveWACStandbyContext|TestSendWACThreadOwner'` and `go test . -count=1 -run TestNewConfigWACThreadOwnerFlags`; fix any rename drift so the suite named in quickstart exists and is green

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies
- **Foundational (Phase 2)**: Depends on Setup (flags exist for later stories; Lua does not read flags)
- **US1 (Phase 3)**: Depends on Phase 2 (`apply`) — MVP
- **US2 (Phase 4)**: Depends on Phase 2; independently testable with seeded Redis (does not require US1 behaviour, but shares `processCloudWhatsAppPayload` with US3 — do US2 before US3)
- **US3 (Phase 5)**: Depends on Phase 1 flags; persist path does not need Redis. File overlap with US2 on `facebookapp.go` receive loop
- **US4 (Phase 6)**: Depends on Phase 2 (`get`) and Phase 1 send flag. Edits `sendCloudAPIWhatsappMsg`, not the receive loop — can overlap US1 in time if two people split files
- **Polish (Phase 7)**: After US1–US4

### User Story Dependencies

- **US1 (P1)**: After Phase 2. No other story.
- **US2 (P2)**: After Phase 2. Seeded keys simulate US1; can be tested without live handover.
- **US3 (P3)**: After Phase 1. After US2 to avoid receive-loop merge conflicts.
- **US4 (P3)**: After Phase 2. Independent of US2/US3.

### Parallel Opportunities

- T006 (fixtures) in parallel with nothing else in US1 until T007
- T012 (standby fixture) can start while US4 implementation is in `sendCloudAPIWhatsappMsg`
- After Phase 2: Developer A on US1 (`wa_handover.go`) and Developer B on US4 (`sendCloudAPIWhatsappMsg`) in parallel; then US2 then US3 on the receive loop

### Parallel Example: After Phase 2

```text
Developer A: T006–T009 (US1 handover) in handlers/facebookapp/wa_handover.go + testdata + TestReceiveWACHandover
Developer B: T015–T016 (US4 send) in handlers/facebookapp/facebookapp.go sendCloudAPIWhatsappMsg + TestSendWACThreadOwner
```

Do **not** parallel US2 and US3 (same `processCloudWhatsAppPayload` block).

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1: flags default false
2. Phase 2: Lua CAS + unit tests
3. Phase 3: US1 handovers
4. **STOP**: `TestReceiveWACHandover` + `TestWACThreadOwner`. Deploy with flags off — ownership keys start, sends unchanged

### Incremental Delivery

1. Setup + Foundational → Redis primitive ready
2. US1 → handover ownership in production (MVP)
3. US2 → messages refresh existing marks
4. US4 → enable send-block in staging when keys look sane
5. US3 → enable standby context persist independently
6. Polish: changelog + quickstart suite

### Parallel Team Strategy

1. Together: T001–T005
2. Then A: US1, B: US4
3. Then A: US2 then US3
4. Together: T017–T019

---

## Notes

- [P] = different files, no incomplete dependency
- Tests first in each story; they must fail before the implementing task
- Quote from data-model that implementers must not invent: key `wac:thread_owner:{channel_uuid}:{urn.Identity()}`; hash fields `state` (`owner` \| `not_owner`) and `ts`; TTL 86400 seconds from last **applied** write; incoming `<=` stored → no write; `update_if_exists` + missing key → `absent` and no write
- Redis key is not a public contract; do not add `Backend` methods
- Commit after each task or story checkpoint (Conventional Commits, Principle VII)
- Skip T017 until behaviour is in; changelog last among feature files
