# Implementation Plan: Accumulate WAC Context

**Branch**: `006-accumulate-wac-context` | **Date**: 2026-11-12 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/006-accumulate-wac-context/spec.md`

## Summary

The pending `wa_conversation_handover` row will append `context_text` on conflict instead of replacing it, and a `WAC` `field=messages` webhook that carries renderable `conversation_context` will write that buffer before `WriteMsg`. Standby and `control_passed` keep failing the webhook on persist error. No schema change, no new flag, no Mailroom change. Execution order and artifacts: [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-pending-context.md](./contracts/wac-pending-context.md), [quickstart.md](./quickstart.md).

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`, toolchain `go1.24.x`); module path `github.com/nyaruka/courier`

**Primary Dependencies**: `github.com/jmoiron/sqlx` (`NamedExecContext`), `github.com/lib/pq`, `github.com/sirupsen/logrus`, `github.com/nyaruka/null`, existing `handlers` test helpers (`TestHandlerRequest`, `CaptureLogs`, `NewTestServer`)

**Storage**: Postgres `wa_conversation_handover` (existing table; writer SQL only). No new Redis key. Existing spool path for this event is unchanged.

**Testing**: `go test ./backends/rapidpro/ -count=1 -run 'TestMsgSuite/TestWriteWAConversationHandover'` against local Postgres (`courier_test`); `go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACMessagesContext|TestReceiveWACStandbyContext'` against `MockBackend`

**Target Platform**: Linux Courier service (horizontal scale; buffer lives in Postgres, not in the process)

**Project Type**: backend messaging gateway (RapidPro writer + WAC handler slice)

**Performance Goals**: sustain the declared inbound peak of **242.9 req/s** (1-minute rate, 2026-10-28 14:06 UTC) with at most one `wa_conversation_handover` upsert per context-bearing webhook (one per queued message when a `messages` change renders)

**Constraints**: no schema migration; no new `COURIER_*` flag; concatenation inside the existing `ON CONFLICT DO UPDATE` (not a prior `SELECT`); 32000-character tail with `[truncated]` outside the cap; messages-path writer error logs `wac_pending_context_write_failed` and still calls `WriteMsg`; no `context_text` or URN path on that log; `WAC` only (`WCD` shares `processCloudWhatsAppPayload`); `backendTimeout` stays 20s

**Scale/Scope**: `backends/rapidpro/wa_conversation_handover.go` + its test, `handlers/facebookapp/wa_handover.go`, the messages loop in `facebookapp.go`, one fixture, `MockBackend` test seam, changelog. No Mailroom, no Redis, no FBA/IG/`WCD` behaviour.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Notes |
|-----------|------|-------|
| I. Reviewed Changes on a Protected Main Branch | PASS | Lands via PR to `weni-ai/courier`; not planned as a direct `main` push |
| II. Secrets and Supply Chain | PASS | No new secret, flag, or module |
| III. Structured, Privacy-Safe Observability | PASS | New ERROR log is `event`, `channel_uuid`, `external_id` only. Pre-existing `contact_urn` on the DB-error log is not extended (Principle XVI) |
| IV. Versioned Contracts | PASS | Patch on the pending row and on `POST /c/wac/receive`. Same columns, one pending row. See [contracts/wac-pending-context.md](./contracts/wac-pending-context.md) |
| V. Specification Traceability | PASS | Spec opens with `## Origin Without Product Spec`; this plan pins that spec |
| VI. No Silent Divergence | PASS | No Product Spec; nothing to diverge from. Divergence Gate section omitted |
| VII. Conventional, Atomic Commits | PASS | Implement phase; not this command |
| VIII. Release Changelog | PASS | `WENI-CHANGELOG.md` `fix` entry in the delivery sequence. Upstream `CHANGELOG.md` is not edited |
| IX. Never Trust the Client | PASS | Context is taken only after the existing signature check, and only when `renderWAHandoverContextText` accepts it |
| X. Fail Gracefully and Predictably | PASS | Messages writer error → log + `WriteMsg`. Standby and `control_passed` writer error → non-200 |
| XI. Bounded Retry Over REST | PASS (justified exception) | No new REST hop. Messages context failure is not redeliverable. Append is not idempotent under Meta redelivery. Both are in spec Constitution Alignment |
| XII. Stateless, Peak-Sized Services | PASS | State stays in Postgres. Peak declared. Per-route series unavailable (Istio); service-wide peak reused. Spool path not extended |
| XIII. Diagnosable Errors | PASS (justified exception) | Sentry via the ERROR log: `channel_uuid` + `external_id`. No contact UUID before `WriteMsg`; URN forbidden |
| XIV. Tests Exercise Flows | PASS | Webhook → handover event → queued msg, and Postgres upsert of the pending buffer. See [quickstart.md](./quickstart.md) |
| XV. Explicit Over Clever | PASS | Named 32000 constant in the writer, interpolated into the SQL. Newline and empty-string rules are the UPDATE expression |
| XVI. Contained Changes | PASS | Writer, shared helper, messages hook, tests, changelog. No Redis, no flag, no standby message ingestion |

**Post-Phase 1 re-check**: PASS. Design artifacts add no column, no `Backend` method, no flag, no Product Spec divergence, and no extra public route. Justified exceptions unchanged: messages-path context is fail-open, redelivery may append a duplicate line, per-route peak is the service-wide series, and the new error has no contact UUID.

## Project Structure

### Documentation (this feature)

```text
specs/006-accumulate-wac-context/
├── plan.md              # This file
├── spec.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── wac-pending-context.md
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks) — not created here
```

### Source Code (repository root)

```text
backends/rapidpro/
├── wa_conversation_handover.go       # ON CONFLICT append, type, truncate, metadata
└── wa_conversation_handover_test.go  # concatenation, consumed insert, truncation, metadata
handlers/facebookapp/
├── wa_handover.go                    # shared persist helper; standby and control_passed call it
├── facebookapp.go                    # WAC field=messages hook before WriteMsg
├── facebookapp_test.go               # TestReceiveWACMessagesContext
└── testdata/wac/
    ├── messagesContextSummaryWAC.json  # new
    └── helloWAC.json                   # existing; zero handover events
test.go                                 # SetErrorOnWAHandover + call order
WENI-CHANGELOG.md                       # fix entry on the shipping release
```

**Structure Decision**: The append belongs in `backends/rapidpro/wa_conversation_handover.go` because every caller (`control_passed`, standby, and the new messages hook) must share one pending row. The messages hook stays in `processCloudWhatsAppPayload`, which already owns the `WriteMsg` loop. Render and contact-name lookup stay in `wa_handover.go` so `facebookapp.go` does not grow a second copy. Handler tests keep using `MockBackend`; only the rapidpro suite hits Postgres.

## Complexity Tracking

Justified exceptions from spec.md Constitution Alignment (gate still PASS):

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| XI: `field=messages` context write failure is logged, HTTP 200 | A non-200 makes Meta redeliver and `WriteMsg` duplicates the inbound | Failing the webhook so the context write is retried |
| XI: append is not idempotent under redelivery of the same context | AC-08 forbids a dedup column; AC-09 leaves standby-copy dedup to a later spec | Hashing the payload into a new column, or making the UPDATE a no-op when the text matches |
| XII: WAC-only req/s not declared | Istio has no path label | Inventing a per-route peak without a series |
| XIII: no contact UUID on the new Sentry event | Contact row is created in `WriteMsg`, which has not run yet | Attaching the URN (Principle III forbids it) |

## Inherited Context

- Product Spec: none
- Engineering Spec: `specs/006-accumulate-wac-context/spec.md`

## Delivery Sequence

1. **Writer SQL + rapidpro tests** — append, `history`-if-either, tail truncation, payload and metadata rules. Must land first: every caller, including spool replay, uses this statement. Isolate pending rows per test so `TestWriteWAConversationHandover` does not leak into the upsert case (see research). Proves AC-01, AC-02, AC-03, AC-08.
2. **Shared helper in `wa_handover.go`** — `persistWAHandoverContext` and the `control_passed` branch call it and still return the write error. Existing standby and handover tests stay green, including `LenQueuedMsgs()==0` on standby (AC-09). Must land before the messages hook so that hook does not copy render or contact-name logic (AC-04).
3. **`MockBackend` test seam** — `SetErrorOnWAHandover` and a call-order slice. Needed by the flow test; no production behaviour.
4. **`WAC` `field=messages` hook + flow tests** — render once per change; write immediately before each `WriteMsg`; log and continue on error. Covers AC-04, AC-05, AC-06, AC-07, and the `WCD` negative.
5. **Changelog** — `fix` bullets in `WENI-CHANGELOG.md` on the release that ships this change.

**Cross-repository coordination**: none. Mailroom already selects one pending row and sets `consumed_on`. It must not change. RapidPro application code is not in this repository.

## Peak Load Plan

- **Declared peak**: 242.9 inbound req/s (1-minute rate at 2026-10-28 14:06 UTC, window 2026-10-07 to 2026-11-06). Added cost at that peak, if every request carried context: 242.9 upserts/s on `wa_conversation_handover`. A `messages` change writes once per queued message, not a new fan-out.
- **How this plan sustains it**: one `INSERT ... ON CONFLICT DO UPDATE` per write, on the existing partial unique index; no extra Redis command; no in-process buffer; `backendTimeout` stays 20s; spool path unchanged. Courier stays stateless.
- **How it is verified**: unit and flow tests do not load-test Postgres. After deploy, watch Courier 5xx on `/c/wac/receive`, Postgres errors on `wa_conversation_handover`, and the count of `wac_pending_context_write_failed` (should stay near zero). No new SLO. Done means production satisfies the spec ACs, not a merged PR.

## Observability Plan

- **Structured logs added** (implement with `logrus.WithError` + `logrus.Fields`, never interpolate context text or URNs):
  - `wac_pending_context_write_failed` ERROR — `event`, `channel_uuid`, `external_id` (`msg.ID`). Fired only when the messages-path `WriteWAConversationHandover` returns an error.
- **Correlation identifier**: `channel_uuid` enters from the resolved channel; `external_id` enters from the inbound message id already on the payload. Neither is a URN.
- **Error reports**: Sentry via the existing `logrus_sentry` hook on that ERROR. Opaque ids only. No contact UUID, no `context_text`, no contact name.
- **Signals watched after rollout**: `wac_pending_context_write_failed` rate; Meta non-200 on standby / `messaging_handovers` (those paths still fail closed); confirm messages with context still return 200 and that the log line has no URN path. Success on the messages path adds no log line.

## Rollout and Rollback Plan

- **Rollout**: deploy Courier only. No flag. The next context-bearing webhook appends. Rows already pending are not rewritten until the next write. `EnableWACStandbyContext` stays the standby gate. Messages context persist is on for every `WAC` payload that renders, including while that flag is false.
- **Backward compatibility window**: the previous binary uses the same table and the same partial unique index. Mailroom’s select-one-pending-row contract is unchanged. A longer `context_text` still fits the existing Mailroom format string.
- **Rollback**: redeploy the previous Courier tag. The old `ON CONFLICT` replaces `context_text` on the next write. A row that already holds concatenated text remains a legal `summary` or `history` value.
- **Migration reversibility**: no migration. Nothing to reverse.

## Delivery artifacts vs spec ACs

| AC | Lands in sequence step | Proven by |
|----|------------------------|-----------|
| AC-01 | 1 | Rewritten upsert test: concatenation, `history` if either side is `history`, summary+summary stays `summary`, truncation tail |
| AC-02 | 1 | Consumed row kept; new pending row is insert-only |
| AC-03 | 1 | `TestWriteWAConversationHandoverUpsertsPending` expects `First summary\n[user] hi`; consumed case in the same file |
| AC-04 | 2 + 4 | `messagesContextSummaryWAC.json`: one handover event then one queued msg; URN and `OccurredOn` from the message |
| AC-05 | 4 | `helloWAC.json` and whitespace summary: zero handover events, one queued msg |
| AC-06 | 3 + 4 | `SetErrorOnWAHandover`: HTTP 200, msg queued, `wac_pending_context_write_failed` without context text or URN |
| AC-07 | 4 | Messages test leaves `EnableWACStandbyContext` false and still persists; no new config field |
| AC-08 | 1 | No migration; upsert test still sees one pending row |
| AC-09 | 2 | Existing `TestReceiveWACStandbyContext` still has `LenQueuedMsgs()==0` |
