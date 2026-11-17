# Implementation Plan: WAC Thread Ownership

**Branch**: `005-wac-thread-ownership` | **Date**: 2026-11-12 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/005-wac-thread-ownership/spec.md`

## Summary

Courier will derive WhatsApp Cloud (`WAC`) multipartner thread ownership from Meta webhooks and keep it in Redis for 24 h, so a later send can know whether another responder holds the thread. `control_passed` writes `owner` even without a summary; `control_taken` writes `not_owner` and stays a 200 with no inbound message; `field=messages` only updates an existing key. Two global flags default off: standby-context persist and send blocking (fail-open on missing key or Redis read error). Ownership writes always run. Execution order and artifacts: [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wac-thread-ownership.md](./contracts/wac-thread-ownership.md), [quickstart.md](./quickstart.md).

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`, toolchain `go1.24.x`); module path `github.com/nyaruka/courier`

**Primary Dependencies**: `github.com/gomodule/redigo/redis` (`redis.NewScript` / `EVAL`), `github.com/sirupsen/logrus`, `github.com/nyaruka/gocommon/urns`, existing `handlers` test helpers (`RunChannelTestCases`, `RunChannelSendTestCases`, `NewTestServer`)

**Storage**: Redis/Valkey hash `wac:thread_owner:{channel_uuid}:{urn.Identity()}` with TTL 24 h (new). Postgres `wa_conversation_handover` unchanged (existing writer only).

**Testing**: `go test ./handlers/facebookapp/` against local Redis (MockBackend DB 0) and `httptest` Graph; config default test in package `courier`

**Target Platform**: Linux Courier service (horizontal scale; no process-local ownership cache)

**Project Type**: backend messaging gateway (channel handler slice)

**Performance Goals**: sustain the declared inbound peak of **242.9 req/s** (1-minute rate, 2026-10-28 14:06 UTC) with at most one Redis `EVAL` per WAC message/handover event; send-path `HGET` only when the send flag is on

**Constraints**: no URN/phone/message text in new logs (Principle III); no `Backend` interface growth; WAC only (not `WCD`); flags default false; send fail-open on Redis read failure; Lua compare-and-set must be atomic; `http.DefaultClient` unused (unchanged Graph client)

**Scale/Scope**: one handler package (`handlers/facebookapp`) + two `config.go` bools + changelog; no other channel, no Thread Control API client, no Mailroom contract change beyond `MsgFailed` when the send flag is on

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Notes |
|-----------|------|-------|
| I. Reviewed Changes on a Protected Main Branch | PASS | Lands via PR to `weni-ai/courier`; not planned as a direct `main` push |
| II. Secrets and Supply Chain | PASS | No new secret; flags are `COURIER_*` bools; no new module |
| III. Structured, Privacy-Safe Observability | PASS | New events use `channel_uuid`, `outcome`, `state`, `source`, `msg_uuid` only. Pre-existing `contact_urn` on handover logs is not extended (Principle XVI) |
| IV. Versioned Contracts | PASS | Two MINOR config additions, default off. Webhook status codes unchanged. Redis key internal. See [contracts/wac-thread-ownership.md](./contracts/wac-thread-ownership.md) |
| V. Specification Traceability | PASS | Spec opens with `## Origin Without Product Spec`; this plan pins that spec |
| VI. No Silent Divergence | PASS | No Product Spec; nothing to diverge from. Section omitted below |
| VII. Conventional, Atomic Commits | PASS | Implement phase; not this command |
| VIII. Release Changelog | PASS | `WENI-CHANGELOG.md` MINOR entry in delivery sequence |
| IX. Never Trust the Client | PASS | Ownership derived after existing `X-Hub-Signature` check; Redis state is not client-supplied |
| X. Fail Gracefully and Predictably | PASS | Handover Redis errors → non-200; message Redis errors → 200 + log; send read errors → send |
| XI. Bounded Retry Over REST | PASS (justified exception) | No new REST hop. Message-path Redis write is not redeliverable so Meta does not duplicate `WriteMsg`. Handover-path is redeliverable. Documented in spec Constitution Alignment |
| XII. Stateless, Peak-Sized Services | PASS | State in Redis with TTL. Peak declared. Per-route series unavailable (Istio); service-wide peak reused |
| XIII. Diagnosable Errors | PASS (justified exception) | Sentry: `channel_uuid` + `event`/`source` or `msg_uuid`. No contact UUID on handover-only events; URN forbidden |
| XIV. Tests Exercise Flows | PASS | Webhook → Redis/DB and send → Graph-or-block; see [quickstart.md](./quickstart.md) |
| XV. Explicit Over Clever | PASS | Named TTL, key prefix, states, Lua outcomes, log event names |
| XVI. Contained Changes | PASS | WAC handler + config + changelog only |

**Post-Phase 1 re-check**: PASS. Design artifacts add no new store, no `Backend` method, no Product Spec divergence, and no extra public route. Justified exceptions unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/005-wac-thread-ownership/
├── plan.md              # This file
├── spec.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── wac-thread-ownership.md
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks) — not created here
```

### Source Code (repository root)

```text
config.go                                    # EnableWACStandbyContext, EnableWACSendOwnershipBlock
WENI-CHANGELOG.md                            # MINOR entry
handlers/facebookapp/
├── wa_thread_owner.go                       # new: key, Lua, apply, get
├── wa_thread_owner_test.go                  # new: CAS unit tests
├── wa_handover.go                           # processMessagingHandover: always resolve sender; Redis then optional persist
├── facebookapp.go                           # messages update-if-exists; standby persist; send-path block
├── facebookapp_test.go                      # handover / messages / send flow tests
└── testdata/wac/
    ├── handoverNoContextWAC.json            # existing; assert Redis owner
    ├── handoverSummaryWAC.json              # existing; Redis + persist
    ├── handoverTakenWAC.json                # new
    ├── handoverTakenNoTimestampWAC.json     # new (entry.time fallback)
    ├── handoverStandbyContextWAC.json       # new
    └── helloWAC.json                        # existing; no key created
```

**Structure Decision**: Keep the slice inside `handlers/facebookapp`, which already owns WAC receive/send and handover parsing. New Redis helpers get their own file so `facebookapp.go` does not grow another inline Lua block. Tests stay next to the handler (`_test.go` + `testdata/wac`), matching existing WAC coverage. No new package under `backends/rapidpro`.

## Complexity Tracking

Justified exceptions from spec.md Constitution Alignment (gate still PASS):

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| XI: `field=messages` Redis write failure is logged, HTTP 200 | A non-200 makes Meta redeliver and `WriteMsg` duplicates | Treating Redis like a REST hop and failing the webhook |
| XII: WAC-only req/s not declared | Istio has no path label | Inventing a per-route peak without a series |
| XIII: no contact UUID on handover-only Sentry events | No contact row until a context persist | Attaching the URN (Principle III forbids it) |

## Inherited Context

- Product Spec: none
- Engineering Spec: `specs/005-wac-thread-ownership/spec.md`

## Delivery Sequence

1. **Config flags + named constants** — `NewConfig` defaults false so later wiring cannot enable send-block by accident.
2. **`wa_thread_owner.go` + unit tests** — Lua CAS must be proven (`created`/`updated`/`stale`/`absent`, equal and missing ts) before any webhook calls it.
3. **`processMessagingHandover`** — resolve sender for `control_passed` and `control_taken`, `mode=set`, then existing context persist. Unlocks AC-04 and AC-05; no-context path must no longer return before URN resolution.
4. **`field=messages` `update_if_exists`** — after primary URN parse; Redis error does not fail the webhook (AC-06).
5. **`field=standby` persist behind `EnableWACStandbyContext`** — default remains ignore (AC-07).
6. **Send-path check behind `EnableWACSendOwnershipBlock`** — WAC only inside `sendCloudAPIWhatsappMsg`; `not_owner` → `MsgFailed`, no Graph (AC-08). Requires step 2 `get`.
7. **Flow tests + changelog** — [quickstart.md](./quickstart.md) names; `WENI-CHANGELOG.md` MINOR.

**Cross-repository coordination**: none. Mailroom already consumes `wa_conversation_handover` and `MsgFailed`. Operators turn flags on after deploy (spec Rollout).

## Peak Load Plan

- **Declared peak**: 242.9 inbound req/s (1-minute rate at 2026-10-28 14:06 UTC), from spec NFR; plus at most 242.9 Redis `EVAL`/s if that peak were 100% WAC message/handover events. Send-path `HGET` only with the send flag on.
- **How this plan sustains it**: one EVAL per event on a single hash key (`HGET` + optional `HSET`/`EXPIRE`); existing Redis pool (`MaxActive` 8, `Wait: true`); no in-process ownership map; no extra HTTP on the inbound path; send flag default off so production send path stays at today's Redis cost until explicitly enabled.
- **How it is verified**: unit tests do not load-test Redis. After deploy (flags off), watch Courier error rate, Meta webhook 4xx/5xx on `/c/wac/receive`, and `wac_thread_owner_update_failed` count. Redis `EVAL` latency is the existing instance; no new SLO. Feature is not "done" until production satisfies spec ACs (flags may stay off).

## Observability Plan

- **Structured logs added** (from spec; implement with `logrus.Fields`, never interpolate URNs):
  - `wac_thread_owner_applied` INFO — `event`, `channel_uuid`, `outcome`, `state`, `source`
  - `wac_thread_owner_update_failed` ERROR — `event`, `channel_uuid`, `source`
  - `wac_send_blocked` INFO — `event`, `channel_uuid`, `msg_uuid`, `reason=not_owner`
  - `wac_send_ownership_read_failed` ERROR — `event`, `channel_uuid`, `msg_uuid`
- **Correlation identifier**: `channel_uuid` inbound; `channel_uuid` + message UUID on send.
- **Error reports**: Sentry via `logrus_sentry` on the two ERROR events; opaque IDs only (`channel_uuid`, `msg_uuid`).
- **Signals watched after rollout**: `wac_thread_owner_update_failed` (should be ~0); Meta retries on handover (non-200); after send flag on: `wac_send_blocked` vs successful Graph sends; `wac_send_ownership_read_failed` (Redis health). Confirm no URN paths in those events.

## Rollout and Rollback Plan

- **Rollout**: (1) Deploy Courier, flags unset/false — ownership writes start, sends unchanged. (2) Confirm keys on a routing-enabled WAC number and `control_passed` summaries still insert. (3) Enable `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` in staging then production. (4) Enable `COURIER_ENABLE_WAC_STANDBY_CONTEXT` independently if standby context persist is wanted.
- **Backward compatibility window**: previous Courier ignores the new keys and env vars. Default-off flags keep Graph and standby behaviour identical to 1.79.x.
- **Rollback**: set both flags false (send and standby revert immediately) or redeploy the previous tag (stops Redis writes too). Orphan keys expire in ≤24 h.
- **Migration reversibility**: no schema change. Safe.

## Delivery artifacts vs spec ACs

| AC | Lands in sequence step | Proven by |
|----|------------------------|-----------|
| AC-01 | 1 + 2 (writes ignore flags) | `NewConfig` defaults; flow tests with flags false |
| AC-02 | 2 | `HGETALL` + TTL in unit/flow tests |
| AC-03 | 2 | `wa_thread_owner_test.go` stale/equal/missing |
| AC-04 | 3 | `TestReceiveWACHandover` no-context + summary |
| AC-05 | 3 | `handoverTakenWAC.json` |
| AC-06 | 4 | `helloWAC.json` absent vs seeded |
| AC-07 | 5–6 | standby fixture; send with flag false |
| AC-08 | 6 | send `not_owner` / missing / Redis error |
