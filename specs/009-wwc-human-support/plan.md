# Implementation Plan: WWC human-support forwarding

**Branch**: `009-wwc-human-support` | **Date**: 2026-11-23 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/009-wwc-human-support/spec.md`

## Summary

Courier forwards Engine queue facts for Shopping Assistant human support to weni-webchat-socket `POST {base_url}/send`, and stores the shopper `message.message_id` from the existing receive callback on `msgs_msg.external_id`. Privileged bodies carry `Authorization: Bearer` from `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN`. `message_status` HTTP 404 and HTTP 500 retry inside the send; 403 uses only the existing mailroom `MsgErrored` schedule. Execution detail: [research.md](./research.md), [data-model.md](./data-model.md), [contracts/wwc-human-support.md](./contracts/wwc-human-support.md), [quickstart.md](./quickstart.md).

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`, toolchain `go1.24.x`); module path `github.com/nyaruka/courier`

**Primary Dependencies**: `handlers/weniwebchat` send and receive, `utils.MakeHTTPRequest` / `GetHTTPClient`, `github.com/buger/jsonparser`, `github.com/nyaruka/ezconf` v0.2.1, `logrus`

**Storage**: existing Postgres `msgs_msg.external_id` and `msgs_msg.metadata`. No new column. Milestones, delivery status, and the agent name stay in the socket.

**Testing**: `go test ./handlers/weniwebchat/ -count=1` and a small `go test . -count=1 -run TestBestEffortAction` for the sender predicate. Socket stubbed with `httptest`. No Postgres or Redis required for these tests.

**Target Platform**: Linux Courier service. Stateless process. Outbound HTTP to the channel `base_url`.

**Project Type**: backend messaging gateway (WWC handler slice plus one sender predicate)

**Performance Goals**: no new inbound route. Added outbound work stays inside the declared service peak of **242.9 req/s** (1-minute, 2026-10-28 14:06 UTC). A privileged call holds one sender goroutine for at most 3 attempts inside the existing 35s send context.

**Constraints**: do not change `utils.shouldRetryTransient`; do not export `utils.withJitter`; nameless `typing_start` and unlabeled chat JSON stay byte-compatible via `omitempty`; WhatsApp handlers untouched; empty token fails closed with no HTTP call; agent name and bearer never reach logrus or the stored channel log

**Scale/Scope**: `handlers/weniwebchat`, `msg.go` action constants, `sender.go` predicate, `config.go` one field, WWC tests, changelog at release

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Notes |
|-----------|------|-------|
| I. Reviewed Changes on a Protected Main Branch | PASS | Lands via PR that names this spec |
| II. Secrets and Supply Chain | PASS | One `COURIER_*` secret, empty default, redacted in the channel log. No new module |
| III. Structured, Privacy-Safe Observability | PASS | New logrus fields enumerated in the spec. URN, text, token, and name are not fields |
| IV. Versioned Contracts | PASS | Minor additive queue, `/send`, receive, and env contracts. See [contracts/wwc-human-support.md](./contracts/wwc-human-support.md) |
| V. Specification Traceability | PASS (justified exception) | Inheritance block pins `59dabf0b030431fae835756a43aa7efd8d58f953`. That commit has no tag and the product file is still Draft. The spec records the departure. Planning does not widen it |
| VI. No Silent Divergence | PASS | Divergence Gate below is `none` |
| VII. Conventional, Atomic Commits | PASS | Implement phase |
| VIII. Release Changelog | PASS | `WENI-CHANGELOG.md` is the last step of the delivery sequence |
| IX. Never Trust the Client | PASS | Callback `message_id` and queue metadata validated in the handler before write or POST |
| X. Fail Gracefully and Predictably | PASS | `GetHTTPClient` plus the 35s send context. Outcomes are `MsgSent`, `MsgFailed`, or `MsgErrored` |
| XI. Bounded Retry Over REST | PASS | 3 attempts, 500ms backoff with local jitter, for 500 and for `message_status` 404. 401/400/409 are not retried. 403 is mailroom-only |
| XII. Stateless, Peak-Sized Services | PASS (justified exception) | No new process or disk state. Peak is the service-wide series; Istio has no WWC path label |
| XIII. Diagnosable Errors | PASS (justified exception) | `channel_uuid`, `msg_uuid`, `event_id` or `message_id`. `courier.Msg` has no contact UUID; no new lookup |
| XIV. Tests Exercise Flows | PASS | Receive, chat, product, each control type, typing, and the status matrix. See [quickstart.md](./quickstart.md) |
| XV. Explicit Over Clever | PASS | Caps and attempt counts are named constants in the handler. No inferred author and no name splitting |
| XVI. Contained Changes | PASS | WWC handler, WWC-only sender predicate, action constants, one config field, WWC tests. Shared retry helper and the action test harness stay as they are |

**Post-Phase 1 re-check**: PASS. Contracts match the spec status matrix, including in-request `message_status` 404 and mailroom-only 403. Research keeps jitter and the 500 retry inside the WWC handler. Justified exceptions are unchanged (untagged product pin, service-wide peak, no contact UUID).

## Project Structure

### Documentation (this feature)

```text
specs/009-wwc-human-support/
├── plan.md
├── spec.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── wwc-human-support.md
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks) — not created here
```

### Source Code (repository root)

```text
config.go                                 # WwcHumanSupportIngressToken
msg.go                                    # action type constants
sender.go                                 # bestEffortAction: existing typing_indicator, plus WWC typing_stop
handlers/weniwebchat/
├── weniwebchat.go                        # receive message_id; author/name; control POSTs; postPrivileged
└── weniwebchat_test.go                   # flow cases and the failure matrix
WENI-CHANGELOG.md                         # minor entry when the code ships
```

**Structure Decision**: single Go module. The WWC handler owns parsing, validation, HTTP, and redaction. `sender.go` only decides which actions skip the normal status path. RapidPro already persists `external_id` and `metadata`; this plan does not edit it.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| V product spec is untagged | The developer pointed at a clean `origin/main` commit that has no tag, and the socket spec pins the same SHA | Refusing to plan would block the courier slice of a feature whose product file is already the agreed pin |
| XII peak uses the inbound service-wide series | Istio has no path label, so WWC send rate is not isolated | Inventing a WWC QPS without a series |
| XIII no contact UUID | `courier.Msg` does not expose one, and a lookup would be a new read on the send path | Logging the URN would violate Principle III |

## Inherited Context

- Product Spec: Atendimento humano visível no Shopping Assistant (tag, marcos, horário, status de leitura e nome do atendente) — https://github.com/weni-ai/vtex-cx-experience-specs/blob/59dabf0b030431fae835756a43aa7efd8d58f953/specs/009-sa-human-support-status/spec.md
- Pinned version: `59dabf0b030431fae835756a43aa7efd8d58f953`
- Architecture doc: none
- Engineering Spec: `specs/009-wwc-human-support/spec.md`

## Delivery Sequence

1. Add `WwcHumanSupportIngressToken` on `courier.Config` with an empty default. ezconf binds `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN` (`CamelToSnake("WwcHumanSupportIngressToken")` is `wwc_human_support_ingress_token`).
2. Add the action constants in `msg.go`. No behaviour change until the handler reads them.
3. Teach `receiveMsg` to copy a valid `message.message_id` onto `external_id`. Existing receive tests must stay green before outbound work.
4. Add outbound author/name, control-event bodies, `postPrivileged`, and channel-log redaction in `handlers/weniwebchat`. Unlabeled chat and nameless `typing_start` stay on the current HTTP helpers.
5. Extend `SendAction` for a named `typing_start` and for `typing_stop`.
6. Route WWC `typing_stop` through the existing best-effort action return in `sender.go`. `typing_indicator` stays best-effort for every channel.
7. Land the handler flow tests and the sender predicate test from [quickstart.md](./quickstart.md).
8. On the release that contains the code, add the minor `WENI-CHANGELOG.md` entry. Do not edit upstream `CHANGELOG.md`.

**Cross-repository coordination**: weni-webchat-socket already accepts these bodies. Its flag `WWC_HUMAN_SUPPORT_STATUS` stays false until this Courier build is serving and the shared token is set. Engine emits the new `action_type` values and `author` only after that. Engine reads inbound `external_id` as the shopper `message_id`. The socket still must publish `timestamp_ms` on live non-privileged frames; that fix is not a Courier task.

## Peak Load Plan

- Declared peak: **242.9 req/s** over a 1-minute window at 2026-10-28 14:06 UTC, Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}` for 2026-10-07 through 2026-11-06.
- How this plan sustains it: no new inbound route, no fan-out, no new connection. Privileged retry is at most 3 HTTP calls inside the send goroutine that already exists. State that outlives the request stays in Postgres `external_id` or in the socket.
- How it is verified: handler tests bound the attempt count (3 requests, then stop). No new load test. The service-wide series remains the ceiling, as in specs 005–008.

## Observability Plan

- Structured logs added: privileged-path logrus fields `channel_uuid`, `msg_uuid`, `event_id` or `message_id`, `action_type`, `http_status`, `socket_error`. An invalid callback id logs `message_id_rejected: true` without the raw value.
- Correlation identifier: message UUID plus `event_id` for a control event; stored `external_id` for an accepted shopper callback.
- Error reports: the same opaque ids. Token, `agent_first_name`, URN path, and message text are absent. The channel log request dump replaces the bearer and the name before `WriteChannelLogs`.
- Signals watched after rollout: privileged `MsgFailed` with `socket_error` `unauthorized` or `bad_request` (config or Engine payload), a rise in `MsgErrored` on `message_status` (404 still missing after 3 tries, or socket 500), and 403s while the socket flag is still false.

## Rollout and Rollback Plan

- Rollout: deploy Courier with the token set and Engine still silent; then enable the socket flag and Engine emission together. Courier has no second flag.
- Backward compatibility window: absent metadata keys and a missing `message_id` produce today's JSON and an empty `external_id`. The previous socket image ignores unknown JSON on `/send` for unlabeled chat.
- Rollback: stop Engine emission of the new `action_type` values first. Then roll back the Courier image. In-flight control rows would be POSTed as chat `type: message` by the previous image.
- Migration reversibility: no schema change. `external_id` values already written remain and are harmless to the previous image.

## Divergence Gate

- Divergences found while planning: none
- Tasks blocked by an open amendment: none
