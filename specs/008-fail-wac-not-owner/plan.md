# Implementation Plan: Fail WAC Not-Owner Sends

**Branch**: `008-fail-wac-not-owner` | **Date**: 2026-11-13 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/008-fail-wac-not-owner/spec.md`

## Summary

With `EnableWACSendOwnershipBlock` on, a WAC **service** send whose Redis state is `not_owner` skips Graph, becomes `MsgFailed` with `failed_reason=H`, and is not retried. MARKETING / UTILITY / AUTHENTICATION templates and DirectSend still call Graph. Graph `error.code` 131070, on WAC only, is the same terminal failure and writes Redis `not_owner`. Flag off leaves today’s send and `MsgErrored` on 131070. Execution order and artifacts: [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-send-not-owner.md](./contracts/wac-send-not-owner.md), [quickstart.md](./quickstart.md).

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`, toolchain `go1.24.x`); module path `github.com/nyaruka/courier`

**Primary Dependencies**: existing `blockWACSendIfNotOwner` / `applyWACThreadOwner` / `getTemplate`, `utils.MakeHTTPRequest`, `sqlx` NamedQuery status updates, `logrus`

**Storage**: Redis hash `wac:thread_owner:*` (existing). Postgres `msgs_msg.failed_reason` written by Courier status UPDATEs; production column owned by RapidPro. Test `schema.sql` adds the column.

**Testing**: `go test ./handlers/facebookapp/ -count=1 -run 'TestSendWACThreadOwner'` and `go test ./backends/rapidpro/ -count=1 -run 'TestMsgStatus'` against MockBackend/local Redis and local Postgres. Graph stubbed with `httptest`.

**Target Platform**: Linux Courier service (stateless processes; ownership in Redis; status in Postgres)

**Project Type**: backend messaging gateway (WAC send slice + RapidPro status writer)

**Performance Goals**: outbound only. Flag on: one extra `HGET` per WAC send (already in 005) plus one Lua `EVAL` on the 131070 error path. Sustain the declared service peak of **242.9 req/s** inbound as capacity context; this change does not add inbound work.

**Constraints**: no new `COURIER_*` flag; no production migration in this repo; 131070 handling must not change `WCD`; `requestWAC` stays a package function (no Redis); `MsgStatus` interface gains optional `FailedReason`; RapidPro must allow `H` before flag-on traffic writes it; Principle XVI — do not change inbound ownership or other Meta codes

**Scale/Scope**: facebookapp send path, `status.go` + mock + RapidPro UPDATEs, test schema, changelog. Flows UI is another repository.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Notes |
|-----------|------|-------|
| I. Reviewed Changes on a Protected Main Branch | PASS | Lands via PR |
| II. Secrets and Supply Chain | PASS | No new secret or module |
| III. Structured, Privacy-Safe Observability | PASS | `wac_send_blocked` / `wac_thread_owner_*` fields already enumerated in the spec; no URN |
| IV. Versioned Contracts | PASS | Minor, default-off semantic change; optional `FailedReason`. See [contracts/wac-send-not-owner.md](./contracts/wac-send-not-owner.md) |
| V. Specification Traceability | PASS | Spec opens with Origin Without Product Spec |
| VI. No Silent Divergence | PASS | No Product Spec; Divergence Gate omitted |
| VII. Conventional, Atomic Commits | PASS | Implement phase |
| VIII. Release Changelog | PASS | `WENI-CHANGELOG.md` in the delivery sequence |
| IX. Never Trust the Client | PASS | 131070 only from Graph after Courier’s Bearer POST; integer compare |
| X. Fail Gracefully and Predictably | PASS | Redis read fail-open; 131070 Redis write fail still `F`; shared HTTP client |
| XI. Bounded Retry Over REST | PASS | 131070 is not retried; other Graph errors stay `E` |
| XII. Stateless, Peak-Sized Services | PASS (justified exception) | Peak declared. Per-route outbound series unavailable; inbound service-wide peak reused as in 005 |
| XIII. Diagnosable Errors | PASS (justified exception) | `channel_uuid` + `msg_uuid`; no contact UUID on send Redis errors (same as 005) |
| XIV. Tests Exercise Flows | PASS | Send → Graph-or-block and status SQL. See [quickstart.md](./quickstart.md) |
| XV. Explicit Over Clever | PASS | Named constants for 131070, `H`, allowed categories |
| XVI. Contained Changes | PASS | WAC send + status SQL + tests + changelog. 131070 applied in the WAC caller, not inside shared `requestWAC` |

**Post-Phase 1 re-check**: PASS. Contracts document `FailedReason` and `H`. `requestWAC` only learns to return the parsed body on HTTP error; Redis/status mutation stays on the handler after a WAC+flag check. Justified exceptions unchanged (peak series, no contact UUID). RapidPro column is a deploy dependency, not a Courier migration.

## Project Structure

### Documentation (this feature)

```text
specs/008-fail-wac-not-owner/
├── plan.md
├── spec.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── wac-send-not-owner.md
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks) — not created here
```

### Source Code (repository root)

```text
status.go                                 # FailedReason / SetFailedReason on MsgStatus
test.go                                   # mockMsgStatus
backends/rapidpro/
├── status.go                             # UPDATE msgs_msg SET failed_reason
├── schema.sql                            # test column
└── backend_test.go                       # persist / leave-empty
handlers/facebookapp/
├── facebookapp.go                        # getTemplate before gate; parse error on requestWAC; apply 131070 after return, WAC only
├── wa_thread_owner.go                    # template/DirectSend skip; SetFailedReason
├── facebookapp_test.go                   # TestSendWACThreadOwner extensions
└── wa_thread_owner_test.go               # PII allowlist if new events
WENI-CHANGELOG.md
```

**Structure Decision**: single Go module. Handler package owns send behaviour; RapidPro backend owns the SQL column write; courier root owns the `MsgStatus` interface.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| XII peak uses inbound service-wide series | Istio has no outbound-send path label | Inventing an outbound number without a series would be fiction |
| XIII no contact UUID on send Redis errors | Handler send path may not have loaded the contact | Attaching URN would violate Principle III; same exception as spec 005 |
