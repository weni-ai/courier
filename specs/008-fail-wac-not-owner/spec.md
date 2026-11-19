# Engineering Specification: Fail WAC Not-Owner Sends

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `[008-fail-wac-not-owner]`

**Created**: 2026-11-13

**Status**: Draft

## Origin Without Product Spec

- Product Spec: none
- Problem: Spec 005 already skips Graph when Redis is `not_owner`, but it also blocks template sends, and Graph `#131070` still lands as `MsgErrored` because `wacMTResponse` does not read `error.code`. That lets Mailroom retry a send Meta will keep refusing, and RapidPro never stores a specific `failed_reason`.
- Acceptance criteria:
  - **AC-01**: With `EnableWACSendOwnershipBlock` true, `sendCloudAPIWhatsappMsg` for channel type `WAC` still reads Redis ownership before Graph. State `not_owner` and a **service** message (no Cloud API template of category MARKETING, UTILITY, or AUTHENTICATION, and not `DirectSend`) does not call Graph, sets `MsgFailed`, writes `failed_reason=H`, and does not schedule another Courier/Mailroom send attempt (`next_attempt` unchanged because status is `F`, not `E`).
  - **AC-02**: With the flag true and state `not_owner`, a WAC template whose category is MARKETING, UTILITY, or AUTHENTICATION, and a `DirectSend` utility send, still call Graph exactly as today.
  - **AC-03**: Missing Redis key, empty/`owner` state, or any Redis **read** error still send as today (fail-open), matching spec 005 AC-08. Flag false: no ownership read, no 131070 special case, send path unchanged.
  - **AC-04**: `wacMTResponse` unmarshals Meta `error.code`. When the flag is true and the channel is `WAC`, and Graph returns HTTP error (or a 2xx body that still carries the error object) with `error.code == 131070`, `sendCloudAPIWhatsappMsg` calls `failWACSendOnGraph131070` after `requestWAC`: Redis `not_owner` via `applyWACThreadOwner` mode `set` with `time.Now().Unix()`; `MsgFailed` and `failed_reason=H`; not `MsgErrored`; no Graph retry for that attempt. `requestWAC` itself does not write Redis.
  - **AC-05**: `WriteMsgStatus` `UPDATE msgs_msg` persists `failed_reason` when the status object carries a non-empty reason. Empty reason leaves the existing column value. Courier test schema includes the column.
  - **AC-06**: Structured log for local block stays `event=wac_send_blocked reason=not_owner`. 131070 path logs `event=wac_send_blocked reason=131070` (plus `channel_uuid`, `msg_uuid`). Neither log includes URN or message text.
  - **AC-07**: Channel type `WCD` and other facebookapp channels are unchanged. No new `COURIER_*` flag.

## Scope in This Repository

**This repository implements**:

- Template exception on the existing `blockWACSendIfNotOwner` path in `handlers/facebookapp`.
- Parse of Meta Graph `error.code` in `requestWAC` / `wacMTResponse`, and terminal failure + Redis `not_owner` for `#131070` behind `EnableWACSendOwnershipBlock`.
- `MsgStatus` `failed_reason` plumbing through `WriteMsgStatus` SQL (`updateMsgID`, `updateMsgExternalID`, `bulkUpdateMsgStatusSQL`).
- Flow tests for template allow, service block, 131070, flag off, fail-open Redis, and `failed_reason` persistence.

**Out of scope here**:

- Flows UI copy and RapidPro `FAILED_REASON` choice registration (`H` / “Outro atendimento está com a conversa”). That ships in the Flows/RapidPro repository. Courier only writes the one-character code.
- A production SQL migration in this repo. RapidPro owns `msgs_msg`. Courier only updates the test `schema.sql` and the status `UPDATE`s.
- Changing inbound ownership (`control_passed` / `control_taken` / `field=messages` / standby). Specs 005–007 stay the source of those rules.
- A second send-block flag, Thread Control API calls, `WCD`, on-premises `WA`.
- Mapping other Meta error codes.

**Depends on**:

- Spec `005-wac-thread-ownership` in this tree: `EnableWACSendOwnershipBlock`, `getWACThreadOwner`, `applyWACThreadOwner`, `blockWACSendIfNotOwner`.
- RapidPro `msgs_msg.failed_reason` (nullable `varchar(1)`). Deploy RapidPro/Flows with choice `H` **before or with** Courier, so the UPDATE does not fail on a missing column or check constraint.
- Mailroom: already treats `status='F'` as terminal; no Mailroom change. It must not retry `F`.

## Current State

- `handlers/facebookapp/facebookapp.go` `sendCloudAPIWhatsappMsg` (line 2922): if `WAC` and `EnableWACSendOwnershipBlock`, calls `blockWACSendIfNotOwner` **before** `getTemplate`. Any `not_owner` key blocks, including marketing templates.
- `handlers/facebookapp/wa_thread_owner.go` `blockWACSendIfNotOwner`: `HGET state`; `not_owner` → `MsgFailed`, log `wac_send_blocked`; missing key / read error → send.
- `getTemplate` (line 4590) reads `metadata.templating` and `Template.Category`. `isMarketingTemplate` is computed later only to pick `/marketing_messages` vs `/messages`. `getWACDirectSend` already treats DirectSend as utility.
- `requestWAC` (line 3800): `utils.MakeHTTPRequest`; on any `err` (including non-2xx) returns the status still at `MsgErrored` (created at line 2971) without inspecting the body. `wacMTResponse` (line 2638) has only `messages` and `contacts`.
- `backends/rapidpro/status.go` `updateMsgID` / `updateMsgExternalID` / `bulkUpdateMsgStatusSQL`: set `status`, `error_count`, `next_attempt`, `sent_on`, `external_id`, `metadata`. No `failed_reason`. `status='E'` increments `error_count` and schedules `next_attempt`; `status='F'` is terminal.
- `courier.MsgStatus` (`status.go`) has `SetStatus` / `SetMetadata`, no failed-reason methods.
- Tests: `TestSendWACThreadOwner` in `facebookapp_test.go` (~1140) covers flag off, `not_owner` block, missing key, `owner`, Redis read error. No template case, no 131070 fixture.
- `backends/rapidpro/schema.sql` `msgs_msg` has no `failed_reason`.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `handlers/facebookapp/wa_thread_owner.go` | modified: skip block for allowed templates/DirectSend; add `failWACSendOnGraph131070` after each `requestWAC` return (WAC + flag only) | AC-02, AC-04, AC-07 |
| `handlers/facebookapp/facebookapp.go` | modified: resolve templating before the ownership gate; extend `wacMTResponse`; `requestWAC` unmarshals `error.code` even when `MakeHTTPRequest` returns err. It does not write Redis or set `MsgFailed`. | AC-01, AC-04 |
| `status.go` + `test.go` mock | modified: `FailedReason()` / `SetFailedReason(string)` on `MsgStatus` | AC-05 |
| `backends/rapidpro/status.go` | modified: persist `failed_reason` on the three UPDATEs when non-empty | AC-05 |
| `backends/rapidpro/schema.sql` (+ testdata if INSERT lists columns) | modified: nullable `failed_reason varchar(1)` | tests against the column |
| `handlers/facebookapp/facebookapp_test.go`, `wa_thread_owner_test.go`, `backends/rapidpro/backend_test.go` | modified | Principle XIV |
| `WENI-CHANGELOG.md` | modified | Principle VIII |

### Flow

**Outbound** (`SendMsg` → `sendCloudAPIWhatsappMsg`), `WAC` only:

1. Decode templating the same way `getTemplate` does today (reuse it; a decode error is still a send error, as today).
2. If `EnableWACSendOwnershipBlock` is false: skip Redis and skip 131070 special-case; remainder of the function unchanged.
3. If true: `blockWACSendIfNotOwner`. Inside it:
   - Redis read error or state ≠ `not_owner`: do not block.
   - State `not_owner` and allowed template (category in `{MARKETING, UTILITY, AUTHENTICATION}` after `strings.ToUpper`) or `msg.DirectSend()`: do not block.
   - Else: no Graph; `SetStatus(MsgFailed)`; `SetFailedReason(FailedReasonOtherOwner)` (`H`); log `wac_send_blocked` / `reason=not_owner`; return.
4. Build payload and call `requestWAC` as today.
5. `requestWAC`: always unmarshal `rr.Body` into `wacMTResponse` when the body is present, including when `MakeHTTPRequest` returned a non-2xx error. Do not set Wired when `Error.Code != 0`. Do not read or write Redis and do not set `MsgFailed` here. `WCD` shares this function and stops at this parsed payload.
6. After **each** `requestWAC` return, `sendCloudAPIWhatsappMsg` calls `failWACSendOnGraph131070(msg, status, resp)`. The helper returns false unless `ChannelType()=="WAC"`, `EnableWACSendOwnershipBlock`, and `resp.Error.Code == 131070`. When it returns true:
   1. `applyWACThreadOwner(..., not_owner, set, source=graph_131070)` with `ts=time.Now().Unix()`. Redis write error: log ERROR `wac_thread_owner_update_failed` `source=graph_131070`; still fail the message (Meta already refused).
   2. `status.SetStatus(MsgFailed)`; `SetFailedReason(H)`; INFO `wac_send_blocked` `reason=131070`.
   3. Return that status immediately (`return status, nil`); do not apply contact URN updates and do not continue the part loop.
7. Other Graph errors, flag off, or `WCD`: keep today’s `MsgErrored` (retry via `error_count` / `next_attempt`).

### Rejected Alternatives

- **New flag for 131070**: rejected; the user gates both behaviours on the existing send-block flag.
- **`MsgErrored` for 131070**: rejected; Mailroom would retry and hit 131070 again (the bug this spec closes).
- **Blocking templates**: rejected; Meta still accepts marketing/utility/auth templates when another app owns the customer-service window.
- **Storing the reason only in `metadata` JSON**: rejected; Flows needs `failed_reason` for the history label.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` | modified semantics: also exempts allowed templates and treats Graph 131070 as terminal `F`+`H` | minor | yes; default false keeps current Graph + `E` on 131070 |
| `courier.MsgStatus` | added `FailedReason` / `SetFailedReason` | minor | yes; optional, empty = no column write |
| `UPDATE msgs_msg` (status writer) | modified: may set `failed_reason` | minor | yes; empty leaves previous value |
| Outbound Graph `POST /{phone-number-id}/messages` | skip for service msgs when `not_owner`; still called for allowed templates | minor | yes when flag off |
| Redis `wac:thread_owner:*` | 131070 may `set` `not_owner` | none (internal) | previous Courier ignores the key |

**Consumers affected**: Mailroom/RapidPro see `status='F'` and `failed_reason='H'` instead of retried `E`. Operators: same env var. Meta: fewer refused retries.

## Data Model and Migrations

- **Redis**: unchanged key. 131070 uses mode `set` so a missing key becomes `not_owner` (the Graph response is authoritative).
- **`msgs_msg.failed_reason`**: nullable `varchar(1)`. Courier named constant `FailedReasonOtherOwner = "H"`. Empty on other failures.
- **Migrations**: none in Courier production. Test `schema.sql` adds the column. RapidPro must have the column (and allow `H`) before this binary writes it.

The previously deployed Courier does not write `failed_reason`; a rollback after RapidPro added `H` is safe (column unused).

## Non-Functional Requirements

### Peak Load

- Service-wide inbound peak remains **242.9 req/s** (1-minute, 2026-10-28 14:06 UTC, same Mimir series as spec 005). This spec is outbound-only.
- **Added cost with flag on**: same one Redis `HGET` per WAC send as 005, plus JSON parse of `error` on Graph failures (error path only). 131070 success path of the special case: one Lua `EVAL` (same script as inbound).
- Default flag off: zero extra Redis vs today’s send path after 005 (the 005 gate is already skipped).

### Observability

| Event | Level | Fields |
|-------|-------|--------|
| `wac_send_blocked` | INFO | `event`, `channel_uuid`, `msg_uuid`, `reason` (`not_owner` or `131070`) |
| `wac_send_ownership_read_failed` | ERROR | unchanged |
| `wac_thread_owner_applied` | INFO | existing fields; `source=graph_131070` on the Graph path |
| `wac_thread_owner_update_failed` | ERROR | `source=graph_131070` when the Lua write fails after 131070 |

No URN, phone, or message text. Correlation: `channel_uuid` + `msg_uuid`.

### Failure Handling

- **Redis read (send)**: fail-open, send Graph (AC-03). Timeout: existing pool, one attempt.
- **Redis write after 131070**: log, still `MsgFailed`+`H`. Do not retry Graph.
- **Graph**: existing `utils.MakeHTTPRequest` / shared client timeout. 131070 is not retriable (4xx business reject, Principle XI). Other 5xx/429 stay `MsgErrored` as today.
- **Idempotency**: a Mailroom retry of a message already `F` does not occur. A duplicate 131070 Lua apply uses `now` as `ts`; a later inbound `owner` with a greater `ts` can still win.
- **Postgres status write**: existing spool fallback.

### Security and Authorization

- Trust boundary is the Graph response body after Courier’s authenticated call (Bearer already on the request). `error.code` is validated as integer 131070 before mutating Redis or status.
- No new secret. Operators cannot inject Redis state through this path except by Meta’s error.
- Logs stay free of URN paths (Principle III).

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| AC-01 | Extend `TestSendWACThreadOwner`: flag on, `not_owner`, plain text → Graph hits 0, `MsgFailed`, status `FailedReason()=="H"`. Backend test: `WriteMsgStatus` `F`+`H` leaves `failed_reason='H'` and does not bump `error_count` / `next_attempt`. |
| AC-02 | Same suite: `not_owner` + metadata templating category MARKETING, UTILITY, AUTHENTICATION (one case each) → Graph hit, `MsgWired`. DirectSend true → Graph hit. |
| AC-03 | Keep existing flag-off, missing-key, owner, Redis-closed cases. Add: flag off + Graph body `{"error":{"code":131070}}` → status `E` (or today’s wired-unset / errored), Redis state unchanged, no `failed_reason`. |
| AC-04 | Flag on, Redis `owner` or missing, Graph 400 with `error.code` 131070 → Graph called once, status `F`+`H`, Redis `not_owner`, log `reason=131070`. Unparseable body stays today’s errored path. Other `error.code` (e.g. 131047) stays `E`. |
| AC-05 | `backend_test.go`: UPDATE with empty reason does not null out an existing `H`; UPDATE with `H` persists. |
| AC-06 | Assert log events and `assertNoWACOwnershipPII`. |
| AC-07 | Do not add WCD cases; existing `SendTestCasesWAC` stay with flag default false. |

**Flow coverage**: `SendMsg` → ownership gate → Graph `httptest` → `WriteMsgStatus` (backend tests for SQL). Template allow and 131070 are full send flows, not Lua-only.

**Failure paths covered**: Redis read error (send); Redis write error after 131070 (still `F`); Graph 131070; other Graph error; flag off; missing key.

**Stubbed at the boundary**: Graph `httptest`; `MockBackend` Redis; RapidPro backend tests use local Postgres.

## Rollout and Rollback

1. RapidPro/Flows: add `failed_reason` choice `H` (“Outro atendimento está com a conversa”) if not already present. Confirm the column exists in production.
2. Deploy Courier with `EnableWACSendOwnershipBlock` still false. 131070 and local block stay off. `UPDATE` is compatible (writes `failed_reason` only when set).
3. Enable the flag in staging, then production. Watch `wac_send_blocked` (`not_owner` vs `131070`) and confirm marketing templates still wire.

**Rollback**: set the flag false, or redeploy the previous tag. Previous code ignores `failed_reason` on status structs. Redis `not_owner` from 131070 expires in 24 h and is ignored when the flag is off.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| II. Secrets and Supply Chain | No new secret | none |
| III. Structured, Privacy-Safe Observability | Logs: `event`, `channel_uuid`, `msg_uuid`, `reason`, `source`. No URN/text | none |
| IV. Versioned Contracts | Minor, default-off semantic expansion; optional `FailedReason` | none |
| V. Specification Traceability | Opens with Origin Without Product Spec | none |
| VIII. Release Changelog | `WENI-CHANGELOG.md` entry for template exception, 131070, `failed_reason` | none |
| IX. Never Trust the Client | 131070 only from Graph after Courier’s authenticated POST; integer code check | none |
| X. Fail Gracefully | Redis read fail-open; 131070 write failure still marks `F`; no panics | none |
| XI. Bounded Retry Over REST | 131070 is a 4xx business reject → no Graph retry; other errors unchanged | none |
| XII. Stateless, Peak-Sized Services | State in Redis/Postgres; peak declared | Per-route outbound peak unavailable; reuse service-wide series |
| XIII. Diagnosable Errors | Sentry: `channel_uuid`, `msg_uuid`, `source` | No contact UUID on send-path Redis errors (same as 005) |
| XIV. Tests Exercise Flows | Send flows in Test Strategy | none |
| XV. Explicit Over Clever | Named constants: `131070`, `FailedReasonOtherOwner`, allowed categories, log reasons | none |
| XVI. Contained Changes | WAC send + status SQL + changelog. No inbound/standby, no WCD | none |

## Implementation Decisions

- **Reuse `EnableWACSendOwnershipBlock`** for local skip and 131070. One operator switch.
- **`failed_reason` code `H`**: unused in stock RapidPro (`S/C/L/E/O/D`). Flows registers the same letter and the Portuguese label. If Flows lands on a different free letter before implement, change only the named constant.
- **Allowed templates are category-based**, not URL-based (`/marketing_messages` is only MARKETING). UTILITY and AUTHENTICATION still POST `/messages` and must be exempted.
- **DirectSend counts as allowed**: it is already a utility template send (`getWACDirectSend`).
- **Ownership check stays WAC-only** inside `sendCloudAPIWhatsappMsg`.
- **131070 uses Lua `set` + `now`**: Graph is a later signal than a stale webhook; `now` should win over an older `owner` `ts` in normal operation. A webhook with a *future* `ts` could still overwrite; that is the existing CAS rule and is accepted.
- **Parse `error` even when HTTP status is non-2xx**, because `MakeHTTPRequest` returns `err` on `RRStatusFailure`.
- **Do not clear `failed_reason` on later Wired/Delivered** in this change (contained). Status updates without a reason leave the column as-is; a successful retry after becoming owner is a new send, not this row.

## Open Questions

None.
