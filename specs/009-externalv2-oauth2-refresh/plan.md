# Implementation Plan: ExternalV2 OAuth2 Access-Token Refresh

**Branch**: `009-externalv2-oauth2-refresh` | **Date**: 2026-11-26 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/009-externalv2-oauth2-refresh/spec.md`

## Summary

Opt-in OAuth2 access-token renewal for the `E2` send path. A channel that sets `token_refresh_enabled` gets one in-memory auth state per `SendMsg`, shared by every part. The state is refreshed before the partner call when `expires_in` has passed, and once after the partner call when the response matches `when` (a status in `status_codes`, or a non-2xx body match). The renewed token is sent as `Authorization: Bearer <token>` and persisted in the channel config (`send_authorization`, `refresh_token`, `expires_in`) through a clone passed to `Backend().UpdateChannelConfig`. Every token attempt writes a body-less `Token Refresh` channel log, and the `Authorization` value is redacted in every E2 partner send log. Channels that do not opt in send the same provider request as today. Execution order and artifacts: [research.md](./research.md), [data-model.md](./data-model.md), [contracts/e2-token-refresh.md](./contracts/e2-token-refresh.md), [quickstart.md](./quickstart.md).

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`, toolchain `go1.24.x`); module path `github.com/nyaruka/courier`

**Primary Dependencies**: `net/http`, `net/url`, `encoding/json`, `text/template` (existing handler code), `logrus`, `github.com/nyaruka/gocommon/dates` (already used by `handlers/telesom` and `handlers/clickmobile`; gives a controllable clock), `courier/utils` (`MakeHTTPRequest`), the `courier.Backend` interface (`UpdateChannelConfig`). No new dependency and no `go.mod` change. `github.com/buger/jsonparser` stays available but is not used for the field lookup (see research R2).

**Storage**: Postgres `channels_channel.config` (JSON text) rewritten through `UpdateChannelConfig`. No schema change. No Redis, no file.

**Testing**: `go test -p=1 ./handlers/externalv2/...`; flow tests through `handlers.RunChannelSendTestCases`, direct `SendMsg` tests with a stateful `httptest` server for the 401-then-retry sequences and with a failing `MockBackend` wrapper (`handlers.NewTestServer`) for the save failure; `logrus` test hook for application-log assertions. No network access to real providers.

**Target Platform**: Linux Courier service (stateless processes; channel config in Postgres)

**Project Type**: backend messaging gateway (one channel handler)

**Performance Goals**: sustain the declared peak of **242.9 sends/s** (1-minute rate, 2026-10-28 14:06 UTC, window 2026-10-07 to 2026-11-06; the Courier service-wide figure used by specs 003 to 008 as a proxy for `E2` outbound). Worst case at that peak, every part opted in and replayed: at most 485.8 partner calls/s, 485.8 token calls/s and 485.8 config writes/s; steady state is one refresh per expiry window per channel (see Peak Load Plan).

**Constraints**: provider request unchanged for channels without `token_refresh_enabled`; no new `COURIER_*` setting; per part at most 2 token calls and 2 partner calls; outbound HTTP only through `utils.MakeHTTPRequest`; no secret, token body or URN path in any log; nothing outside `handlers/externalv2` and `WENI-CHANGELOG.md` changes (Principle XVI).

**Scale/Scope**: one handler package (2 source files, 2 test files, `doc.md`) plus one changelog entry. Flows channel form, Mailroom, other handlers, `utils/`, `backend.go`, `channel_log.go` and `test.go` are not touched.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Gate | Notes |
|-----------|------|-------|
| I. Reviewed Changes on a Protected Main Branch | PASS | Lands through a PR on `weni-ai/courier`; the PR states [spec.md](./spec.md) |
| II. Secrets and Supply Chain | PASS | Fake fixtures only; `client_secret`, `refresh_token`, `access_token` never in the repo, config files or logs. No new module; `go.mod` untouched |
| III. Structured, Privacy-Safe Observability | PASS | Logrus fields only (`channel_uuid`, `msg_uuid`); `Token Refresh` log has no bodies; `Authorization` value redacted in every E2 partner send log (Q2, option A). The existing `sender.go` log of `msg_text` and `msg_urn` is outside this change |
| IV. Versioned Contracts | PASS | Additive optional keys, MINOR, backward compatible. See [contracts/e2-token-refresh.md](./contracts/e2-token-refresh.md) |
| V. Specification Traceability | PASS | Spec opens with `## Origin Without Product Spec` in the mandated format |
| VI. No Silent Divergence | PASS (n/a) | No Product Spec, nothing inherited. Differences from the engineering design are recorded in the spec's Implementation Decisions |
| VII. Conventional, Atomic Commits | PASS | One commit per task in the implement phase |
| VIII. Release Changelog | PASS | `WENI-CHANGELOG.md` entry is the last delivery step |
| IX. Never Trust the Client | PASS | The token response is external input: status checked, JSON parsed defensively, field types and whole-number seconds validated, a miss fails the refresh. Operator-set URL and body are trusted at the `send_url` level (spec, Security) |
| X. Fail Gracefully and Predictably | PASS | Token and partner calls use the shared client (60s timeout); every token failure becomes an errored status plus a `Token Refresh` log; no panic, no internal detail returned |
| XI. Bounded Retry Over REST | PASS (justified departure) | Replay capped by `maxTokenRefreshReplays = 1`, with the backoff strategy named as `tokenRefreshReplayDelay = 0`. Replaying after a 401/403 is a departure from "no 4xx retry" and from the dedup-key rule; see Complexity Tracking |
| XII. Stateless, Peak-Sized Services | PASS | State is per `SendMsg` call and in memory only; the config row in Postgres is the only shared state. Peak declared: 242.9 sends/s (1-minute rate, window 2026-10-07 to 2026-11-06), the service-wide proxy used by specs 003 to 008 because no `E2` or outbound series exists |
| XIII. Diagnosable Errors | PASS (justified exception) | The save-failure error log carries `channel_uuid` and `msg_uuid`, no sensitive data, and stays at error level on purpose (an unsaved rotated refresh token breaks later refreshes) so Sentry reports it. `courier.Msg` exposes no contact UUID inside the handler, so that identifier is not attached (same exception as specs 005 and 008). Recorded in the spec's XIII row |
| XIV. Tests Exercise Flows | PASS | Main paths through `RunChannelSendTestCases`; stateful sequences through direct `SendMsg` tests with the reason recorded; every failure path in the spec is covered. See [quickstart.md](./quickstart.md) |
| XV. Explicit Over Clever | PASS | Named constants for the replay cap and its zero delay, Bearer prefix, default token field, default method, match mode, types, grant type, log description, redaction marker and regexp, enabled values, form field names, failure reasons and the log event, trigger and outcome values; status thresholds use `net/http` constants and the form `Content-Type` reuses `contentTypeMappings`; log field keys stay inline once in one helper; why-comments only |
| XVI. Contained Changes | PASS | `handlers/externalv2` and `WENI-CHANGELOG.md` only. The Teams `cloneChannelConfig` helper is not reused; `channel_log.go` and `test.go` are untouched |

**Post-Phase 1 re-check**: PASS. Design does not need a change outside the package. Two points were found during research and are recorded, not hidden: the provider request is the same byte for byte on the disabled path but the stored send log is redacted (R6), and the port from Mailroom is deliberately stricter in four places so that it matches the spec (R3). Justified exceptions unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/009-externalv2-oauth2-refresh/
├── spec.md
├── plan.md                      # This file (/speckit-plan command output)
├── research.md                  # Phase 0 output
├── data-model.md                # Phase 1 output
├── quickstart.md                # Phase 1 output
├── contracts/
│   └── e2-token-refresh.md      # Phase 1 output
├── checklists/
│   └── requirements.md
└── tasks.md                     # Phase 2 output (/speckit-tasks - NOT created here)
```

### Source Code (repository root)

```text
handlers/externalv2/
├── doc.md                       # modified: new keys, two examples, operator limits
├── externalv2.go                # modified: auth state, Bearer, proactive refresh, replay, log redaction, persistence
├── externalv2_test.go           # modified: new send-flow tests; existing tests untouched
├── token_refresh.go             # new: parser, when matcher, token request, path lookup, expiry
└── token_refresh_test.go        # new: parser and token-call unit tests
WENI-CHANGELOG.md                # modified: release entry
```

**Structure Decision**: single existing handler package. `token_refresh.go` has no dependency on `externalv2.go` types except `courier.Channel` and `utils.RequestResponse`, so the parser and token call are testable alone and the send flow only consumes `parseTokenRefreshOptions`, `(*tokenRefreshOptions).matches` and `requestAccessToken`.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| XI: replay after a 401/403 (a 4xx), with no deduplication key | A 401/403 caused by an expired or revoked token is a stale credential, not a defect in the request, so one attempt with a new token can succeed. Capped at 1 per part, and the spec's Q3 option A removes the duplicate-delivery path (no replay on a 2xx body match). The residual risk is a non-2xx status the operator lists in `status_codes`, which `doc.md` warns about | Not replaying leaves the manual rotation this feature removes. `MakeHTTPRequestWithRetry` retries 5xx/429 and cannot swap the `Authorization` header. An `Idempotency-Key` needs partner support and was not chosen (Q3, option B) |
## Inherited Context

- Product Spec: none
- Engineering Spec: `specs/009-externalv2-oauth2-refresh/spec.md`

## Delivery Sequence

Tests come before implementation inside every step. Each step ends with `go test -p=1 ./handlers/externalv2/...` green, including the unchanged existing tests.

1. **Parser and its tests** (`token_refresh_test.go`, then `token_refresh.go`) — everything else consumes the parsed options. Covers the 8 ordered rules, defaults, truthy values, the `when` matcher (Q3 rule), the dotted-path lookup and the expiry computation. It can land alone: nothing calls it yet.
2. **Token call** (`token_refresh_test.go` with an `httptest` token server, then `token_refresh.go`) — builds the `custom` and `refresh` requests, calls `utils.MakeHTTPRequest`, reads the fields and returns a body-less `Token Refresh` channel log for every attempt, including failures. Needs step 1.
3. **Send-flow changes** (`externalv2_test.go`, then `externalv2.go`) — one auth state per `SendMsg`, Bearer header, proactive refresh, single replay from a body snapshot, shared state across parts. Needs steps 1 and 2. The disabled path keeps its request.
4. **Persistence** (`externalv2_test.go`, then `externalv2.go`) — clone `channel.Config()`, write the three keys, call `UpdateChannelConfig`, log and continue on failure. Needs step 3, because it runs after a successful token call.
5. **Redaction, logs and constants** (`externalv2_test.go`, then `externalv2.go`) — the `Authorization` redaction in every partner send log (it touches every send log), the structured application logs for token attempts, and the named constants audit. Needs step 4. Until it lands, an opted-in channel's send log still holds the plain `Authorization` value (Principle III), so steps 3 to 5 ship in one PR and no intermediate commit is merged or deployed alone.
6. **Docs and changelog** — `doc.md` keys, the two examples from the design and the operator limits (non-2xx `body_contains`, recommended `status_codes`, `expires_in_field`/`expires_in_default`, rotation across workers), then the `WENI-CHANGELOG.md` entry.
7. **Verification** — `go test -p=1 ./handlers/externalv2/...`, `go vet ./handlers/externalv2/...`, `gofmt -l handlers/externalv2`, then the pilot check described in the Peak Load Plan.

**Cross-repository coordination**: none. The Flows channel form is out of scope: the new keys are written into the channel config out of band and Flows is not changed. Mailroom is not changed either.

## Peak Load Plan

- Declared peak: **242.9 sends/s** (1-minute rate at 2026-10-28 14:06 UTC, window 2026-10-07 to 2026-11-06), from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}`, the service-wide figure of specs 003 to 008. It is a conservative proxy for `E2` outbound: Istio has no path label and Courier has no outbound series. Worst case at that peak, every part opted in and replayed: 485.8 partner calls/s (242.9/s extra), 485.8 token calls/s and 485.8 `UpdateChannelConfig` writes/s. At most 32 `SendMsg` calls run per instance (`MaxWorkers`), so at most 32 token calls are in flight per instance.
- How this plan sustains it:
  - The auth state is per `SendMsg` call and lives only in memory for that call, so no state outlives the request (Principle XII).
  - The only shared state is the channel config row in Postgres. Concurrent refreshes by several workers are last-write-wins, with no lock and no Redis.
  - Added work is bounded. Per part: at most 2 token calls (one proactive, one after a match) and at most 1 extra partner call (the replay cap, `maxTokenRefreshReplays = 1`), and at most 1 `UpdateChannelConfig` write per successful token call. In steady state this is one refresh per expiry window per channel, because the new `expires_in` is persisted, plus the replay cap. That steady-state figure holds only when the token response gives an expiry (`expires_in_field` or `expires_in_default`): without it the stored expiry stays in the past and every message refreshes once. `doc.md` tells operators to set one of them.
  - Opt-in per channel: channels without `token_refresh_enabled` add no work.
- How it is verified: tests prove the per-part bound (T030: exactly 2 token calls and 2 partner calls in the worst part; T030a and T036 the cap). In staging, enable one pilot channel first and compare the token calls and config writes from the `Token Refresh` logs and the `token_refresh_save` events with the steady state (one refresh per expiry window). This is a precaution, not a gate on the declared peak. If an `E2` series such as `courier.msg_send_E2` becomes available, a later amendment may replace the proxy with it.

## Observability Plan

- Structured logs added: `externalv2.go` (token attempt outcome, save failure) with logrus fields `comp=externalv2`, `channel_uuid`, `msg_uuid`, `event` (`token_refresh`, `token_refresh_save`), `trigger` (`expired` or `match`), `outcome`, `reason` (fixed classification), `status_code`, `elapsed`. Never the URN path, message text, bodies, `client_secret`, `refresh_token`, `access_token` or the `Authorization` value.
- Channel logs: every token attempt writes its own channel log with description `Token Refresh`: method, URL without query string and user info, status code and elapsed only; `Request` and `Response` empty; `Error` is a fixed reason string, never the raw Go error.
- Redaction decision (spec Q2, option A): the `Authorization` header value in the request dump of every E2 partner send log is replaced by `[REDACTED]` inside the handler, before the log is built, for opted-in and non-opted-in channels alike. `channel_log.go` is not changed.
- Correlation identifier: `channel_uuid` from `msg.Channel().UUID()` and `msg_uuid` from `msg.UUID()`, taken inside `SendMsg`/`sendMsgPart`; the existing channel logs already carry the message ID.
- Error reports: the save-failure log at error level reaches Sentry through the existing hook with `channel_uuid` and `msg_uuid` as opaque identifiers; no URN, name or message text. Error level is deliberate: a rotated refresh token that was not saved is lost. No contact UUID (Principle XIII exception above, recorded in the spec's XIII row).
- Signals watched after rollout: count of `Token Refresh` logs per channel, rate of errored `E2` messages, rate of `UpdateChannelConfig` failures (`token_refresh_save` events).

## Rollout and Rollback Plan

- Rollout: opt-in per channel through the config key `token_refresh_enabled`; there is no environment variable and no deploy ordering with another repository. Enable one pilot channel, watch the signals, then widen.
- Backward compatibility window: a previous Courier version ignores the new keys and keeps sending `send_authorization`, which already holds `Bearer <last refreshed token>` in the format it reads today. Valid until that token expires.
- Rollback: remove `token_refresh_enabled` from the channel (effective when the channel cache is refreshed, up to 5 minutes), or deploy the previous Courier version. The log redaction is not behind the key: only the previous deploy removes it.
- Migration reversibility: no schema change and no backfill, so nothing to reverse. The extra keys left in the config JSON are harmless to the previous version.
