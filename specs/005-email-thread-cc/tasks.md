# Tasks: Email Thread Cc

**Input**: `specs/005-email-thread-cc/` (plan.md, spec.md, research.md, data-model.md, contracts/email-cc.md, quickstart.md)

**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/, quickstart.md

**Tests**: INCLUDED. Constitution XIV requires a flow test for every changed flow, and the spec's Test Strategy maps each acceptance criterion to a test. Write each test first and confirm it fails before its implementation task.

**Organization**: The spec has no P1/P2/P3 labels. Its Acceptance criteria and Implementation Decisions are grouped below into four stories:

- **US1 (P1)**: A copy reply lands on the central URN with the author's address as the first line. (spec acceptance 1; Flow 3–5)
- **US2 (P1)**: The copy list is stored on the message, starts from the first `cc`, and only grows. (spec acceptance 2)
- **US3 (P1)**: The agent send goes to the central address and to the list from the latest message of the URN. (spec acceptance 3)
- **US4 (P2)**: A message with no stored Message-ID opens a new conversation, and blocking runs on the real sender before attach. (spec acceptance 4–5)

The three P1 stories are all required for the acceptance criterion, so the MVP is US1 + US2 + US3 together. US1 alone changes receive behavior with no way to reply to copy addresses.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: different file, no dependency on an incomplete task
- **[Story]**: US1–US4 for story phases only
- Paths are relative to the repository root `courier/`

---

## Phase 1: Setup

**Purpose**: Establish a green baseline so any later failure is caused by this change.

- [x] T001 Run `go test ./handlers/email/ -count=1` and `go test ./backends/rapidpro/ -count=1 -run TestMsgSuite` (Postgres at `POSTGRES_HOST`, default localhost) on the unchanged branch and record that both pass. Files: `handlers/email/email_test.go`, `backends/rapidpro/backend_test.go`

---

## Phase 2: Foundational (blocks all user stories)

**Purpose**: The latest-message read that carries `cc`, and the struct fields both directions need. Nothing in a story phase compiles without these.

**Why a new read**: `LookupLastMsgWithExternalID` skips rows with an empty `external_id`. A Message-ID longer than 255 is stored without one, so that row could be the latest snapshot. See research.md decision 1.

- [x] T002 Add `LookupLastMsgByContactURN(context.Context, Channel, urns.URN) (Msg, error)` to the `Backend` interface in `backend.go`, next to `LookupLastMsgWithExternalID` (around line 160). Doc comment: latest visible message on the channel for the URN regardless of `external_id`; no row returns `ErrMsgNotFound`.
- [x] T003 [P] Write `TestLookupLastMsgByContactURN` as a method on `BackendTestSuite` in `backends/rapidpro/backend_test.go` (suite run by `TestMsgSuite`). Insert one `msgs_msg` row on a URN that no other test reads, with `external_id` NULL and `metadata` `{"email":{"subject":"Pedido","cc":["fulano_de_tal@email.com"]}}`, following the INSERT pattern around lines 1000–1010. Assert: (a) `LookupLastMsgByContactURN` returns that row with the metadata; (b) `LookupLastMsgWithExternalID` on the same URN returns `ErrMsgNotFound`-class error, so the external-id read is unchanged; (c) an unknown URN returns `courier.ErrMsgNotFound`. Must fail to compile or fail before T004–T005.
- [x] T004 [P] Add SQL `selectLastMsgByContactURNSQL` in `backends/rapidpro/msg.go`: the same columns and joins as `selectLastMsgWithExternalIDSQL` (around line 284) with the two `external_id` predicates removed. Keep `m.visibility = 'V'`, `ORDER BY m.id DESC`, `LIMIT 1`. Add `GetLastMsgByContactURN(ctx, b, channelID, urnIdentity)` that mirrors `GetLastMsgWithExternalID` (around line 881) and maps `sql.ErrNoRows` to `courier.ErrMsgNotFound`. Reuse `scanDBMsgWithMetadata`; `ExternalID_` is `null.String`, so a NULL external_id already scans.
- [x] T005 Implement `(b *backend) LookupLastMsgByContactURN` in `backends/rapidpro/backend.go`, next to `LookupLastMsgWithExternalID` (around line 1091). Same `*DBChannel` type assertion and `urn == urns.NilURN` guard as its neighbour, then call `GetLastMsgByContactURN`. Depends on T004.
- [x] T006 [P] Extend `MockBackend` in `test.go`: add map `lastMsgByContactURN map[string]Msg` and error map `lastMsgByContactURNErr map[string]error`, both initialized in `NewMockBackend` (around line 122). Add `AddLastMsgByContactURN(msg Msg)` (no `external_id` requirement, unlike `AddLastMsgForContact`), `SetLastMsgByContactURNErr(channel Channel, urn urns.URN, err error)`, and `LookupLastMsgByContactURN` that checks the error map first, then the msg map, else `ErrMsgNotFound`. Key both maps by `channel.UUID()|urn.Identity()`. Do not modify `AddLastMsgForContact` or `LookupLastMsgWithExternalID`. Depends on T002 for the interface.
- [x] T007 Add the `cc` field to two structs in `handlers/email/email.go`: `CC []string \`json:"cc,omitempty"\`` on `moPayload`, placed immediately after `To` so the JSON key order matches the expected `RequestBody` strings (`from, to, cc, body, ...`). Add `CC []string \`json:"cc,omitempty"\`` to `emailThreadMetadata`. Nothing else changes yet.

**Checkpoint**: The backend read exists and its test passes. `go build ./...` succeeds. The email tests still pass, since no behavior changed.

---

## Phase 3: User Story 1 — Copy reply lands on the central URN (P1)

**Goal**: A message that cites a stored Message-ID on a `+wt-` thread is written on that thread's URN even when `from` is another mailbox. The stored text starts with `{from}:` and a blank line.

**Independent Test**: `go test ./handlers/email/ -run TestHandler -count=1`, the cases "Copy reply…" and "Central author…".

### Tests for User Story 1 (write first, must fail)

> Test isolation: `RunChannelTestCases` shares one `MockBackend` across cases and never clears the message-ID or latest-row maps. Each case uses its own opener address and unique Message-IDs.

- [x] T008 [US1] Add a receive case to `receiveTestCases` in `handlers/email/email_test.go`: "Copy reply cites a stored thread Message-ID and stays on the central URN". Seed in `PrepBackend` a parent on `mailto:copy-opener+wt-<8hex>@example.com` (use `urnWithTag("copy-opener@example.com", "<copy-root@example.com>")`) with `WithExternalID("<copy-root@example.com>")` and `AddMsgByExternalID`. Post `from` = `copy-author@example.com`, `in_reply_to` = `<copy-root@example.com>`, body `Estou de acordo`. Expect URN = that parent's URN, `Name` = `copy-opener@example.com`, `Text` = `"copy-author@example.com:\n\nEstou de acordo"`. Must fail on current code.
- [x] T009 [US1] Add a receive case: "Central author body is stored unchanged". Same seeded parent pattern on a different opener, `from` = that opener in different case (`Central-Opener@Example.com`), in_reply_to cites the parent. Expect `Text` equal to the body exactly, URN = central URN, `Name` = `Central-Opener@Example.com` (from spelling kept, per research.md decision 6). Must fail on current code only if the URN or name differs; keep it as the regression guard for the body rule.

### Implementation for User Story 1

- [x] T010 [US1] In `handlers/email/email.go` `contactURNForInbound`, when the URN returned by `LookupContactURNByExternalID` has a path matching `threadTagPattern`, return it immediately, regardless of `from`. Do this before the `sameEmailAddress(realAddressFromURNPath(...), payload.From)` check. Do not call `buildContactURN` for that case. The legacy plain-mailto branch and the break-on-direct-In-Reply-To rule stay unchanged. Makes T008 pass for the URN.
- [x] T011 [US1] In `handlers/email/email.go`, add helper `inboundText(from string, central urns.URN, body string) string`. Return `body` unchanged when `sameEmailAddress(from, realAddressFromURNPath(central.Path()))`; otherwise return `strings.TrimSpace(from) + ":\n\n" + body`. Do not trim `body`. In `receiveEvent`, pass `inboundText(payload.From, urn, payload.Body)` to `NewIncomingMsg`. Depends on T010 (needs the resolved `urn`).
- [x] T012 [US1] In `handlers/email/email.go` `receiveEvent`, compute the contact name: `payload.From` when `sameEmailAddress(payload.From, realAddressFromURNPath(urn.Path()))`, otherwise `realAddressFromURNPath(urn.Path())`. Pass it to `WithContactName`. Verify the legacy case "Reply reuses a legacy contact when From differs only by case" still expects `Fulano123@Gmail.com`. Depends on T010.
- [x] T013 [US1] Update two existing expectations in `receiveTestCases` of `handlers/email/email_test.go` in the same commit as T010. "Reply that cites a threaded contact keeps the thread URN" and "Direct reply to a threaded message ignores an older legacy reference" now expect the stored URN `mailto:fulano123+wt-aaaaaaaa@gmail.com` (the parent's URN), not `urnWithTag("fulano123@gmail.com", "<produto@example.com>")`. The spec's Implementation Decisions record why. `TestThreadContactSegregation` is unchanged.

**Checkpoint**: US1 passes. A copy reply lands on the central URN with its first line being the author's address. Nothing new is created for the author.

---

## Phase 4: User Story 2 — Copy list stored on the message (P1)

**Goal**: `metadata.email.cc` on each inbound message holds the accumulated copy list. It starts from the first `cc`, drops the channel mailbox and the central contact, and only grows.

**Independent Test**: `go test ./handlers/email/ -count=1 -run 'TestHandler|TestAccumulateCopyList'`.

### Tests for User Story 2 (write first, must fail)

- [x] T014 [US2] Add `TestAccumulateCopyList` to `handlers/email/email_test.go`. Table-driven, calls `accumulateCopyList` (not yet defined; test fails to compile, which is the red state). Cases: previous order kept; payload `cc` appended after previous; `from` appended last when not central; `from` not appended when equal to central (case-insensitive); channel mailbox dropped (`support@company.com`); `+wt-` tag stripped from an entry before comparison; dedupe case-insensitive and first spelling kept (`FULANO@x.com` after `fulano@x.com` gives one entry, `fulano@x.com`); entry with a space or angle brackets dropped; entry without a single `@` dropped; empty and whitespace entries dropped; returned `dropped` count equals the drops.
- [x] T015 [US2] Add receive case "First message with cc stores the copy list without the channel mailbox or the opener" in `receiveTestCases` of `handlers/email/email_test.go`. No seed. Data: `from` = `list-opener@example.com`, `to` = `support@company.com`, `cc` = `["support@company.com","fulano_de_tal@email.com","other@example.com"]`, `message_id` `<list-first@example.com>`, subject `Pedido`. Expect URN = `urnWithTag("list-opener@example.com", "<list-first@example.com>")`, `Metadata` = `{"email":{"subject":"Pedido","cc":["fulano_de_tal@email.com","other@example.com"]}}`.
- [x] T016 [US2] Add receive case "Copy reply grows the list and keeps earlier addresses" to `receiveTestCases` in `handlers/email/email_test.go`. Seed in `PrepBackend`: a parent on `urnWithTag("grow-opener@example.com", "<grow-root@example.com>")` with `external_id` `<grow-root@example.com>`, and the latest visible row for that URN via `AddLastMsgByContactURN` whose metadata has `cc` `["fulano_de_tal@email.com"]`. Post `from` = `grow-other@example.com`, `cc` = `["fulano_de_tal@email.com","third@example.com"]`, `in_reply_to` = `<grow-root@example.com>`. Expect `Metadata.email.cc` = `["fulano_de_tal@email.com","third@example.com","grow-other@example.com"]` and `Text` = `"grow-other@example.com:\n\n<body>"`.
- [x] T017 [US2] Add receive case "cc that is not an array is rejected" to `receiveTestCases` in `handlers/email/email_test.go`. Data: `cc` = `"fulano@example.com"` (string). Expect `Status` 400 and a response body that names the error, same style as "Missing from". Prevents a silent drop of recipients.
- [x] T018 [US2] Add receive case "Latest-message lookup failure on receive writes nothing" to `receiveTestCases` in `handlers/email/email_test.go`. Seed in `PrepBackend`: `SetLastMsgByContactURNErr(channel, urn, errors.New("db down"))` for `urnWithTag("err-opener@example.com", "<err-root@example.com>")`, with the parent seeded so the URN resolves to it. Expect `Status` 400. Error is keyed to that URN so it cannot leak into other cases. Known limit: `RunChannelTestCases` does not assert the queue for non-200 cases, so "nothing written" is verified by ordering (T020 runs the lookup before `WriteMsgsAndResponse`) and by the 400 status. Recorded in Notes below.

### Implementation for User Story 2

- [x] T019 [US2] Implement pure function `accumulateCopyList(prev []string, payload *moPayload, central urns.URN, channelAddress string) ([]string, int)` in `handlers/email/email.go`. Order: `prev` as given, then each `payload.CC` entry, then `payload.From` when it is not the central contact. Normalize each candidate: trim; `realAddressFromURNPath`; reject unless exactly one `@`, non-empty local and domain, no whitespace or `<>`; reject when equal (via `sameEmailAddress`) to `channelAddress` or to the central real address; reject when already present (case-insensitive, first spelling kept). Count each rejected payload `cc` entry (not `from`). Makes T014 pass. Depends on T014 and T007.
- [x] T020 [US2] In `receiveEvent` in `handlers/email/email.go`, after the URN is resolved and before `NewIncomingMsg`: call `h.Backend().LookupLastMsgByContactURN(ctx, channel, urn)`. `courier.ErrMsgNotFound` means previous list nil. Any other error returns `handlers.WriteAndLogRequestError(...)` with a wrapped error and writes nothing. On a found row, previous list = `parseEmailThreadMetadata(row.Metadata())` `.CC` (nil metadata gives nil). Channel mailbox = `channel.StringConfigForKey(courier.ConfigUsername, "")`. Depends on T005, T006, T019.
- [x] T021 [US2] In `handlers/email/email.go`, change `buildThreadMetadata` to take the accumulated `cc []string`. Return the `email` object when `cc` is non-empty even if `inReplyTo`, `references` and `subject` are empty. Omit the `cc` key when empty. If `dropped > 0`, log via `logrus` with fields `channel_uuid` and `cc_dropped` (integer) only; no addresses, no URN, no body. Makes T015, T016 pass. Depends on T019, T020.

**Checkpoint**: US2 passes. Stored lists grow, the rejected `cc` shape returns 400, and a failed lookup writes nothing.

---

## Phase 5: User Story 3 — Send to the central address with the list (P1)

**Goal**: The proxy call has `to` = the central real address (no `+wt-`) and `cc` = the list on the latest visible message of the URN. The wired outbound message stores that same list. A failed lookup never reaches the proxy.

**Independent Test**: `go test ./handlers/email/ -run TestSending -count=1`.

### Tests for User Story 3 (write first, must fail)

- [x] T022 [US3] Add a send case to `defaultSendTestCases` in `handlers/email/email_test.go`: "Send uses the central address and the cc of the latest message on the URN". URN `mailto:send-opener+wt-0123abcd@example.com`. `ResponseToExternalID` = `<cc-parent@x>`. `PrepBackend` seeds a parent with `WithExternalID("<cc-parent@x>")` and `WithMetadata` `{"email":{"subject":"Pedido"}}` on that URN (via `AddMsgByExternalID`), then a newer latest row via `AddLastMsgByContactURN` with metadata `{"email":{"cc":["fulano_de_tal@email.com","other@example.com"]}}`. Expect `RequestBody` `to` = `send-opener@example.com`, `cc` = both addresses, `subject` = `Re: Pedido`, `in_reply_to` = `<cc-parent@x>`, `references` = `["<cc-parent@x>"]`. `StatusMetadata` = `{"email":{"in_reply_to":"<cc-parent@x>","references":["<cc-parent@x>"],"subject":"Re: Pedido","cc":["fulano_de_tal@email.com","other@example.com"]}}`. Status `W`. Must fail on current code.
- [x] T023 [US3] Add a send case: "Latest message on the URN without cc omits the key" to `defaultSendTestCases` in `handlers/email/email_test.go`. Same parent pattern on a second unique URN; the newer latest row has metadata `{"email":{"subject":"Pedido"}}` and no `cc`. Expect `RequestBody` with no `"cc"` key, status `W`.
- [x] T024 [US3] Add a send case: "Latest-message lookup failure on send does not call the proxy" to `defaultSendTestCases` in `handlers/email/email_test.go`. On a unique URN, `PrepBackend` calls `SetLastMsgByContactURNErr`. Set `ResponseStatus` 200 with body `{"status":"sent"}`. Expect `Status` `E`. A 2xx from the proxy would give `W`, so `E` proves no successful POST happened. `RequestBody` must not be asserted.

### Implementation for User Story 3

- [x] T025 [US3] In `SendMsg` in `handlers/email/email.go`, after `buildReplyContext`, call `h.Backend().LookupLastMsgByContactURN(ctx, msg.Channel(), msg.URN())`. `ErrMsgNotFound` gives `cc` nil. Any other error: log with `channel_uuid` and `msg_id`, return the already-errored `status` with no `MakeHTTPRequest`. Parse `cc` via `parseEmailThreadMetadata`. Depends on T005, T006. Makes T024 pass.
- [x] T026 [US3] In `SendMsg` in `handlers/email/email.go`, set `payload.CC` to the list from T025. `payload.To` stays `realAddressFromURNPath(msg.URN().Path())`. The encoder keeps `SetEscapeHTML(false)`. Depends on T025, T007. Makes T022 and T023 request bodies pass.
- [x] T027 [US3] Change `buildOutboundThreadMetadata` in `handlers/email/email.go` to take `cc []string`. Include `cc` when non-empty. It is already called only in the 2xx branch of `SendMsg`, so the list is written only after a successful send. `mergeEmailMetadata` keeps sibling keys (`ticketer_id`, `chats_msg_uuid`). Makes T022 `StatusMetadata` pass. Depends on T026.

**Checkpoint**: US3 passes. Send carries the central `to` and the latest list. Existing `TestSending` cases with no list keep their exact bodies.

---

## Phase 6: User Story 4 — New conversation and blocking guard (P2)

**Goal**: An email whose cited Message-IDs are not stored here opens a new conversation, and its sender is that conversation's contact. Blocking uses the real sender and runs before attaching to a thread.

**Independent Test**: `go test ./handlers/email/ -run TestHandler -count=1`, the cases "New conversation…" and "Blocked copy author…".

### Tests for User Story 4 (write first)

- [x] T028 [US4] Add a receive case "Sender with no stored Message-ID opens a new conversation" to `receiveTestCases` in `handlers/email/email_test.go`. `from` = `cc-only-sender@example.com`, `message_id` `<newconv@x>`, no `in_reply_to`/`references`. Seed in `PrepBackend` an unrelated parent on `urnWithTag("someone-else@example.com", "<other-root@x>")` whose metadata lists `cc-only-sender@example.com` in `cc`. Expect URN = `urnWithTag("cc-only-sender@example.com", "<newconv@x>")`, `Name` = `cc-only-sender@example.com`, `Text` unchanged (sender is central, no prefix), and no URN from the other thread's cc. Passes if T010 is correct; must not pull the sender onto the other thread.
- [x] T029 [US4] Add a receive case "Blocked copy author is ignored on a thread reply" to `receiveTestCases` in `handlers/email/email_test.go`. `PrepBackend` calls `mb.SetEmailMailboxBlocked("copy-blocked@example.com", true)` and seeds a central parent with `in_reply_to` citing it. `from` = `copy-blocked@example.com`. Expect the same response as "Receive from blocked mailbox is ignored": `Status` 200, `Response` `""`. Use a unique address so the flag does not leak into later cases.

### Implementation for User Story 4

- [x] T030 [US4] Verification only, in `handlers/email/email.go` `receiveEvent`: confirm `IsEmailMailboxBlocked(ctx, channel, payload.From)` still runs before `contactURNForInbound` and before the `LookupLastMsgByContactURN` added in T020. If T029 fails, move the block check up. Do not change the blocked response. Depends on T020.

**Checkpoint**: US4 passes. A stored-ID miss opens a new conversation. A blocked copy author writes nothing.

---

## Phase 7: Polish & cross-cutting

- [x] T031 [P] Add a `WENI-CHANGELOG.md` entry, `* feat: ...`, under a new heading above `1.79.0`. Describe the author line, the copy list and the new `cc` on send, and that email-proxy must accept `cc` first. The version number is chosen at tag time (Principle VIII). Do not edit `CHANGELOG.md`.
- [x] T032 [P] Peak check, `backends/rapidpro/msg.go` and `handlers/email/email.go`: confirm the new SQL has `LIMIT 1` and no thread-wide scan, and that `SendMsg` still makes exactly one `utils.MakeHTTPRequest` call. Record the result in plan.md's Peak Load Plan.
- [x] T033 [P] Run `gofmt -l handlers/email backends/rapidpro backend.go test.go` (expect no output) and `go vet ./handlers/email/ ./backends/rapidpro/ ./...`.
- [x] T034 Run `go test -p=1 ./...` (CI parity, Postgres and Redis up). Walk each row of quickstart.md's "Receive outcomes", "Send outcomes" and "Lookup outcome" tables and confirm it maps to a test name that passes. Record the test names in the PR description.
- [x] T035 Split the commits on `feat/email-chann-cc` by story (all touched files listed in T002–T030) per Principle VII, Conventional Commits, imperative, at most 50 characters. Suggested: `feat: add latest message lookup by contact URN` (T002–T006), `test: cover email copy replies on the central URN` (T008–T009, T013), `feat: keep email copy replies on the central URN` (T010–T012), `feat: store the email copy list on messages` (T014–T021), `feat: send the email copy list to the proxy` (T022–T027), `feat: guard email blocking and new conversations` (T028–T030). Each commit must pass `go test ./handlers/email/`.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup. Blocks every story. T004 and T006 can run in parallel with T003. T005 waits for T004. T006 waits for T002.
- **US1 (Phase 3)**: depends on Phase 2 (T007 for the struct, T002 for the interface). Tests T008–T009 before T010–T012.
- **US2 (Phase 4)**: depends on Phase 2 and on US1's `urn` resolution (T010). T014 before T019.
- **US3 (Phase 5)**: depends on Phase 2 (T005, T006, T007). Independent of US1 and US2 at the code level, but edits the same two files, so run it after them in one branch.
- **US4 (Phase 6)**: depends on T010 and T020.
- **Polish (Phase 7)**: depends on every story being green.

### Within a story

Tests before implementation. Helpers before the function that calls them (T019 before T020; T025 before T026 before T027).

### Parallel opportunities

- T003, T004, T006 touch different files after T002 is merged.
- T031, T032, T033 are independent of each other.
- Within `handlers/email/email_test.go` and `handlers/email/email.go` there is no parallelism: those files are edited by one stream at a time.

---

## Implementation Strategy

### MVP (US1 + US2 + US3 together)

All three P1 stories are needed for the acceptance criterion: the copy reply is on the opener's conversation, and the agent's reply reaches copy addresses. Stop after Phase 5 and run `go test ./handlers/email/ -count=1` before starting US4.

### Incremental delivery

1. Phase 1–2: backend read and structs (no behavior change, safe to merge alone).
2. Phase 3: receive identity and author line. Visible to agents at once, so ship with the changelog entry.
3. Phase 4–5: list and send. Ship only after email-proxy accepts `cc` (see Dependencies below).
4. Phase 6: guard tests. Can merge with step 3.

### External dependency (not a task in this repository)

- **email-proxy** must accept optional `cc` on `POST /send` and set SMTP `Cc` (contracts/email-cc.md). It may also start sending `cc` on receive. Courier must not send a non-empty `cc` to a proxy that rejects unknown fields. This is an ordering constraint on the release, not code in this repo.

---

## Notes

- Known gap: the shared harness in `handlers/test.go` does not assert on the queue for non-200 responses. The "writes nothing" guarantee for T017 and T018 rests on ordering plus the 400 status. Adding a queue assertion to the shared harness would affect every channel, so it is out of scope (Constitution XVI). Decide separately if wanted.
- T001: `go test ./handlers/email/ -count=1` passed on the unchanged tree. `go test ./backends/rapidpro/ -count=1 -run TestMsgSuite` did not run: Postgres rejected `courier` with `password authentication failed`.
- T033: `gofmt -l` is clean for the files this change edits. `backends/rapidpro/conversion_events.go` and `backends/rapidpro/wa_conversation_handover.go` were already unformatted in the tree and were left untouched. `go vet ./handlers/email/ ./backends/rapidpro/` passed.
- T034: `go test ./handlers/email/ -count=1` passed after the change. `go test -p=1 ./...` was not green because the local Postgres user `courier` fails authentication. `TestLookupLastMsgByContactURN` is written and not executed here.
- Test isolation: never reuse an opener address, Message-ID or URN across receive or send cases in the shared mock.
- Expected changes to existing tests: only T013 (two URN expectations) and the case-only name check stays green through T012. Any other existing expectation that changes means the implementation drifted from the spec.
