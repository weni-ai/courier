# Tasks: WWC human-support forwarding

**Input**: Design documents from `/specs/009-wwc-human-support/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wwc-human-support.md](./contracts/wwc-human-support.md), [quickstart.md](./quickstart.md)

**Tests**: Included. Spec Test Strategy lists the flow cases and the failure matrix. Write each story’s tests first and confirm they fail before that story’s implementation.

**Stories**: The engineering spec has no product user stories. Increments follow the product journeys this repository can prove, and [quickstart.md](./quickstart.md):

| Story | Priority | Product journey | Increment |
|-------|----------|-----------------|-----------|
| US1 | P1 | SC-001 id half; enables Journey 2 | Store a valid callback `message.message_id` on `external_id`. Invalid or absent leaves it empty and still accepts the message |
| US2 | P1 (MVP with US1) | Journey 1 | Privileged POST plus `human_support_started`, `human_support_ended`, `human_support_state`. Times copied unchanged |
| US3 | P1 | Journey 2 | `message_status` `delivered` / `read`, including in-request HTTP 404 |
| US4 | P2 | Journey 3 | `author`, `agent_first_name`, named `typing_start`, WWC `typing_stop` |

Journey 4 (balloon time) and Journey 5 (reload) are socket history. Courier does not mint `timestamp_ms` and does not store milestones. No story for them.

No schema migration. Do not edit `utils/http.go`, `channel_log.go`, `handlers/test.go`, `backends/rapidpro/`, WhatsApp handlers, or upstream `CHANGELOG.md`.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US4 on story-phase tasks only
- Every task includes an exact file path

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: The Go module, `handlers/weniwebchat`, and `httptest` seams already exist. No new package or dependency.

**Checkpoint**: No setup files to create. Phase 2 can start.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The token field and the action-type constants. No send or receive behaviour changes in this phase.

**⚠️ CRITICAL**: US2–US4 name these symbols. US1 does not need them.

- [X] T001 [P] In `config.go`, add `WwcHumanSupportIngressToken string` on `courier.Config` with an empty default and a `help` tag that names `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN` (same secret as the socket `WWC_HUMAN_SUPPORT_INGRESS_TOKEN`; empty disables privileged `/send`). Place it next to the other `COURIER_*` secrets. ezconf `CamelToSnake("WwcHumanSupportIngressToken")` is `wwc_human_support_ingress_token`, so the env var is `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN`. Do not read the field from any handler yet.
- [X] T002 [P] In `msg.go`, next to `MsgActionTypingIndicator`, add `MsgActionHumanSupportStarted MsgActionType = "human_support_started"`, `MsgActionHumanSupportEnded = "human_support_ended"`, `MsgActionHumanSupportState = "human_support_state"`, `MsgActionMessageStatus = "message_status"`, and `MsgActionTypingStop = "typing_stop"`. Leave `MsgActionTypingIndicator = "typing_indicator"` unchanged. Do not branch on the new constants yet.

**Checkpoint**: `go test ./handlers/weniwebchat/ -count=1` still passes. No new HTTP header and no new JSON field.

---

## Phase 3: User Story 1 - Store the shopper message id (Priority: P1)

**Goal**: `POST /c/wwc/<uuid>/receive` copies a valid `message.message_id` onto `msgs_msg.external_id`. Constraint from data-model: set it when that string is 1–64 characters and has no Unicode space; otherwise leave `external_id` empty and still queue the message. `received_on` stays `time.Unix` of `message.timestamp` in seconds. Do not read `author`, `delivery_status`, `support_id`, or `timestamp_ms` from the callback.

**Independent Test**: `go test ./handlers/weniwebchat/ -count=1 -run TestHandler`. The UUID id is `ExternalID()`. A missing id, a 65-character id, and an id with a space are accepted with an empty `ExternalID()`.

### Tests for User Story 1

> Write T003 first. New cases fail until T004. Existing `testCases` must stay green.

- [X] T003 [US1] In `handlers/weniwebchat/weniwebchat_test.go`, add `testCases` entries consumed by `TestHandler` / `RunChannelTestCases`. Start from an existing accepted text callback. (1) `message.message_id` `3f1a8c0e-6b2d-4e5a-9c7d-1b0e4a6c8d20` and `ExternalID: Sp("3f1a8c0e-6b2d-4e5a-9c7d-1b0e4a6c8d20")`. (2) the same text with no `message_id` and no `ExternalID` (empty). (3) `message_id` of 65 characters: message accepted, `ExternalID` empty. (4) `message_id` that contains a space: message accepted, `ExternalID` empty. Do not assert `timestamp_ms`.

### Implementation for User Story 1

- [X] T004 [US1] In `handlers/weniwebchat/weniwebchat.go`, add `wwcMaxMessageIDLen` = 64 and optional `MessageID string \`json:"message_id,omitempty"\`` on `miMessage`. In `receiveMsg`, after today’s type and timestamp checks: if `message_id` is absent or empty, leave `external_id` empty. If present, accept only when the length is 1–64 (`wwcMaxMessageIDLen`) and no rune has `unicode.IsSpace`; then `msg.WithExternalID`. Otherwise drop the field, log `message_id_rejected: true` without the raw value, and still queue the message. Do not store `timestamp_ms`. Do not fail the webhook for a bad id.

**Checkpoint**: `go test ./handlers/weniwebchat/ -count=1 -run 'TestHandler|TestSending|TestActions'` passes. Outbound bodies are unchanged.

---

## Phase 4: User Story 2 - Forward start, end, and open state (Priority: P1) 🎯 MVP

**Goal**: `SendMsg` POSTs the three control types to `{base_url}/send` and does not build a chat `type: message`. `occurred_at_ms` is copied unchanged. Privileged calls send `Authorization: Bearer` from `WwcHumanSupportIngressToken`. Empty token, a missing, empty, or over-long `event_id`, a non-positive `occurred_at_ms`, and `open` not `true` are `MsgFailed` with no HTTP call. A valid `event_id` is 1–128 characters. HTTP 202 is `MsgSent`. 401, 400, and 409 are `MsgFailed` after one request. 403, and 404 on these types, are `MsgErrored` after one request. 500, 429, 502, 503, 504, and transport errors retry up to 3 times inside the 35s send context, then `MsgErrored`. A send context that is already cancelled or expired is `MsgErrored`, and the httptest hit count does not exceed 3.

**Independent Test**: `go test ./handlers/weniwebchat/ -count=1 -run TestHumanSupportControl`. Started body matches the contract example. `TestSending` and `TestActions` still pass.

### Tests for User Story 2

> Write T005 first. It fails until T006. Use `handlers.NewTestServer`, set `server.Config().WwcHumanSupportIngressToken = "test-token"`, and call `SendMsg`. Do not add a config hook to `RunChannelSendTestCases` or `RunChannelActionTestCases`.

- [X] T005 [US2] Add `TestHumanSupportControl` in `handlers/weniwebchat/weniwebchat_test.go`. Channel address `250788383383`, channel UUID `8eb23e93-5ecb-45ba-b726-3b064e0c568c`, token `test-token`. Count httptest hits. Cases: (1) `action_type` `human_support_started`, `event_id` `evt-1`, `occurred_at_ms` `1730000000123`, socket 202 → body `type` `human_support_started`, `to` URN path, `from` `250788383383`, `channel_uuid` that UUID, `event_id` `evt-1`, `occurred_at_ms` 1730000000123, no `message` object, header `Authorization: Bearer test-token`, `MsgSent`. (2) socket body `{"message":"human support started","result":"replay"}` with 202 is still `MsgSent`. (3) `human_support_ended` with a different positive `occurred_at_ms` → that number unchanged, `MsgSent`. (4) `human_support_state` with `open: true` → body contains `"open":true` and no chat `message`, `MsgSent`. (5) empty token → zero requests, `MsgFailed`. (6) `event_id` length 129, missing `event_id`, `event_id` present and empty, `occurred_at_ms` 0, `open: false`, missing `open` → zero requests, `MsgFailed` each. (7) socket 401, 400, 409 → one request each, `MsgFailed`. (8) socket 403 → one request, `MsgErrored`. (9) socket 404 on `human_support_started` → one request, `MsgErrored`. (10) socket 500 twice then 202 → three requests, `MsgSent`; three 500s → three requests, `MsgErrored`. (11) channel log request dump for case (1) does not contain `test-token`. `Idempotency-Key` on the retries of case (10) is `evt-1` every time. (12) the send context is already cancelled before `SendMsg` returns → `MsgErrored`, and the httptest hit count is at most 3.

### Implementation for User Story 2

- [X] T006 [US2] In `handlers/weniwebchat/weniwebchat.go`, add `wwcPrivilegedAttempts` = 3, `wwcPrivilegedBaseBackoff` = 500ms, `wwcMaxEventIDLen` = 128. Add `postPrivileged`: `utils.MakeHTTPRequest` on `utils.GetHTTPClient` (never `http.DefaultClient`, never `MakeHTTPRequestWithRetry`). Retry HTTP 500, 429, 502, 503, 504, and transport errors. Do not retry 401, 403, 400, 409, or 404 in this task. Local jitter copies `utils.withJitter` (`backoff := base << attempt`, then half plus a random half). Do not export `withJitter` and do not edit `utils/http.go`. `postPrivileged` takes the idempotency key as an argument, sets `Idempotency-Key` to that value once, and reuses it on every attempt of that call. `human_support_started`, `human_support_ended`, and `human_support_state` pass `event_id`. Do not hardcode the key inside the helper. `Authorization: Bearer <token>`. Content-Type `application/json`. Empty token returns `MsgFailed` and does not dial. Map 202 → `MsgSent`; 401, 400, 409 → `MsgFailed`; 403 and 404 → `MsgErrored`; exhausted retry or the 35s context deadline → `MsgErrored`. Before `NewChannelLogFromRR`, replace the `Authorization` header value with `Bearer [redacted]` and the JSON string of `agent_first_name` with `[redacted]`. Do not edit `channel_log.go`. Logrus fields only: `channel_uuid`, `msg_uuid`, `event_id`, `action_type`, `http_status`, `socket_error` (JSON `error` string when present). No token, name, URN path, or message text. In `SendMsg`, before the chat builder, handle `human_support_started`, `human_support_ended`, and `human_support_state`. Do not send the row’s text, attachments, or products. `to` is `msg.URN().Path()`, `from` is `msg.Channel().Address()`, `channel_uuid` is the channel UUID. `event_id` missing, empty, or longer than 128 (`wwcMaxEventIDLen`) is `MsgFailed` with no POST. A valid id is 1–128 characters. `occurred_at_ms` on start and end must be a positive integer and is copied unchanged. `human_support_state` requires `open: true`; `false` or absent is `MsgFailed` with no POST. Read metadata with `jsonparser` (`GetInt` for `occurred_at_ms`, `GetBoolean` for `open`). These three types always use `postPrivileged`. Leave unlabeled chat and `SendAction` on today’s helpers.

**Checkpoint**: `go test ./handlers/weniwebchat/ -count=1 -run 'TestHumanSupportControl|TestSending|TestActions'` passes. A start is not a chat bubble. 404 on start is one try.

---

## Phase 5: User Story 3 - Forward delivered and read (Priority: P1)

**Goal**: `message_status` uses the same privileged POST. `status` is `delivered` or `read`. `message_ids` is optional; absent is valid only for `read`. Present and empty, a non-string element, more than 100 ids, or `delivered` without ids is `MsgFailed` with no POST. `sending` or any other status is `MsgFailed` with no POST. HTTP 404 on this type only retries inside the send (3 attempts, 500ms backoff with jitter). Three 404s is `MsgErrored`. A 202 on a later try is `MsgSent`. 404 on the US2 types stays one request.

**Independent Test**: `go test ./handlers/weniwebchat/ -count=1 -run 'TestHumanSupportStatus|TestHumanSupportControl'`. `read` with no ids and `delivered` with one id are `MsgSent` on 202. A 404 that becomes 202 takes three requests.

### Tests for User Story 3

> Write T007 first. It fails until T008.

- [X] T007 [US3] Add `TestHumanSupportStatus` in `handlers/weniwebchat/weniwebchat_test.go`, same server and token setup as T005. Cases: (1) `action_type` `message_status`, `status` `read`, no `message_ids`, `event_id` set, socket 202 → body `type` `message_status`, `status` `read`, no `message_ids`, no chat `message`, bearer present, `MsgSent`. (2) `status` `delivered` with one id → that id in `message_ids`, `MsgSent`. (3) `status` `sending`, `delivered` with no ids, `message_ids` `[]`, a non-string element, and 101 ids → zero requests, `MsgFailed` each. (4) `event_id` length 129 → zero requests, `MsgFailed`. (5) socket 404, 404, then 202 → three requests, `MsgSent`. (6) 404 three times → three requests, `MsgErrored`. (7) `Idempotency-Key` is that `event_id` on every try. Do not change the US2 assertion that a start’s 404 is a single request.

### Implementation for User Story 3

- [X] T008 [US3] In `handlers/weniwebchat/weniwebchat.go`, add `wwcMaxStatusMessageIDs` = 100. Handle `action_type` `message_status` on the US2 control path (no chat body). `status` must be `delivered` or `read`. `message_ids` via `jsonparser.ArrayEach`: absent allowed only for `read`; present and empty, a non-string element, more than 100 ids, or `delivered` without ids → `MsgFailed`, no POST. `sending` or any other status → `MsgFailed`, no POST. Pass a flag into `postPrivileged` so HTTP 404 is retried only when the JSON `type` is `message_status`, with the same `wwcPrivilegedAttempts` and `wwcPrivilegedBaseBackoff` jitter. 404 on every other privileged type stays a single request then `MsgErrored`. Same `event_id` on every try. Reuse the T006 `event_id` rule: missing, empty, or longer than 128 is `MsgFailed` with no POST. Pass `event_id` as the idempotency-key argument.

**Checkpoint**: `go test ./handlers/weniwebchat/ -count=1 -run 'TestHumanSupportStatus|TestHumanSupportControl'` passes. Status 404 retries. Start 404 does not.

---

## Phase 6: User Story 4 - Forward the agent name and typing (Priority: P2)

**Goal**: Chat and product sends add top-level `author` and `agent_first_name` when Engine set them. `author` is `ai_agent` or `human_agent`; any other value is `MsgFailed` with no POST. Absent `author` omits the field. Trim the name; empty after trim omits it. Internal Unicode space, or more than 100 runes, is `MsgFailed` with no POST. `ai_agent` together with a non-empty name is `MsgFailed` with no POST. Do not take the first token of a longer name. A body is privileged when `author` is `human_agent` or `agent_first_name` is non-empty (including typing). `ai_agent` with no name stays on today’s unauthenticated `MakeHTTPRequestWithRetry`. Nameless `typing_start` stays `{"type":"typing_start","to":"<urn path>","from":"ai-assistant","channel_uuid":"<uuid>"}` with no `Authorization`. `typing_stop` on WWC uses the sender’s best-effort path and writes no `MsgStatus`. An invalid typing name or an empty token on privileged typing returns an error and does not POST.

**Independent Test**: `go test ./handlers/weniwebchat/ -count=1 -run 'TestHumanSupportChat|TestActions'` and `go test . -count=1 -run TestBestEffortAction`. `TestActions` still matches the nameless body. `typing_stop` is best-effort only for channel type `WWC`.

### Tests for User Story 4

> Write T009 and T010 first. They fail until T011 and T012. T009 and T010 touch different files and can be written together.

- [X] T009 [P] [US4] Add `TestHumanSupportChat` in `handlers/weniwebchat/weniwebchat_test.go` with the T005 server and token. Cases: (1) text, `author` `ai_agent`, no name, socket 202 → body has `"author":"ai_agent"`, no `Authorization`, no `agent_first_name`, `MsgSent`. (2) text, `author` `human_agent`, `agent_first_name` `Julia`, 202 → existing text body plus those two top-level fields (not inside `message`), `Authorization: Bearer test-token`, `Idempotency-Key` equal to that msg UUID on the request, `MsgSent`, channel log contains neither `test-token` nor `Julia`. (3) product-list msg with the same human-agent metadata → bearer and both fields on the product body, `MsgSent`. (4) name `Julia Silva`, a 101-rune name, `author` `shopper`, `author` `ai_agent` plus a name → zero requests, `MsgFailed` each. (5) privileged chat with empty token → zero requests, `MsgFailed`. (6) `SendAction` `typing_indicator` with `agent_first_name` `Julia` → `type` `typing_start`, the name, and the bearer. (7) `SendAction` `typing_stop` with and without a name → `type` `typing_stop`; bearer only when the name is set. (8) invalid typing name → zero requests, error returned. Keep `TestActions` as the nameless `typing_start` case with no bearer. A nameless `typing_start` against socket 500 stays one `MakeHTTPRequest` error, no bearer.
- [X] T010 [P] [US4] Add `TestBestEffortAction` in `sender_test.go`. Build `mockMsg` values with `metadata` `action_type` and `NewMockChannel`. Assert: `typing_indicator` is best-effort for channel type `WWC` and for a non-WWC type; `typing_stop` is best-effort only when `ChannelType()` is `WWC`; `human_support_started` is not best-effort. The test calls the predicate only. It does not POST.

### Implementation for User Story 4

- [X] T011 [US4] In `handlers/weniwebchat/weniwebchat.go`, add `wwcMaxAgentFirstNameRunes` = 100. Do not reuse `wwcMaxMessageIDLen` for names. Add `Author` and `AgentFirstName` to `moPayload` with `omitempty`. One helper used by the text/attachment `SendMsg` path and by `sendProductMessage` / `sendPayload`: `author` `ai_agent` or `human_agent` is copied to the top level; any other value is `MsgFailed` and does not POST; absent omits `author`. Trim `agent_first_name`; empty omits it; internal Unicode space or more than 100 runes is `MsgFailed` and does not POST; non-empty together with `ai_agent` is `MsgFailed` and does not POST. Do not split the name. Privileged when `author` is `human_agent` or the name is non-empty: call `postPrivileged`, passing `msg.UUID()` as the idempotency-key argument from T006 (the same value on every attempt of that call), and do not retry 404. Otherwise keep `MakeHTTPRequestWithRetry` for chat and `MakeHTTPRequest` for nameless typing. In `SendAction`, map `typing_indicator` to `typing_start` and `typing_stop` to `typing_stop`. Nameless `typing_start` keeps `from` `ai-assistant` and no `Authorization`. A valid name makes that request privileged. An invalid name or an empty token on privileged typing returns an error and does not POST. Do not write `MsgStatus` from `SendAction`.
- [X] T012 [P] [US4] In `sender.go`, extract `bestEffortAction(msg Msg) bool` and use it in place of the `msg.ActionType() == MsgActionTypingIndicator` check in `sendMessage`. Return true for `MsgActionTypingIndicator` on every channel. Return true for `MsgActionTypingStop` only when `msg.Channel().ChannelType() == "WWC"`. The existing early return stays: 20s context, `is_action`, `SendMsgAction`, warning log, `return` before `WriteMsgStatus`. Control events stay on the `SendMsg` path. Do not add a typing branch inside `handlers/weniwebchat` `SendMsg`.

**Checkpoint**: `go test ./handlers/weniwebchat/ -count=1` and `go test . -count=1 -run TestBestEffortAction` pass. Unlabeled chat JSON and nameless `typing_start` match the existing fixtures.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T013 [P] In `WENI-CHANGELOG.md`, add a minor bullet under a new heading immediately above the current newest (`1.80.0` at task-generation time). If a newer heading already exists when this ships, add the bullet there instead of creating another. Describe WWC human-support forwarding: privileged `/send` with `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN`, control events, shopper `message_id` on `external_id`, and in-request `message_status` 404. Do not edit upstream `CHANGELOG.md`.
- [X] T014 Run [quickstart.md](./quickstart.md): `go test ./handlers/weniwebchat/ -count=1` and `go test . -count=1 -run 'TestBestEffortAction'`.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: empty
- **Foundational (Phase 2)**: T001 and T002 in parallel. Blocks US2–US4. US1 can start without them
- **US1 (Phase 3)**: T003 then T004. No dependency on Phase 2
- **US2 (Phase 4)**: after Phase 2. T005 then T006. Builds `postPrivileged`
- **US3 (Phase 5)**: after US2. T007 then T008. Adds the 404 exception on the helper US2 created
- **US4 (Phase 6)**: T011 after US2 (`postPrivileged` exists). T012 after T002 and T010 only. T009 and T010 can be written together
- **Polish**: after US1–US4

### User Story Dependencies

- **US1 (P1)**: first checkpoint. Does not change `/send`
- **US2 (P1)**: MVP together with US1 (SC-001: stored shopper id, and start/end times copied unchanged)
- **US3 (P1)**: after US2. Independently tested by `TestHumanSupportStatus` plus the US2 404-on-start case
- **US4 (P2)**: chat/typing handler after US2. Sender predicate does not wait for US2 or US3

### Within Each User Story

- Tests fail before the implementation task in that story
- US2’s helper exists before US3’s 404 flag and before US4’s privileged chat
- A story checkpoint is green before the next story’s implementation

### Parallel Opportunities

- T001 and T002 (different files)
- T003/T004 (US1) in parallel with T001/T002
- T009 and T010 (test files) once US2 has landed for T009’s server helper; T010 does not need US2
- T011 and T012 (handler vs `sender.go`) after their tests
- T013 while T014 is being prepared; run T014 last

---

## Parallel Example: User Story 4

```bash
# Write both failing tests together:
Task: "T009 TestHumanSupportChat in handlers/weniwebchat/weniwebchat_test.go"
Task: "T010 TestBestEffortAction in sender_test.go"

# Then implement the two files together:
Task: "T011 author, name, and SendAction in handlers/weniwebchat/weniwebchat.go"
Task: "T012 bestEffortAction in sender.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 and User Story 2)

1. Phase 2 constants and token field
2. US1: shopper `message_id` on `external_id`. Stop and run `TestHandler`
3. US2: start, end, and open state. Stop and run `TestHumanSupportControl`
4. That pair is the courier half of SC-001. Status, name, and typing are not in this stop

### Incremental Delivery

1. US1 does not change outbound JSON
2. US2 adds privileged control events. Unlabeled chat and nameless typing stay on the old helpers
3. US3 adds `message_status` and the only in-request 404
4. US4 adds the name, the human-agent bearer on chat and products, and WWC `typing_stop`
5. T013 and T014 after all four checkpoints

### Parallel Team Strategy

1. One person lands T001 and T002
2. Then: one person runs US1, another starts US2 tests
3. After T006: one person runs US3, another writes T010 and T012 (sender), then T009 and T011 once `postPrivileged` exists

---

## Notes

- [P] tasks are different files and do not depend on an unfinished task
- Do not retry HTTP 500 in `utils.shouldRetryTransient`. The 500 retry is only inside `postPrivileged`
- Do not enqueue these `action_type` values from Engine until this build is serving and `WWC_HUMAN_SUPPORT_STATUS` is on. A 403 stays on the mailroom `MsgErrored` schedule
- Commit after each checkpoint. Do not commit unless asked
