# Implementation Plan: Record WAC Standby

**Branch**: `007-record-wac-standby` | **Date**: 2026-11-13 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/007-record-wac-standby/spec.md`

## Summary

With `EnableWACStandbyContext` on, a `WAC` `field=standby` webhook appends shopper and partner lines to the pending `wa_conversation_handover` row and sets Redis `not_owner` through the existing timestamp compare. Status receipts update ownership and add no line. The standby HTTP body is omitted from the channel log whether the flag is on or off. No schema change, no new flag, no `WriteMsg`, no media download. Execution order and artifacts: [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-standby.md](./contracts/wac-standby.md), [quickstart.md](./quickstart.md).

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`, toolchain `go1.24.x`); module path `github.com/nyaruka/courier`

**Primary Dependencies**: `github.com/gomodule/redigo` (`SET NX EX`, existing `luaWACThreadOwnerApply`), `github.com/sirupsen/logrus`, existing `handlers` test helpers (`TestHandlerRequest`, `CaptureLogs`, `NewTestServer`), `persistRenderedWAHandover` and `applyWACThreadOwner`

**Storage**: Postgres `wa_conversation_handover` through the existing writer (no SQL change). New Redis string `wac:standby:seen:{channel_uuid}:{message_id}` (TTL 24h). Existing hash `wac:thread_owner:*` gains `not_owner` writes from standby only when the flag is on.

**Testing**: `go test ./handlers/facebookapp/ -count=1 -run 'TestRenderWACStandby|TestReceiveWACStandbyTranscript|TestReceiveWACStandbyContext|TestReceiveWACMessagesContext|TestReceiveWACHandover'` against `MockBackend` and local Redis. No Postgres suite.

**Target Platform**: Linux Courier service (horizontal scale; dedup and ownership live in Redis, pending text in Postgres)

**Project Type**: backend messaging gateway (WAC handler slice plus the inbound log wrapper)

**Performance Goals**: sustain the declared inbound peak of **242.9 req/s** (1-minute rate, 2026-10-28 14:06 UTC). Flag on, if every request were one standby message: 242.9 ownership `EVAL`s/s, 242.9 `SET NX`/s, and 242.9 pending-row upserts/s. No Graph media GET.

**Constraints**: no schema migration; no new `COURIER_*` flag; no `WriteMsg`, `WriteMsgStatus`, or `WriteChannelEvent` from standby; no `resolveMediaURL`; dedup key deleted when the context write returns an error; Redis connection not held across `WriteWAConversationHandover`; `WAC` only for lines and `not_owner` (`WCD` shares `processCloudWhatsAppPayload`); body omission does not depend on the flag; `backendTimeout` stays 20s

**Scale/Scope**: new `wa_standby.go` and its test, the standby branch in `facebookapp.go`, a context flag in `server.go`, fixtures under `testdata/wac/`, changelog. No Mailroom, no send-path change, no FBA/IG transcript.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Notes |
|-----------|------|-------|
| I. Reviewed Changes on a Protected Main Branch | PASS | Lands via PR to `weni-ai/courier`; not planned as a direct `main` push |
| II. Secrets and Supply Chain | PASS | No new secret, flag, or module. Admin token is not used |
| III. Structured, Privacy-Safe Observability | PASS (justified exception) | New logs are `channel_uuid`, `external_id`, `reason`. Standby body is removed from the channel log and from the wrapper `request` field. Pre-existing `contact_urn` on the DB-error log is not extended (Principle XVI) |
| IV. Versioned Contracts | PASS | Patch on the webhook and the channel log. Minor, default-off, on the existing flag. Redis keys are internal. See [contracts/wac-standby.md](./contracts/wac-standby.md) |
| V. Specification Traceability | PASS | Spec opens with `## Origin Without Product Spec`; this plan pins that spec |
| VI. No Silent Divergence | PASS | No Product Spec; nothing to diverge from. Divergence Gate section omitted |
| VII. Conventional, Atomic Commits | PASS | Implement phase; not this command |
| VIII. Release Changelog | PASS | `WENI-CHANGELOG.md` entry in the delivery sequence. Upstream `CHANGELOG.md` is not edited |
| IX. Never Trust the Client | PASS | Lines and `not_owner` come only from the signature-checked body. Missing URN or id does not write |
| X. Fail Gracefully and Predictably | PASS | Writer and ownership errors return HTTP 400. No media download |
| XI. Bounded Retry Over REST | PASS | No new REST hop. Failed write deletes the seen key and returns 400 so Meta redelivers. Successful line is idempotent via `SET NX` |
| XII. Stateless, Peak-Sized Services | PASS (justified exception) | Dedup and ownership are Redis with TTL. Pending text stays in Postgres. Peak declared. Per-route series unavailable (Istio); service-wide peak reused. Spool path not extended |
| XIII. Diagnosable Errors | PASS (justified exception) | Sentry via `wac_standby_context_write_failed`: `channel_uuid` + `external_id`. No contact UUID in the handler; URN forbidden |
| XIV. Tests Exercise Flows | PASS | Webhook → handover event and/or Redis, plus the channel log. See [quickstart.md](./quickstart.md) |
| XV. Explicit Over Clever | PASS | Named constants for the seen-key prefix, 24h TTL, origin labels, and the log placeholder. Ordering stays in the existing Lua script |
| XVI. Contained Changes | PASS | Standby branch, new file, log hook, fixtures, tests, changelog. No `field=messages` change and no send-path change |

**Post-Phase 1 re-check**: PASS. Design artifacts add no column, no `Backend` method, no flag, and no Product Spec divergence. The seen key and the `not_owner` write are the spec’s Redis state, not a new public contract. Justified exceptions unchanged: the pre-existing `contact_urn` log stays, per-route peak is the service-wide series, and the new error has no contact UUID.

## Project Structure

### Documentation (this feature)

```text
specs/007-record-wac-standby/
├── plan.md              # This file
├── spec.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── wac-standby.md
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks) — not created here
```

### Source Code (repository root)

```text
server.go                                 # MarkChannelLogBodyOmitted; redact dump before channel log and logrus
handlers/facebookapp/
├── wa_standby.go                         # parse, render, dedup, not_owner, persist
├── wa_standby_test.go                    # TestRenderWACStandby
├── facebookapp.go                        # standby branch calls the processor; Standby json.RawMessage
├── facebookapp_test.go                   # TestReceiveWACStandbyTranscript; update TestReceiveWACStandbyContext
├── wa_thread_owner.go                    # unchanged Lua; called with mode set, state not_owner, source standby
├── wa_thread_owner_test.go               # assertNoWACOwnershipPII gains the new event names
└── testdata/wac/
    ├── standbyShopperTextWAC.json        # new
    ├── standbyEchoTextWAC.json           # new
    ├── standbyEchoFlowWAC.json           # new
    ├── standbyEchoTemplateWAC.json       # new
    ├── standbyEchoTemplateNoStructureWAC.json
    ├── standbyShopperImageWAC.json       # new
    ├── standbyShopperAudioWAC.json       # new
    ├── standbyStatusWAC.json             # new
    ├── handoverStandbyContextWAC.json    # existing; flag-on now also not_owner
    └── helloWAC.json                     # existing; body still logged
WENI-CHANGELOG.md                         # entry on the shipping release
```

**Structure Decision**: Render, dedup, and the item loop live in `wa_standby.go` so `facebookapp.go` only branches. Lines go through `persistRenderedWAHandover` so the 006 append SQL is the only writer. Ownership goes through `applyWACThreadOwner` so the PR 335 compare is not reimplemented. Body omission lives in `server.go` because that is where `DumpRequest` is copied into the channel log. Handler tests use `MockBackend` and its Redis pool; the rapidpro suite is not required.

## Complexity Tracking

Justified exceptions from spec.md Constitution Alignment (gate still PASS):

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| III: pre-existing `contact_urn` on `writeWAConversationHandover` database errors | The writer already logs it. This change calls that writer and must not widen the log | Removing the field is a separate Principle XVI change (specs 005 and 006 left it) |
| XII: WAC-only req/s not declared | Istio has no path label | Inventing a per-route peak without a series |
| XIII: no contact UUID on `wac_standby_context_write_failed` | The handler does not have the contact row; `contactForURN` runs inside the writer | Attaching the URN (Principle III forbids it) |

## Inherited Context

- Product Spec: none
- Engineering Spec: `specs/007-record-wac-standby/spec.md`

## Delivery Sequence

1. **Renderer in `wa_standby.go` + `TestRenderWACStandby`** — pure line rules for text, media, flow, and both template shapes. Must land first: the webhook loop only calls it. Proves the string half of AC-03, AC-04, AC-05, and AC-06.
2. **Channel-log omission in `server.go`** — context pointer, `MarkChannelLogBodyOmitted`, body replacement on the panic defer and on the normal path. No WhatsApp parse. Proven when step 3 sets the flag; harmless until then.
3. **Standby branch + flow tests** — flag check, `WAC` gate, item order (messages and echoes, then statuses, then legacy summary), `applyWACThreadOwner`, `SET NX` / `DEL`, `persistRenderedWAHandover`. Fixtures and `TestReceiveWACStandbyTranscript`. Update `TestReceiveWACStandbyContext` so flag-on expects `not_owner`. Extend `assertNoWACOwnershipPII`. Proves AC-01, AC-02, AC-07 through AC-13, and the webhook half of AC-03 through AC-06. Existing `TestReceiveWACMessagesContext` and `TestReceiveWACHandover` stay green.
4. **Changelog** — `WENI-CHANGELOG.md` on the release that ships this change: transcript and `not_owner` behind the existing flag, standby body omitted from the channel log.

**Cross-repository coordination**: none. Mailroom already selects one pending row and sets `consumed_on`. It must not change. RapidPro application code is not in this repository. Live Desk is not called.

## Peak Load Plan

- **Declared peak**: 242.9 inbound req/s (1-minute rate at 2026-10-28 14:06 UTC, window 2026-10-07 to 2026-11-06). Added cost at that peak, if every request were one standby message and the flag were on: 242.9 `EVAL`s/s, 242.9 `SET NX`/s, and 242.9 upserts/s. Statuses are the `EVAL` only. Flag off adds no Redis command and no upsert.
- **How this plan sustains it**: reuse the ownership script and the existing pending-row upsert; one short Redis connection per command, closed before the 20s DB timeout; no media GET; no in-process state that outlives the request; spool path unchanged. Courier stays stateless.
- **How it is verified**: handler tests do not load-test Redis. After deploy with the flag still false, confirm standby channel logs show `[standby body omitted]` and that `field=messages` logs still contain the body. After the flag is enabled, watch `wac_standby_context_write_failed` and handover/standby HTTP 400s. No new SLO. Done means production satisfies the spec ACs, not a merged PR.

## Observability Plan

- **Structured logs added** (implement with `logrus.WithError` / `logrus.WithFields`, never interpolate line text, template parameters, or URNs):
  - `wac_standby_context_write_failed` ERROR — `event`, `channel_uuid`, `external_id`. Fired only when a transcript `WriteWAConversationHandover` returns an error.
  - `wac_standby_skipped` INFO — `event`, `channel_uuid`, `external_id` when present, `reason` (`no_urn`, `no_message_id`, `bad_timestamp`). `no_urn` and `no_message_id` skip the append. `bad_timestamp` skips only the ownership Lua call; the line is still appended with `OccurredOn` from `entry.time`.
  - `wac_thread_owner_applied` INFO — existing helper, `source=standby`.
- **Correlation identifier**: `channel_uuid` from the resolved channel; `external_id` from `messages[].id` or `message_echoes[].id`. Neither is a URN.
- **Error reports**: Sentry via the existing `logrus_sentry` hook on that ERROR. Opaque ids only. No contact UUID, no `context_text`, no phone.
- **Signals watched after rollout**: count of `wac_standby_context_write_failed` (near zero); channel logs for standby contain the placeholder and not message text; `field=messages` logs unchanged; with the flag on, `wac_thread_owner_applied` `source=standby` appears and a duplicate id does not grow the pending row twice.

## Rollout and Rollback Plan

- **Rollout**: deploy Courier with `EnableWACStandbyContext` still false. Body omission starts immediately. Transcript lines and standby `not_owner` start only when that flag is turned on. Send blocking still requires `EnableWACSendOwnershipBlock`.
- **Backward compatibility window**: the previous binary uses the same table. Mailroom’s select-one-pending-row contract is unchanged. A longer `context_text` still fits the existing Mailroom format. Default-off means a deploy alone does not change ownership or context.
- **Rollback**: redeploy the previous tag, or turn the flag off on this binary to stop new lines and new standby ownership writes. Log omission remains until the previous binary is deployed. The previous binary ignores `wac:standby:seen:*` and any `not_owner` keys; both expire within 24 hours.
- **Migration reversibility**: no migration. Pending rows that already contain transcript lines remain legal `history` values.

## Delivery artifacts vs spec ACs

| AC | Lands in sequence step | Proven by |
|----|------------------------|-----------|
| AC-01 | 3 | `standbyShopperTextWAC.json`: `[shopper] Test standby message`, zero queued msgs |
| AC-02 | 3 | `standbyEchoTextWAC.json`: `[outro atendimento]` plus the echo body |
| AC-03 | 1 + 3 | Renderer table and `standbyEchoFlowWAC.json` |
| AC-04 | 1 + 3 | Structured `summer_sale_2026` line, first line only carries the origin marker |
| AC-05 | 1 + 3 | Name and values, no sibling `template`, no Graph |
| AC-06 | 1 + 3 | `<image>` and `<audio>` with no media server |
| AC-07 | 3 | `standbyStatusWAC.json`: 200, no line, no `WriteMsgStatus`, `not_owner` |
| AC-08 | 3 | Older timestamp does not move a seeded hash; newer timestamp sets `not_owner`; equal timestamp keeps `ts`; legacy summary fixture also sets `not_owner` |
| AC-09 | 3 | Every standby case: `LenQueuedMsgs()==0`, no `msg` in `BackendCalls`, no status, no channel event |
| AC-10 | 3 | Second post does not append; TTL in `(0, 86400]`; other channel appends; status creates no seen key |
| AC-11 | 3 | Writer error → 400 and key absent; retry appends. Closed Redis pool → 400 and zero events |
| AC-12 | 2 + 3 | Placeholder in the channel log and in the error `request` field; `helloWAC.json` still logs `Hello World` |
| AC-13 | 3 | Flag off: no line, seeded hash unchanged, body still omitted |
