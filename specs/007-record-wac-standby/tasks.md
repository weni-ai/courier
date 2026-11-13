# Tasks: Record WAC Standby

**Input**: Design documents from `/specs/007-record-wac-standby/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-standby.md](./contracts/wac-standby.md), [quickstart.md](./quickstart.md)

**Tests**: Included. Spec Test Strategy maps AC-01…AC-13 to tests. Write each story's new assertions first and confirm they fail before the implementation task for that story.

**Stories**: Spec has acceptance criteria, not product user stories. Mapped to independently testable increments:

| Story | Priority | Spec ACs | Increment |
|-------|----------|----------|-----------|
| US1 | P1 (MVP) | AC-03, AC-04, AC-05, AC-06 (line text) | Pure renderer emits shopper, echo, flow, template, and media lines |
| US2 | P2 | AC-01, AC-02, AC-09, AC-12, AC-13, webhook half of AC-03…AC-06 | `WAC` `field=standby` appends those lines, never calls `WriteMsg`, and the channel log drops the body even when the flag is off |
| US3 | P3 | AC-07, AC-08, AC-10, AC-11 | `not_owner` with the PR 335 timestamp rule, status receipts with no line, 24h dedup, writer error releases the key |

No new `COURIER_*` flag and no schema migration. Phase 1 and Phase 2 add no tasks.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US3 on story-phase tasks only
- Every task includes an exact file path

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: No new module, no `COURIER_*` flag, and no migration. `wa_conversation_handover` stays as in `backends/rapidpro/schema.sql`.

**Checkpoint**: No setup files to create. US1 can start.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Nothing to add under all stories. `persistRenderedWAHandover`, `applyWACThreadOwner`, `luaWACThreadOwnerApply`, `SetErrorOnWAHandover`, `GetLastChannelLog`, and `EnableWACStandbyContext` (default false) already exist.

**Checkpoint**: Foundation is the current tree. User stories start at Phase 3.

---

## Phase 3: User Story 1 - Render standby lines (Priority: P1) 🎯 MVP

**Goal**: Turn a shopper message or a partner echo into one history line. Flow, template, and media follow the contract strings. No webhook, no Redis, no `WriteMsg` (AC-03, AC-04, AC-05, AC-06).

**Independent Test**: `go test ./handlers/facebookapp/ -count=1 -run TestRenderWACStandby`. No Postgres and no Redis.

### Tests for User Story 1

> Write these tests FIRST. Assertions fail until T002. Stub the two functions so the test compiles.

- [ ] T001 [US1] Add `handlers/facebookapp/wa_standby_test.go` `TestRenderWACStandby`. In `handlers/facebookapp/wa_standby.go` add only `renderWACStandbyShopper` and `renderWACStandbyEcho`, both returning `("", false)`, so the test compiles and fails. Assert these exact strings: (1) documented flow echo (header `Book Appointment`, body `Schedule your visit with us.`, footer `Tap below to continue`, `flow_cta` `Book Now`) → `[outro atendimento] Book Appointment — Schedule your visit with us. — Tap below to continue — Book Now` (separator is space, U+2014, space); (2) flow with only `flow.name` `Appointment Booking` → `[outro atendimento] Appointment Booking`; (3) flow with no text and no name → `[outro atendimento] <flow>`; (4) documented `summer_sale_2026` echo with sibling `template` → `[outro atendimento] <image>\nHi Maria, enjoy 25% off!\nLimited time\nShop Now: https://example.com/summer2026` (origin marker on the first line only); (5) a body `{{3}}` with only two text parameters stays `{{3}}`; (6) the same send-time parameters and no sibling `template` → `[outro atendimento] template:summer_sale_2026 <image> | Maria | 25% | summer2026`; (7) template name `summer_sale_2026` and no parameter values → `[outro atendimento] template:summer_sale_2026`; (8) shopper `type=image` → `[shopper] <image>`; shopper `type=audio` → `[shopper] <audio>`; echo `type=image` → `[outro atendimento] <image>`; (9) shopper text `Test standby message` → `[shopper] Test standby message`; echo text `Hello! Your order #12345 has shipped.` → `[outro atendimento] Hello! Your order #12345 has shipped.`; (10) text body that is only spaces returns ok `false`; (11) echo body `Hi {{customer_name}}` with one send-time text parameter whose `parameter_name` is `customer_name` and whose `text` is `Maria` → `[outro atendimento] Hi Maria`. Do not call `resolveMediaURL`. Do not read Redis.

### Implementation for User Story 1

- [ ] T002 [US1] Fill `renderWACStandbyShopper` and `renderWACStandbyEcho` in `handlers/facebookapp/wa_standby.go`. Constants: `wacStandbyOriginShopper = "shopper"`, `wacStandbyOriginOther = "outro atendimento"`. Prefix `[{origin}] ` on the first line only; further lines of the same message join with `\n` and do not repeat the prefix. Text: trimmed `text.body`; empty → `("", false)`. Media types `image`, `audio`, `video`, `document`, `voice`, `sticker` → `[{origin}] <type>` only (no caption, no id, no mime). Flow: non-empty interactive header text, body text, footer text, and `action.parameters.flow_cta` joined by ` — `; else sibling `flow.name`; else `<flow>`. Template with sibling `template.components`: HEADER format `IMAGE`/`VIDEO`/`DOCUMENT`/`AUDIO` is that marker; HEADER/BODY `{{n}}` (`n` starts at 1) is replaced by the nth text parameter of the matching send-time component, in array order; a `parameter_name` also replaces `{{parameter_name}}`; unmatched `{{n}}` stays; FOOTER text is its own line; each button is `{text}` or `{text}: {url}` after the same substitution, aligned by button index. Template without the sibling object: `[outro atendimento] template:{name}` plus, when any values exist, a space and values joined by ` | ` in send-time component order (text parameters contribute `text`; media parameters contribute `<image>` / `<video>` / `<document>` / `<audio>`). Missing template name → `false`. Any other type: button text or interactive reply title when non-empty, else `[{origin}] <type>`. Do not import the Graph client. Do not edit `facebookapp.go`.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run TestRenderWACStandby` passes. No webhook behaviour has changed. This is the MVP of the line rules.

---

## Phase 4: User Story 2 - Append standby lines and omit the body (Priority: P2)

**Goal**: With the flag on, a `WAC` standby webhook appends one `history` line per rendered shopper message or echo through `persistRenderedWAHandover`, and does not call `WriteMsg`, `WriteMsgStatus`, or `WriteChannelEvent`. With the flag off, it still logs `ignoring standby webhook` and writes no row. The HTTP body is `[standby body omitted]` on the channel log in both cases. `WCD` does not gain transcript lines (AC-01, AC-02, AC-09, AC-12, AC-13, webhook half of AC-03…AC-06).

**Independent Test**: `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACStandbyTranscript|TestReceiveWACStandbyContext'`. Shopper text yields one history line and `LenQueuedMsgs()==0`. Flag off yields zero events. `GetLastChannelLog().Request` contains `[standby body omitted]` and not the message text. `helloWAC.json` still logs `Hello World`.

### Tests for User Story 2

> Write these tests FIRST. The new line assertions and the log-omission assertions fail until T006. `TestReceiveWACStandbyContext` flag-off (zero events, no Redis write) and flag-on summary persist already pass and must stay. On that flag-off subtest (`handoverStandbyContextWAC.json`), also assert `GetLastChannelLog().Request` contains `[standby body omitted]` and does not contain `Standby summary of the conversation.` Do not yet require `not_owner` or a seen key; that is US3. Leave `TestReceiveWACStandbyContext` flag-on Redis assertion as empty until T008.

- [ ] T003 [P] [US2] Add fixtures under `handlers/facebookapp/testdata/wac/`. Envelope matches `handoverStandbyContextWAC.json` (`object` `whatsapp_business_account`, `entry[0].id` `8856996819413533`, `entry[0].time` `1454119029`, `field` `standby`, `metadata.phone_number_id` `12345`) but content lives under `value.standby`, not `value.messages`. `standbyShopperTextWAC.json`: contact `wa_id` `5678`, message `from` `5678`, `id` `wamid.SHOPPER`, `timestamp` `1454119029`, `type` `text`, `text.body` `Test standby message`. `standbyEchoTextWAC.json`: echo `id` `wamid.ECHO`, `timestamp` `1454119029`, `message.to` `5678`, `message.type` `text`, `message.text.body` `Hello! Your order #12345 has shipped.`. `standbyEchoFlowWAC.json`: echo `id` `wamid.FLOW`, interactive flow with header `Book Appointment`, body `Schedule your visit with us.`, footer `Tap below to continue`, `flow_cta` `Book Now`, `message.to` `5678`. `standbyEchoTemplateWAC.json`: the documented `summer_sale_2026` echo (`id` `wamid.TEMPLATE`, `message.to` `5678`) including sibling `template`. `standbyEchoTemplateNoStructureWAC.json`: the same send-time `message.template` and no sibling `template` (`id` `wamid.TEMPLATE.BARE`). `standbyShopperImageWAC.json`: `type` `image`, `id` `wamid.IMAGE`, `from` `5678`, image id `media-1` (must not be fetched). `standbyShopperAudioWAC.json`: `type` `audio`, `id` `wamid.AUDIO`, `from` `5678`. Do not put `conversation_context` on these files.
- [ ] T004 [US2] Add `TestReceiveWACStandbyTranscript` in `handlers/facebookapp/facebookapp_test.go`. Use `handlers.NewTestServer`, `testChannelsWAC[0]`, `handlers.TestHandlerRequest` on `/c/wac/receive` with `addValidSignatureWAC`. Each subtest builds its own `MockBackend`. Flag on unless noted. Cases: (1) `standbyShopperTextWAC.json` → HTTP 200, `WAHandoverEvents()` length 1, `ContextType` `history`, `ContextText` `[shopper] Test standby message`, `ContactURN` `whatsapp:5678`, `OccurredOn` `time.Unix(1454119029, 0).UTC()`, `ContextPayload` nil, `LenQueuedMsgs()==0`, `BackendCalls()` contains `wa_handover` and not `msg`, `GetLastMsgStatus` returns an error, `GetLastChannelEvent` returns an error; (2) `standbyEchoTextWAC.json` → one line `[outro atendimento] Hello! Your order #12345 has shipped.`; (3) `standbyEchoFlowWAC.json` → the flow line from T001; (4) `standbyEchoTemplateWAC.json` → the structured template line from T001; (5) `standbyEchoTemplateNoStructureWAC.json` → the name-and-values line from T001; (6) image and audio fixtures → `[shopper] <image>` and `[shopper] <audio>`, HTTP 200 with no media `httptest` server; (7) shopper text with `EnableWACStandbyContext` left false → HTTP 200, body contains `ignoring standby webhook`, zero handover events, zero queued msgs; (8) after (1) and after (7), `GetLastChannelLog().Request` contains `[standby body omitted]` and does not contain `Test standby message` or `5678`; (9) `helloWAC.json` (`field=messages`) → `GetLastChannelLog().Request` still contains `Hello World`; (10) shopper fixture on `WCD` via `newWACDemoHandler` and POST `/c/wcd/{uuid}/receive` with the flag on → zero handover events, zero queued msgs. Cases (1)–(6) and (8) fail until T006. (7), (9), and (10) must stay true after T006.

### Implementation for User Story 2

- [ ] T005 [US2] In `server.go` `channelHandleWrapper`, after the 30s context is created and before `handlerFunc`, store an unexported `*channelLogBodyControl` (`omit bool`) on that context. Export `MarkChannelLogBodyOmitted(ctx context.Context)` in package `courier`: set `omit` when the pointer is present, no-op when it is not. `omitChannelLogBody(dump []byte) []byte` splits on the first `\r\n\r\n`, keeps the request line and headers, and replaces the body with `[standby body omitted]`. If the separator is missing, the whole dump becomes that placeholder. Call it from the panic `defer` before the logrus `request` field, and assign the result back to `request` immediately after `handlerFunc` returns, before the error logrus line and before every `NewChannelLog`. Do not parse WhatsApp JSON in `server.go`. Do not skip writing the channel log.
- [ ] T006 [US2] Add `Standby json.RawMessage \`json:"standby,omitempty"\`` on the `moPayload` value struct in `handlers/facebookapp/facebookapp.go`. Replace the body of the `field == standby` branch (today lines 1147–1168) with: (1) `courier.MarkChannelLogBodyOmitted(ctx)` before the flag check; (2) flag false: keep `ignoring standby webhook` and `continue` (no Redis, no `WriteWAConversationHandover`); (3) flag true and `channel.ChannelType() != "WAC"`: today's `persistWAHandoverContext` only, then `continue`; (4) flag true and `WAC`: `processWACStandby` in `handlers/facebookapp/wa_standby.go`, then `continue`. `processWACStandby` unmarshals `value.standby` into types local to that file (`messages`, `message_echoes` with `message.to` / `message.recipient` / `template` / `interactive` / sibling `template` and `flow`, `statuses` parsed but not applied until T008). For each shopper message and echo, in that order: URN is `urns.NewWhatsAppURN(from)` when `from` is set, else `from_user_id`; echo uses `message.to`, else `message.recipient`. No URN: `logrus` INFO `event=wac_standby_skipped`, `reason=no_urn`, `channel_uuid`, `external_id` when present, and continue. `OccurredOn` is the parsed item timestamp, or `time.Unix(entry.Time, 0).UTC()` when the timestamp is empty or not an integer. Render with T002. `("", false)` or empty message id: do not append (empty id logs `reason=no_message_id`). Otherwise `persistRenderedWAHandover` with `courier.WAHandoverContextHistory`, the line, contact name from `wacHandoverContactName` on standby contacts, that `OccurredOn`, nil context pointer, nil `controlPassed`. `context_payload` stays nil. A non-nil error returns from `processCloudWhatsAppPayload` (`return nil, nil, err` → HTTP 400). Do not `SET` a seen key and do not call `applyWACThreadOwner` in this task. After the item loop, if `conversation_context` still renders, call `persistWAHandoverContext` as today (error still fails the webhook). The branch must `continue` before `for _, msg := range change.Value.Messages`. Do not call `resolveMediaURL`, `WriteMsg`, `WriteMsgStatus`, or `WriteChannelEvent`. Success info strings, with no message text: `standby context recorded` when any line was written; keep `wa conversation handover persisted` for a legacy summary; `standby without conversation context` when nothing was appended. Close any Redis connection before `persistRenderedWAHandover` if one was opened. Do not add a `Config` field.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run 'TestRenderWACStandby|TestReceiveWACStandbyTranscript|TestReceiveWACStandbyContext|TestReceiveWACMessagesContext'` passes. Lines append. The body is omitted. `field=messages` still queues a msg and still logs `Hello World`. Redis ownership from standby is still absent.

---

## Phase 5: User Story 3 - Ownership, dedup, and redelivery (Priority: P3)

**Goal**: Flag-on standby sets `not_owner` through the existing Lua script (`incoming <= stored` is `stale`, mode `set`). Statuses ACK with no line. Message ids are claimed in Redis for 24 hours, per channel. A failed context write deletes that key and returns HTTP 400 so the retry can append (AC-07, AC-08, AC-10, AC-11).

**Independent Test**: `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACStandbyTranscript|TestReceiveWACStandbyContext'`. Seeded `owner` at ts `200` stays `owner` when the item timestamp is `100`, and the line is still appended. A second post of the same id does not append. Writer error leaves the seen key absent; the retry appends one line.

### Tests for User Story 3

> Write these tests FIRST. They fail until T008. US2 cases must stay green.

- [ ] T007 [US3] Extend `TestReceiveWACStandbyTranscript` in `handlers/facebookapp/facebookapp_test.go` and `assertNoWACOwnershipPII` in `handlers/facebookapp/wa_thread_owner_test.go`. Use `seedThreadOwner` and `threadOwnerHash` (`whatsapp:5678`). Cases, flag on: (1) seed `owner` / ts `200`, shopper fixture with `timestamp` `100` → hash stays `owner` / `200`, the line is still appended; (2) no seed, timestamp `150` → `not_owner` / `150`; (3) seed `owner` / `100`, timestamp `150` → `not_owner` / `150`; (4) a second message id with timestamp `150` after (3) → `state` and `ts` stay `not_owner` / `150` and that second line is appended; (5) post `standbyShopperTextWAC.json` twice → one handover event, `TTL` of `wac:standby:seen:{channel_uuid}:wamid.SHOPPER` is in `(0, 86400]`; (6) same body with `metadata.phone_number_id` and mock channel address `99999` (new UUID via `courier.NewMockChannel`, type `WAC`) → one event on that channel and a different seen key; (7) `standbyStatusWAC.json` (`recipient_id` `5678`, `status` `delivered`, `id` `wamid.STATUS`, `timestamp` `1454119029`) → HTTP 200, response body contains `standby status recorded` and does not contain `standby without conversation context`, zero handover events, `not_owner`, `GetLastMsgStatus` error, `LenQueuedMsgs()==0`, and `EXISTS` on `wac:standby:seen:*wamid.STATUS` is 0; (8) `SetErrorOnWAHandover(true)` on the shopper fixture → HTTP 400, zero events, seen key absent (`EXISTS` 0); clear the error and post again → HTTP 200 and one line; (9) `mb.RedisPool().Close()` before the shopper post → HTTP 400, zero handover events; (10) item timestamp `not-a-number`, seeded `owner` → `owner` unchanged, line still appended (`OccurredOn` from `entry.time` `1454119029`); (11) text body only spaces → zero handover events; (12) flag off with seeded `owner` / `200` on `handoverStandbyContextWAC.json` → hash unchanged. Update `TestReceiveWACStandbyContext` flag-on so `handoverStandbyContextWAC.json` still persists the summary, `LenQueuedMsgs()==0`, and Redis is `not_owner` for `whatsapp:5678` at `1454119029`. Writer-error subtest stays HTTP 400. On the writer-error log, assert `event=wac_standby_context_write_failed`, `channel_uuid`, `external_id` `wamid.SHOPPER`, no `context_text`, and `assertNoWACOwnershipPII`. Add `wac_standby_context_write_failed` and `wac_standby_skipped` to that helper's switch (no `contact_urn`, no `sender_phone_number`, no `whatsapp:`). The logrus `request` field on the writer-error request must contain `[standby body omitted]` and not `Test standby message`.

### Implementation for User Story 3

- [ ] T008 [US3] In `handlers/facebookapp/wa_standby.go` `processWACStandby`, before each append from T006, apply ownership and dedup. Constants: `wacStandbySeenKeyPrefix = "wac:standby:seen"`, `wacStandbySeenTTL = 24 * time.Hour`. For each shopper message, echo, and status that has a URN: call `applyWACThreadOwner` with `wacThreadOwnerStateNone`, `wacThreadOwnerModeSet`, source `standby`, and the Unix seconds, only when the item timestamp parsed or was empty (empty uses `entry.Time`, including `0`). A non-empty unparseable timestamp does not call Lua and logs INFO `event=wac_standby_skipped`, `reason=bad_timestamp`, `channel_uuid`, and `external_id` when present. That log does not drop the item: a rendered message or echo still appends with `OccurredOn` from `entry.Time`. Borrow `RedisPool().Get()` and `Close` before `persistRenderedWAHandover`. Redis error: return it; do not `SET`. Statuses stop there: no render, no `SET`, no `WriteMsgStatus`. When any status had a URN, the HTTP 200 info string includes `standby status recorded`. A status-only webhook must not return `standby without conversation context`. A status with no URN does not count and does not add that string. For a rendered message or echo with a non-empty id: `SET wac:standby:seen:{channelUUID}:{messageID} 1 NX EX 86400`. `redigo` `"OK"` claims the key; `redis.ErrNil` skips the append. Any other `SET` error returns from the handler. After a claimed key, `persistRenderedWAHandover` nil keeps the key (including the existing spool-nil path). Non-nil: `DEL` the key, `logrus.WithError` ERROR `event=wac_standby_context_write_failed` with `channel_uuid` and `external_id` only, then return the error. Do not log the line, the template parameters, or the URN. A `stale` Lua result still appends. Flag off must not reach this function. Do not edit `wa_thread_owner.go` or `backends/rapidpro/wa_conversation_handover.go`.

**Checkpoint**: `go test ./handlers/facebookapp/ -count=1 -run 'TestRenderWACStandby|TestReceiveWACStandbyTranscript|TestReceiveWACStandbyContext|TestReceiveWACHandover|TestReceiveWACMessagesContext'` passes. Duplicate ids do not append. A failed write can be retried. `control_passed` tests stay green.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Release note and the quickstart suite

- [ ] T009 [P] Add bullets to `WENI-CHANGELOG.md`: standby transcript and `not_owner` behind the existing `COURIER_ENABLE_WAC_STANDBY_CONTEXT`, and the standby HTTP body omitted from the channel log even when that flag is off. Read every version heading in that file. If any heading is newer than `1.79.0`, add the bullets under that existing heading and do not create another version section. Create section `1.79.1` above `1.79.0` only when `1.79.0` is the newest heading. Do not edit upstream `CHANGELOG.md`.
- [ ] T010 [P] Run [quickstart.md](./quickstart.md) from repo root: `go test ./handlers/facebookapp/ -count=1 -run 'TestRenderWACStandby'` and `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACStandbyTranscript|TestReceiveWACStandbyContext|TestReceiveWACMessagesContext|TestReceiveWACHandover'`. Fix rename drift so those tests exist and pass. Confirm `backends/rapidpro/schema.sql` was not edited.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No tasks
- **Foundational (Phase 2)**: No tasks
- **US1 (Phase 3)**: Starts immediately — MVP of the line rules
- **US2 (Phase 4)**: Webhook wiring calls the US1 renderer. Fixtures (T003) can be written while US1 is in review
- **US3 (Phase 5)**: After US2. It extends `processWACStandby`; it does not replace the renderer
- **Polish (Phase 6)**: After US1, US2, and US3

### User Story Dependencies

- **US1 (P1)**: No other story. `handlers/facebookapp/wa_standby.go` render functions and `wa_standby_test.go` only
- **US2 (P2)**: T006 calls T002's renderers. T003 has no dependency on US1. T004 after T003. T005 (`server.go`) has no dependency on US1 and can run beside T001. T006 after T004 and T005
- **US3 (P3)**: After T006. T007 after the US2 tests exist. T008 after T007

### Within Each User Story

- Tests fail before the implementation task
- US1: stub plus failing assertions, then the real renderer
- US2: fixtures, then flow tests, then the log helper, then the webhook branch
- US3: failing Redis and retry cases, then the claim / `DEL` / Lua calls inside the branch US2 already added

### Parallel Opportunities

- T003 (fixtures) and T001 (renderer test) in parallel
- T005 (`server.go`) and T001/T002 in parallel
- T009 (`WENI-CHANGELOG.md`) and T010 (test run) in parallel after T008
- Do not parallel T001 with T002 (same functions, test first)
- Do not parallel T006 with T008 (both edit `processWACStandby` in `wa_standby.go`)
- Do not parallel T006 with any other edit to `processCloudWhatsAppPayload` in `facebookapp.go`

### Parallel Example: User Story 1 and User Story 2 setup

```text
T001: handlers/facebookapp/wa_standby_test.go and render stubs in wa_standby.go
T003: handlers/facebookapp/testdata/wac/standbyShopperTextWAC.json and the other standby fixtures
T005: server.go MarkChannelLogBodyOmitted
```

Then T002, then T004 → T006, then T007 → T008.

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. T001 failing renderer tests
2. T002 line rules
3. **STOP**: `TestRenderWACStandby` green. Nothing is wired to the webhook yet. Safe to review the strings before any Redis or Postgres write.

### Incremental Delivery

1. US1 → exact transcript lines (MVP of the format)
2. US2 → flag-on webhook appends those lines, flag-off stays quiet, channel log drops the body. Deployable: context is recorded, ownership is still the PR 335 standby rule (no `not_owner` from standby)
3. US3 → `not_owner`, dedup, status ACK, retry after a failed write
4. Polish: changelog + quickstart commands

### Parallel Team Strategy

1. One person: T001–T002 (`wa_standby.go` renderer)
2. Another: T005 (`server.go`) and T003 (fixtures) while US1 is in review
3. T004 and T006 stay with one person (`facebookapp.go` calls the renderer)
4. T007–T008 stay on `wa_standby.go` after T006
5. T009 and T010 together at the end

---

## Notes

- [P] = different files, no incomplete dependency
- Tests first. Renderer assertions, the shopper-text flow, the channel-log placeholder, the stale timestamp, and the writer-error retry must fail before their implementation tasks
- Constraints implementers must not invent, from `data-model.md`: transcript `context_type` is `history`; `context_payload` is NULL on these lines; seen key is `wac:standby:seen:{channel_uuid}:{message_id}`, value `1`, TTL 86400 seconds, `SET NX`, `DEL` only when `WriteWAConversationHandover` returns a non-nil error; ownership key stays `wac:thread_owner:{channel_uuid}:{urn.Identity()}`; `incoming <= stored` does not change `state` or `ts`; flag off does not write that hash; statuses do not create a seen key
- Do not log `context_text`, template parameters, contact name, phone numbers, or URN paths on `wac_standby_context_write_failed` or `wac_standby_skipped`
- Do not call `WriteMsg` from standby. Mailroom and Live Desk are not in this repository
- Commit after each task or story checkpoint (Conventional Commits, Principle VII)
- Changelog (T009) comes after the behaviour is in
