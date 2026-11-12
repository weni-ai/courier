# Implementation Plan: Email Thread Cc

**Branch**: `feat/email-chann-cc` | **Date**: 2026-11-11 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/005-email-thread-cc/spec.md`

## Summary

Copy replies stay on the thread's central `+wt-` URN, the agent sees the author's address as the first line of `text`, and the accumulated copy list travels in `metadata.email.cc`. Send reads that list from the latest visible message of the URN, not from the parent being replied to, and omits `cc` when the list is empty. No schema change. email-proxy implements the header contract; this plan only changes Courier.

## Technical Context

**Language/Version**: Go 1.24 (module `github.com/nyaruka/courier`, toolchain `go1.24.x`)

**Primary Dependencies**: `go-chi/chi`, `github.com/nyaruka/gocommon/urns`, `github.com/buger/jsonparser`, `logrus`, `stretchr/testify`. Outbound HTTP stays `utils.MakeHTTPRequest` / `GetHTTPClient`.

**Storage**: Postgres `msgs_msg.metadata` and `msgs_msg.text` via `backends/rapidpro`. No new column, index, or contact field. Redis, RabbitMQ and the spool are untouched.

**Testing**: `go test ./handlers/email/` (`RunChannelTestCases`, `RunChannelSendTestCases`) and `go test ./backends/rapidpro/` for the new lookup. CI remains `go test -p=1 ./...`.

**Target Platform**: Linux containers, the Courier service.

**Project Type**: Channel handler plus one backend read. Not a new service.

**Performance Goals**: No new HTTP hop. At most one extra `msgs_msg` read on receive and one on send, each `ORDER BY id DESC LIMIT 1`.

**Constraints**: Receive context 30s (`channelHandleWrapper`). Send client timeout 60s. Do not retry `POST /send` inside `SendMsg`. Do not log mailbox addresses or body. Empty `cc` must stay off the JSON so current exact-body tests remain valid.

**Scale/Scope**: Email channel only (`handlers/email`, `courier.Backend`, `backends/rapidpro`, `MockBackend`). Peak ceiling is the service inbound peak of 242.9 req/s (1-minute rate, 2026-10-28 14:06 UTC, window 2026-10-07 to 2026-11-06). Email is a subset of that peak.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Notes |
|-----------|------|-------|
| I. Reviewed changes | PASS | Ships through a PR to `main`. This plan does not push. |
| II. Secrets | PASS | No new secret. Bearer token stays `EMAIL_PROXY_AUTH_TOKEN`. |
| III. Privacy-safe logs | PASS | New log is `channel_uuid` plus integer `cc_dropped`. No address, URN path, or body. |
| IV. Versioned contracts | PASS | Optional `cc`, omitted when empty. Minor, backward compatible. See [contracts/email-cc.md](./contracts/email-cc.md). |
| V. Traceability | PASS | Spec opens with `## Origin Without Product Spec`. Product Spec is `none`. |
| VI. No silent divergence | PASS | Nothing inherited. Divergence Gate removed. |
| VII. Conventional commits | PASS | Implementation commits are `feat:`, imperative, ≤ 50 characters, atomic. |
| VIII. Changelog | PASS | User-facing author line and new recipients go in `WENI-CHANGELOG.md` on the release. Do not edit `CHANGELOG.md`. |
| IX. Never trust the client | PASS | `from` and `body` stay required. Each `cc` entry is checked before write. Channel UUID is resolved before the handler. |
| X. Fail predictably | PASS | Lookup uses the request context. Proxy call uses `GetHTTPClient`, not `http.DefaultClient`. Failures are HTTP 400 or status `E`. |
| XI. Bounded retry | PASS | No in-handler retry of `POST /send` (not idempotent). Worker path unchanged: status `E`, then `F` when `error_count >= 2`, backoff `5 * (error_count + 1)` minutes. `cc` is written only after HTTP 2xx. |
| XII. Stateless, peak-sized | PASS | List lives on `msgs_msg`. No process or disk state. Peak declared above; the plan adds one indexed read, not a new request class. |
| XIII. Diagnosable errors | PASS | Channel UUID, contact UUID when the backend already has it, message or channel-log UUID. No mailbox, no body. |
| XIV. Flow tests | PASS | Receive and send go through `RunChannelTestCases` and `RunChannelSendTestCases`, including failure paths in [quickstart.md](./quickstart.md). |
| XV. Explicit | PASS | Prefix, accumulation and the mailbox check are each one function. No new numeric threshold. |
| XVI. Contained | PASS | Email handler, one backend method, mock, email tests, one rapidpro test. Shared harness in `handlers/test.go` is not extended. |

No violation. Complexity Tracking is empty.

**Post-design re-check**: `research.md`, `data-model.md`, `contracts/email-cc.md` and `quickstart.md` add no required field, no migration, no local-disk state and no secret. Gates still PASS.

## Project Structure

### Documentation (this feature)

```text
specs/005-email-thread-cc/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── email-cc.md
├── checklists/
│   └── requirements.md
└── tasks.md             # /speckit-tasks, not this command
```

### Source Code (repository root)

```text
backend.go                          # Backend.LookupLastMsgByContactURN
test.go                             # MockBackend seed + lookup
handlers/email/email.go             # receive identity, body, cc; send cc
handlers/email/email_test.go        # flow cases
backends/rapidpro/backend.go        # method on *backend
backends/rapidpro/msg.go            # SQL + scan
backends/rapidpro/backend_test.go   # lookup returns a row with empty external_id
```

**Structure Decision**: Stay inside the email handler and the existing rapidpro message reads. `LookupLastMsgWithExternalID` remains the threading fallback. The copy list uses a new read so a row without `external_id` can still be the latest snapshot. See research decision 1.

## Complexity Tracking

No constitution violation requires justification.

## Inherited Context

- Product Spec: none
- Engineering Spec: `specs/005-email-thread-cc/spec.md`

## Delivery Sequence

1. **Backend read** — add `LookupLastMsgByContactURN` on `courier.Backend`, `backends/rapidpro` (SQL + `scanDBMsgWithMetadata`, `sql.ErrNoRows` mapped to `courier.ErrMsgNotFound`) and `MockBackend`. Land the rapidpro test in the same change. The handler cannot call a method the interface does not have.
2. **Receive** — in `contactURNForInbound`, adopt a stored URN whose path matches `threadTagPattern` regardless of `from`. Then prefix `text`, load the previous list, accumulate, store `metadata.email.cc`. Block check stays first, unchanged. Update the two existing receive cases whose expected URN was the rebuilt tag while a `+wt-` parent was seeded (research decision 3).
3. **Send** — after `buildReplyContext`, load the latest row for `msg.URN()`. Not found or empty `cc` omits the JSON key. Any other error returns status `E` and does not POST. On HTTP 2xx, write the same list into outbound metadata.
4. **Flow tests** — add the receive and send cases in [quickstart.md](./quickstart.md). Run `go test ./handlers/email/ -count=1`.
5. **Release note** — `WENI-CHANGELOG.md` when the change is released. Not part of the code change's compile, but required before the tag.

**Cross-repository coordination**: email-proxy must accept optional `cc` on `POST /send` and may start sending `cc` on receive before this Courier deploy. Courier must not be deployed first against a proxy that rejects unknown JSON. The proxy work is specified in [contracts/email-cc.md](./contracts/email-cc.md) and is not a task in this repository. Implementation here can proceed in parallel; only the production order is proxy, then Courier.

## Peak Load Plan

- Declared peak: 242.9 req/s inbound, 1-minute rate, 2026-10-28 14:06 UTC, Istio `istio_requests_total{destination_workload="courier", reporter="destination"}`, window 2026-10-07 to 2026-11-06. Email receive and send are a subset. No per-path series exists.
- How this plan sustains it: no new request class, no new HTTP call, no in-process cache, no lock. One extra `SELECT ... ORDER BY m.id DESC LIMIT 1` on the same channel-id + URN-identity join `LookupLastMsgWithExternalID` already uses, without the `external_id` predicates. State stays on `msgs_msg`.
- How it is verified: the SQL in `backends/rapidpro/msg.go` is the check. CI does not reproduce 242.9 req/s. A thread-wide scan or a second proxy call would fail this check. Confirmed: `selectLastMsgByContactURNSQL` ends with `LIMIT 1`, and `SendMsg` calls `utils.MakeHTTPRequest` once, only after the latest-message lookup returns.

## Observability Plan

- Structured logs added: on receive, when at least one `cc` entry is dropped, `logrus` fields `channel_uuid` and `cc_dropped` (int). Nowhere else. Do not add `from` to the existing blocked-mailbox line and do not log the list.
- Correlation identifier: channel UUID is on that log. The proxy call keeps `NewChannelLogFromRR` (channel-log UUID, message id). Send parent resolution already logs `channel_uuid` and `msg_id`; do not add the URN path to new lines.
- Error reports: a lookup or send failure that reaches Sentry carries channel UUID, contact UUID when already on the backend object, and message UUID or channel-log UUID. No mailbox and no body.
- Signals watched after rollout: email send status `E`/`F` rate and channel-log errors on `POST /send` do not step up after deploy. A step up with `cc` in the request body means the proxy is rejecting the new field; roll Courier back. No new metric is required for this slice.

## Rollout and Rollback Plan

- Rollout: no flag. Deploy email-proxy first (absent or empty `cc` keeps current SMTP). Deploy Courier second, via PR to `main`. Threads with no list omit `cc` and keep today's send.
- Backward compatibility window: the previous Courier binary ignores `email.cc` (`encoding/json` on `emailThreadMetadata`). Inbound `cc` from a newer proxy is discarded by that binary. Outbound from that binary has no `cc` key.
- Rollback: redeploy the previous Courier image. New mail goes back to building a URN from `from`. Prefixed rows already stored stay prefixed.
- Migration reversibility: there is no migration. The `cc` key left in metadata is ignored by the previous binary.

Do not roll the proxy back to a decoder that rejects unknown fields while this Courier version is still sending a non-empty `cc`.
