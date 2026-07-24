# Tasks: PSTN Telephony Channel (Courier)

**Input**: Design documents from `/specs/001-pstn-telephony-channel/`  
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

## Format: `[ID] [P?] [Story] Description`

---

## Phase 1: Setup

- [x] T001 Create feature spec artifacts in `specs/001-pstn-telephony-channel/`
- [x] T002 Set active feature in `.specify/feature.json`

---

## Phase 2: Foundational

- [x] T003 Create `handlers/telephony/telephony.go` with `TPH` handler registration and address-based routing
- [x] T004 Implement `GetChannel` DID lookup via `GetChannelByAddress`
- [x] T005 Implement `buildContactURN` for `tel:` and withheld caller ID

---

## Phase 3: User Story 1 - Inbound voice turns (P1)

- [x] T006 [US1] Implement `receiveMessage` in `handlers/telephony/telephony.go`
- [x] T007 [US1] Add inbound handler tests in `handlers/telephony/telephony_test.go`

---

## Phase 4: User Story 2 - Outbound agent text (P1)

- [x] T008 [US2] Implement `SendMsg` posting to `{base_url}/send` in `handlers/telephony/telephony.go`
- [x] T009 [US2] Add outbound send tests in `handlers/telephony/telephony_test.go`

---

## Phase 5: Polish

- [x] T010 Document gateway contract in `specs/001-pstn-telephony-channel/contracts/gateway-api.md`
- [x] T011 Add quickstart in `specs/001-pstn-telephony-channel/quickstart.md`

---

## Dependencies

- US1 and US2 both depend on Phase 2 foundation
- Flows `TPH` channel type (separate repo) required for end-to-end telephony

## Validation

```bash
go test ./handlers/telephony/... -v
```

Requires Redis on `localhost:6379` (Courier test harness).
