# Engineering Specification: WAC Handover Synthetic Inbound Message

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `[fix/control-passed]`

**Created**: 2026-11-26

**Status**: Draft

**Cycle**: SDD lite (this spec, then implement directly; no plan/tasks).

## Origin Without Product Spec

- Product Spec: none
- Problem: In WAC multipartner, a `field=messaging_handovers` / `type=control_passed` webhook only writes the accumulated context into `wa_conversation_handover` and nothing reaches Mailroom. Mailroom consumes that pending row only when the contact sends an inbound message (`handleBrainRouting` → `consumeHandoverContext` in `core/tasks/handler/worker.go`), so an agent that took control never receives its context if the contact stays silent.
- Acceptance criteria:
  - **AC-01**: Payload A (nested `conversation_context`): upsert of the pending row, then an inbound message with empty text and `metadata.wa_handover=true`.
  - **AC-02**: Payload B (roles only) with a pending row from standby or messages: the empty message is written, with no new upsert.
  - **AC-03**: Payload B with no pending row: no message is written; Redis ownership is updated as today.
  - **AC-04**: The message metadata carries `previous_owner_app_role` (from `previous_owner_role`) and `new_owner_role`, omits empty fields, and never contains `context_text` or `control_passed.metadata`.
  - **AC-05**: Redelivery of the same webhook (same dedup key) repeats neither upsert nor message and answers 200. Two `control_passed` for the same contact with different timestamps produce two messages, each with pending context.
  - **AC-06**: Upsert error deletes the dedup key and returns non-200. `WriteMsg` error logs `wac_handover_msg_write_failed` and returns 200 with the pending row intact. Pending-row query error skips the message and returns 200.
  - **AC-07**: `control_taken`, non-WAC channel (`WCD`), or `control_passed` without sender: no message, no dedup key, no new query. There is no feature flag: the behavior is the default for WAC `control_passed`.
  - **AC-08**: A Redis error in the dedup claim does not block the flow (fail-open) and logs without PII.
  - **AC-09**: No new log line contains `context_text`, contact name, URN, or `control_passed.metadata`.
  - **AC-10**: `control_passed` with neither `value.timestamp` nor `entry.time` writes no dedup key, and two successive handovers for the same contact do not block each other.
  - **AC-11**: The synthetic message is written with an empty `ExternalID`. The message captured by `MockBackend` has `ExternalID() == ""`, and `queueMsgHandling` accepts it.

## Scope in This Repository

**This repository implements**:

- In `processMessagingHandover` (`handlers/facebookapp/wa_handover.go`), for channel type `WAC`, always (no new flag): Redis dedup claim, optional upsert, pending-context check, and the synthetic inbound `WriteMsg`.
- New method `Backend.HasPendingWAConversationHandover(ctx, channelUUID, urn) (bool, error)` in the `courier.Backend` interface, the rapidpro backend, and `MockBackend`.
- Real-payload fixtures (A and B from the request) and flow, unit and backend tests.

**Out of scope here** (follow-ups, owned by Mailroom / Flows):

- Any Mailroom or Flows change. Recorded follow-ups:
  - hide the empty message (archiving does not remove it from the contact history, which only excludes `VISIBILITY_DELETED`);
  - show "Control passed" in the history by reading `metadata.wa_handover` in Flows `Event.from_msg`;
  - send `handover_metadata` (escalation reason) to Nexus, which `consumeHandoverContext` does not read today;
  - Mailroom guard: a `wa_handover` message with no pending row must not reach the router.
  - Mailroom: keep `wa_handover` messages out of ticket forwarding, trigger matching and flow-session resume (see Rollout and Rollback).
- Changing the `wa_conversation_handover` schema or the Meta contract.
- The existing INFO log `wa conversation handover received` (logs `contact_urn`, `contact_name`, `sender_phone_number`, `handover_metadata` in clear). Not touched here.
- `control_taken`, `WCD` (shares the cloud flow), on-premises `WA`.

**Depends on**:

- Mailroom as deployed: with a pending row and an empty-text `msg_event` it consumes the row and sends `"Context: <context_text>"` to Nexus. With no pending row it returns `event.Text` (empty) to the router, which is why Courier writes the message only when a pending row exists.
- Specs `005`, `006`, `007` in this tree: `applyWACThreadOwner`, `persistRenderedWAHandover`, standby/messages accumulation into the pending row.

## Current State

- `handlers/facebookapp/wa_handover.go` `processMessagingHandover` (line 387): `control_taken` only calls `applyWACThreadOwner` (Redis) and returns `control_taken recorded`. `control_passed` calls `applyWACThreadOwner` (WAC only), then `renderWAHandoverContextText`; without renderable context it returns `control_passed without conversation context` and writes nothing, even if standby/messages already accumulated context. With context it calls `persistRenderedWAHandover` → `Backend.WriteWAConversationHandover`.
- `resolveWAHandoverSenderURN` accepts `wa_id`, `user_id` or `phone_number`. `resolveControlPassedOwner` accepts nested `previous_owner` or loose roles; loose `previous_owner_role` becomes `AppRole`. `parseWAHandoverOccurredOn` uses `value.timestamp`, falling back to `entry.time` (epoch 0 when both are missing).
- `backends/rapidpro/wa_conversation_handover.go`: upsert with `ON CONFLICT (channel_id, contact_id) WHERE consumed_on IS NULL`, concatenating `context_text` (cap 32000 runes). On DB error it spools and returns the spool result. No read query exists for the pending row.
- Inbound WAC messages: `h.Backend().NewIncomingMsg(...).WithReceivedOn(...)` then `WriteMsg` (`handlers/facebookapp/facebookapp.go`, ~1352–1468). Other inbound paths already write without `ExternalID` (~2041). `queueMsgHandling` (`backends/rapidpro/task.go`) enqueues `msg_event` with `text`, `metadata`, `msg_external_id`.
- Backend dedup: `NewIncomingMsg` (`backends/rapidpro/backend.go:218`) calls `checkMsgSeen`; fingerprint = channel + URN, text compared inside a 2–4 s window. With empty text it cannot tell two handovers apart. When it matches, `WriteMsg` becomes a no-op (`alreadyWritten`).
- Redis dedup precedent: `claimWACStandbySeen` / `deleteWACStandbySeen` in `handlers/facebookapp/wa_standby.go`: `SET key 1 NX EX 86400`, key `wac:standby:seen:{channel_uuid}:{message_id}`.
- Existing flags in `config.go` (`EnableWACStandbyContext`, `EnableWACSendOwnershipBlock`) gate standby accumulation and the send block. Neither gates `control_passed` handling, and this change adds no flag.
- Fixtures with nested owner: `handlers/facebookapp/testdata/wac/handoverSummaryWAC.json`, `handoverNoContextWAC.json`. Tests: `TestReceiveWACHandover*` in `facebookapp_test.go` (~480–620), `assertNoWACOwnershipPII` in `wa_thread_owner_test.go`, backend suite in `backends/rapidpro/wa_conversation_handover_test.go`.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `handlers/facebookapp/wa_handover.go` | modified: WAC branch in the `control_passed` path | AC-01…AC-10 |
| `handlers/facebookapp/wa_handover_message.go` (new) | new: dedup key claim/release, metadata builder, synthetic msg writer, log helpers, named constants | keeps the change out of the 500-line file; Principle XV |
| `backend.go` | modified: `HasPendingWAConversationHandover` in `Backend` | AC-02, AC-03 |
| `backends/rapidpro/wa_conversation_handover.go` | modified: read query | AC-02, AC-03 |
| `test.go` (`MockBackend`) | modified: `HasPendingWAConversationHandover`, pending-row state (set by `WriteWAConversationHandover` when `ContextText != ""`, plus `SeedPendingWAConversationHandover` and `ConsumePendingWAConversationHandover` to simulate Mailroom), `SetErrorOnWAPendingCheck`, `PendingCheckCalls()` | AC-02, AC-06, AC-07 |
| `handlers/facebookapp/testdata/wac/` | new fixtures: payload A, payload B, and no-timestamp variants | real-payload tests |
| `handlers/facebookapp/facebookapp_test.go`, `wa_handover_message_test.go`, `wa_thread_owner_test.go`, `backends/rapidpro/wa_conversation_handover_test.go` | modified/new | Principle XIV |
| `WENI-CHANGELOG.md` | modified | Principle VIII |

### Flow

`processMessagingHandover`, `control_passed`, resolved sender URN. Everything up to and including `applyWACThreadOwner` is unchanged. Then:

- **Gate**: `channel.ChannelType()=="WAC"`. No flag. Other channel types (`WCD`) keep the current code path untouched (no Redis, no query, no `WriteMsg`).
- Let `ts = occurredOn.Unix()` (`value.timestamp`, fallback `entry.time`).

1. **Claim dedup** (only when `ts != 0`), through the package var `wacHandoverClaimSeen` (default `claimWACHandoverSeen(pool, channelUUID, urn, ts) (claimed bool, err error)`; a var so tests can inject a Redis error at the claim only): `SET wac:handover:seen:{channel_uuid}:{urn.Identity()}:{ts} 1 NX EX 86400`, connection released before step 2.
   - Key exists → log INFO `wac_handover_duplicate`, return 200 (`wa conversation handover already processed`). No upsert, no message.
   - Redis error → log WARN `wac_handover_dedup_failed`, continue without dedup (fail-open).
   - `ts == 0` → log WARN `wac_handover_dedup_skipped`, continue (no key).
2. **Upsert** when `renderWAHandoverContextText` is ok (payload A): existing `persistRenderedWAHandover`. Error → delete the key (own connection, best effort), return the error (non-200, Meta redelivers). The existing INFO `wa conversation handover received` line stays as is.
3. **Pending check**: `Backend.HasPendingWAConversationHandover(ctx, channel.UUID(), urn)`. Error → log ERROR `wac_handover_pending_check_failed`, skip the message, return 200. `false` → no message; return the current info string (`control_passed without conversation context` for B, `wa conversation handover persisted` for A when the row somehow is not visible, e.g. spooled).
4. **Write synthetic message**: `NewIncomingMsg(channel, urn, "")`, `WithReceivedOn(occurredOn)` (see Implementation Decisions for `ts == 0`), **no `WithExternalID`**, `WithMetadata(...)`, then `WriteMsg`.
   - Metadata: `{"wa_handover": true}` plus `previous_owner_app_role`, `previous_owner_app_id`, `previous_owner_business_id`, `new_owner_role`, taken from `resolveControlPassedOwner` and omitted when empty. `context_text` and `control_passed.metadata` are never copied.
   - `WriteMsg` error → log ERROR `wac_handover_msg_write_failed`; return 200; pending row and dedup key untouched.
   - Success → INFO `wac_handover_msg_written`; return `wa conversation handover persisted` for payload A (unchanged string) and `wa conversation handover message written` for payload B (which previously returned `control_passed without conversation context`).

The rapidpro backend then runs its normal inbound path (`writeMsgToDB`, `queueMsgHandling`), and Mailroom consumes the pending row and calls Nexus.

**Backend query** (`hasPendingWAConversationHandover`, timeout `backendTimeout`):

```sql
SELECT EXISTS (
  SELECT 1
    FROM wa_conversation_handover h
    JOIN channels_channel ch ON ch.id = h.channel_id
    JOIN contacts_contacturn u ON u.contact_id = h.contact_id AND u.org_id = h.org_id
   WHERE ch.uuid = $1 AND u.identity = $2
     AND h.consumed_on IS NULL AND h.context_text <> ''
)
```

It resolves the contact through the URN without creating anything (read-only).

### Rejected Alternatives

- **New `ChannelEvent` type for the handover**: rejected for this phase; needs Mailroom and Flows changes.
- **Context text in the message metadata**: rejected; Mailroom reads `context_text` from the pending row (already accumulated and truncated), and metadata would copy conversation text into `msgs_msg`.
- **Invented `ExternalID`** (to make `checkMsgSeen`/dedup work): rejected; Mailroom reuses it as `ResponseToExternalID` and `wacQuotedMessageID` would quote a non-existent `wamid`.
- **Always write the message, with or without pending context**: rejected; today's Mailroom returns `event.Text` (empty) to the router when no row exists.
- **Dedup through the backend `checkMsgSeen`**: rejected; empty text makes the fingerprint useless and the window is 2–4 s.
- **Feature flag (`COURIER_ENABLE_WAC_HANDOVER_MESSAGE`, default off)**: dropped by decision of the team. The behavior is the default for WAC `control_passed`. The existing flags do not gate this path, so there is no config kill switch (see Rollout and Rollback).
- **Fail the webhook when `WriteMsg` fails**: rejected; Meta would redeliver and the upsert would append the same context twice. The pending row survives and the next inbound consumes it.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| WAC `control_passed` webhook handling | modified: now also writes an inbound message and a dedup key, always on, no flag | minor | additive (no field, route or schema removed); the observable change is the new inbound shape below, announced in `WENI-CHANGELOG.md` |
| `courier.Backend.HasPendingWAConversationHandover` | added | minor | yes for this repo's two implementations (rapidpro, `MockBackend`); no external implementers |
| `msg_event` task / `msgs_msg` row | new kind of inbound: empty `text`, no `external_id`, `metadata.wa_handover=true` plus owner roles/ids | minor | yes; Mailroom already handles it (no schema change; `external_id` is nullable). Emitted for every WAC `control_passed` that has pending context |
| Redis `wac:handover:seen:*` | added, internal | none | yes; older Courier ignores it |
| `wa_conversation_handover` | read only | none | yes |

**Consumers affected**: Mailroom receives the new inbound shape and sends `"Context: …"` to Nexus. Flows shows an empty inbound message in the history (accepted for this phase; follow-ups above). No new env var for operators.

## Data Model and Migrations

- **Redis** `wac:handover:seen:{channel_uuid}:{urn.Identity()}:{ts}`: value `"1"`, TTL 86400 s (named constants, same TTL as the standby key).
- **Postgres**: no schema change. One new read on `wa_conversation_handover`/`contacts_contacturn`/`channels_channel`. One new `msgs_msg` row per handover with context.
- **Migrations**: none. Rollback to the previous version leaves already written messages in place and no schema dependency.

## Non-Functional Requirements

### Peak Load

Same measured Courier inbound peak as specs 005–007, stated as a peak: **242.9 req/s** (1-minute rate, 2026-10-28 14:06 UTC; Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}`, 2026-10-07 to 2026-11-06). Istio has no path label, so `/c/wac/receive` cannot be isolated.

- **Added work per WAC `control_passed`, worst case** (every request at that peak is a `control_passed`, far above reality because handovers are rare): **242.9** `SET NX`/s, up to **242.9** upserts/s, **242.9** indexed pending-row reads/s, **242.9** inbound `WriteMsg`/s. A webhook with several changes repeats this per change. Real handover volume is a small fraction of that, and the existing `wa conversation handover received` path already does the upsert.
- **Other webhooks** (`messages`, `standby`, `control_taken`, non-WAC channels): no new Redis command, query, or write.
- No new process memory that outlives the request, no new local-disk state (the spool path is the existing one).

### Observability

All logs are structured `logrus` fields. Only `event`, `channel_uuid` and, when a message exists, `msg_uuid` are logged. Never `context_text`, contact name, URN, phone, or `control_passed.metadata`.

| Event | Level | Fields |
|-------|-------|--------|
| `wac_handover_duplicate` | INFO | `event`, `channel_uuid` |
| `wac_handover_dedup_failed` | WARN | `event`, `channel_uuid`, `error` (Redis error, no key value) |
| `wac_handover_dedup_skipped` | WARN | `event`, `channel_uuid`, `reason=no_timestamp` |
| `wac_handover_pending_check_failed` | ERROR | `event`, `channel_uuid`, `error` |
| `wac_handover_msg_write_failed` | ERROR | `event`, `channel_uuid`, `msg_uuid`, `error` |
| `wac_handover_msg_written` | INFO | `event`, `channel_uuid`, `msg_uuid` |

Correlation: `channel_uuid` + `msg_uuid` (the message has no external ID by design; the `wa_handover` metadata marker identifies it in `msgs_msg`). The `error` field carries only the Go error from Redis/Postgres/queue, which contains no URN or text. ERROR lines reach Sentry with `channel_uuid` and `msg_uuid`; no contact UUID exists in this handler before the write (same exception as spec 008).

### Failure Handling

- **Redis claim**: explicit pool, one attempt. Error → fail-open (process without dedup, log). Duplicate delivery is then only limited by the pending-row check and the backend 2–4 s window.
- **Upsert** (`WriteWAConversationHandover`, `backendTimeout` = 20 s, spool fallback on DB error): the Redis connection is not held during the call (spec 007 rule). Error (spool also failed) → key deleted, handler returns the error, webhook non-200, Meta redelivers (bounded by Meta). A spooled upsert returns nil and the pending check may not see the row yet; then no message is written and the context waits for the next inbound or the spool flush. Accepted.
- **Pending check**: `backendTimeout`. Error → skip message, 200; row stays pending.
- **`WriteMsg`**: `backendTimeout`; the backend spools on DB failure. Error → log, 200, no retry here: redelivery would re-append context, so the operation is deliberately not retried (Principle XI applies to REST propagation; this is Postgres + queue behind the backend, already spooled).
- **Idempotency**: dedup key per (channel, URN, timestamp), set before the upsert, deleted only when the upsert fails. Key present = redelivery. Two different timestamps for the same contact are two handovers.
- **Known limit**: the backend's `checkMsgSeen` (empty text, same channel+URN, within 2–4 s of a previous empty message) makes the second synthetic `WriteMsg` a no-op. The second handover's context was already upserted into the pending row, so it is delivered with the next consumption. Accepted.

### Security and Authorization

- Trust boundary: the WAC webhook body, authenticated by the existing `validateSignature` check (`X-Hub-Signature`) in the WAC receive handler before `processMessagingHandover`. Sender, timestamp, roles and context are validated as today (`resolveWAHandoverSenderURN`, `parseWAHandoverOccurredOn`; invalid values return 400).
- The synthetic message is written only for a URN that has a pending row in this channel (server-side lookup), so a webhook cannot create inbound traffic for an unrelated contact.
- Free-text Meta fields (`control_passed.metadata`, `conversation_context`) never enter the message metadata, only role/id strings.
- No new secret and no new configuration.

## Test Strategy

| Acceptance criterion | How this repository proves it |
|----------------------|-------------------------------|
| AC-01 | Flow test with payload A fixture: `mb.WAHandoverEvents()` has 1 event, then 1 queued msg with `Text()==""`, metadata `wa_handover=true`; `BackendCalls()` order is `wa_handover`, `msg`. |
| AC-02 | Payload B fixture; pending row seeded with `SeedPendingWAConversationHandover`, and also created by a prior standby/messages request: 1 queued msg, 0 new handover events. After `ConsumePendingWAConversationHandover`, the same fixture with a new timestamp writes no message. |
| AC-03 | Payload B, no pending: 0 queued msgs, response `control_passed without conversation context`, Redis ownership hash updated (`threadOwnerHash`). |
| AC-04 | Unit test on the metadata builder plus assertion on the captured msg: `previous_owner_app_role="ai_agent"`/`"marketing"`, `new_owner_role="escalation"`, empty fields absent, no `context_text`, no `Cliente solicitou…` text. |
| AC-05 | Post the same fixture twice: 1 upsert, 1 msg, both 200. Post it with two different `timestamp` values: 2 msgs. |
| AC-06 | `SetErrorOnWAHandover(true)`: non-200 and the dedup key is absent in Redis. `SetErrorOnQueue(true)` (`WriteMsg`): 200, log `wac_handover_msg_write_failed`, handover event kept. Pending-check error injection: 200, 0 msgs, log `wac_handover_pending_check_failed`. |
| AC-07 | Table test: `control_taken` (`handoverTakenWAC.json`), `WCD` channel, `handoverNoSenderWAC.json`: 0 msgs, no `wac:handover:seen:*` key, `HasPending…` call counter stays 0. |
| AC-08 | Replace the package var `wacHandoverClaimSeen` (the claim seam, restored with `t.Cleanup`; the package tests do not use `t.Parallel`) with a function that returns an error: flow still completes with 1 upsert and 1 msg, log `wac_handover_dedup_failed`, `assertNoWACOwnershipPII` passes. Closing the Redis pool is not usable here: `applyWACThreadOwner` runs earlier and already fails the webhook with 400 (see "closed redis pool fails the webhook" in `facebookapp_test.go`). |
| AC-09 | `assertNoWACOwnershipPII` extended with the six new events; also assert no entry value contains `context_text`, the summary text, the contact name, or `control_passed.metadata`. |
| AC-10 | Fixtures without `timestamp` and `entry.time`: no `wac:handover:seen:*` key, log `wac_handover_dedup_skipped`, two successive posts produce two msgs. |
| AC-11 | Captured `mockMsg.ExternalID()==""`. Backend test next to `TestWriteMsg` (`backends/rapidpro/backend_test.go`, ~1535): `NewIncomingMsg` with empty text and no `WithExternalID`, then `WriteMsg`, writes the row with NULL `external_id` and queues `msg_event` with `msg_external_id:""` and the `wa_handover` metadata. |

Backend suite (`backends/rapidpro/wa_conversation_handover_test.go`) for `HasPendingWAConversationHandover`: pending with text → true; pending with empty `context_text` → false; consumed row → false; other channel or other URN → false; no contact → false (and no contact is created).

Existing tests changed by the new default: the `control_passed` cases in `TestReceiveWACHandover*` (`facebookapp_test.go`, ~480–620) now also see a queued message and a dedup key; the "Control passed without context" case with `repost: true` now gets `wa conversation handover already processed` on the second post. They are updated, not deleted.

**Flow coverage**: webhook (signed) → `processMessagingHandover` → upsert → pending check → `WriteMsg` → queued msg, for payloads A and B; duplicate delivery and two distinct handovers end to end.

**Not covered by tests**: the 2–4 s `checkMsgSeen` collapse of two empty messages (`MockBackend` does not apply it; a rapidpro backend test can assert the second `WriteMsg` is a no-op if wanted) and the spooled-upsert case (pending check returns false, no message).

**Failure paths covered**: invalid timestamp is covered by existing tests; new ones cover upsert error, `WriteMsg` error, pending-check error, Redis error, missing timestamp, duplicate delivery.

**Stubbed at the boundary**: `MockBackend` (real Redis from `NewMockBackend`, in-memory handover list and queued messages); the rapidpro suite runs against local Postgres/Redis.

## Rollout and Rollback

There is no flag: the behavior is active as soon as the binary is deployed.

1. Deploy to staging with a WAC multipartner channel; send payloads A and B and confirm Nexus receives `Context: …` and that the `wa_handover` message shows empty in the history.
2. Promote the same tag to production through the normal release (`WENI-CHANGELOG.md` `1.81.0`).

Known accepted effects: a window exists between the pending check and Mailroom consuming the row (Mailroom could consume the row via another inbound first, and the synthetic message would then reach the router empty); the first agent reply carries no quote because the message has no `ExternalID`; the empty message is visible in the history.

**Known accepted effect: Mailroom does not always reach the brain with this message.** In `handleMsgEvent` (`core/tasks/handler/worker.go`) the empty message goes through other steps before `handleBrainRouting`, and the pending row is consumed only there (`BrainOn`, no open ticket, `shouldRouteToBrain`):

| Contact state in Mailroom | What happens to the empty `wa_handover` message | Context delivered |
|---------------------------|--------------------------------------------------|-------------------|
| Open ticket | `ForwardIncoming` sends the empty message to the ticket, and the brain is skipped | no, row stays pending |
| Matching trigger (e.g. catch-all) | `FindMatchingMsgTrigger(event.Text)` runs with empty text; the trigger flow may start | no, unless the brain runs later |
| Active flow session | `ResumeFlow` resumes the session with an empty message | no, row stays pending |
| `BrainOn` off, or `disable_ai_response` | handled as inbox | no |
| None of the above | `consumeHandoverContext` → `"Context: <context_text>"` to Nexus | yes |

This is accepted in this phase because the team chose no Mailroom change. In the not-delivered cases the row stays pending and is consumed by the next real inbound, as today. It has more reach now that there is no flag. Added to the Mailroom follow-ups: ignore messages with `metadata.wa_handover` for ticket forwarding, trigger matching and session resume, and route them straight to the brain only when a pending row exists.

**Rollback**: redeploy the previous tag (there is no config kill switch). Written messages stay; unconsumed pending rows are consumed by the next inbound as before. Redis dedup keys expire in 24 h.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| II. Secrets and Supply Chain | No new secret or dependency | none |
| III. Structured, Privacy-Safe Observability | Six structured events with `channel_uuid`/`msg_uuid` only; no URN, name, text, or Meta `metadata` | The pre-existing INFO `wa conversation handover received` logs URN/name in clear; it is out of scope (Principle XVI) and listed as out of scope |
| IV. Versioned Contracts | Interface method and message shape are additive and minor; no flag, so the new inbound is always emitted for WAC `control_passed` with pending context | Not default-off. Justified: additive, no schema or route change, announced in the changelog; rollback is a redeploy |
| V. Specification Traceability | Opens with `## Origin Without Product Spec` | none |
| VII. Conventional Commits | `fix:`/`feat:`/`test:` atomic commits | none |
| VIII. Release Changelog | new `1.81.0` entry in `WENI-CHANGELOG.md` (`1.80.0` is already tagged; new inbound behavior = minor) | none |
| IX. Never Trust the Client | Signature check precedes processing; URN and timestamp validated; free-text Meta fields not copied; message only for URNs with a pending row | none |
| X. Fail Gracefully | Explicit timeouts via `backendTimeout`; Redis fail-open; `WriteMsg` failure does not fail the webhook | none |
| XI. Bounded Retry Over REST | No REST propagation added; Meta redelivery on upsert error is bounded by Meta and idempotent via the dedup key | none |
| XII. Stateless, Peak-Sized Services | State in Redis/Postgres; peak declared | Per-route peak unavailable; service-wide series reused |
| XIII. Diagnosable Errors | ERROR logs carry `channel_uuid`, `msg_uuid` | No contact UUID before the write (same as spec 008) |
| XIV. Tests Exercise Flows | Flow tests for A and B plus failure paths | none |
| XV. Explicit Over Clever | Named constants for key prefix, TTL, metadata keys, event names | none |
| XVI. Contained Changes | Only the `control_passed` WAC path, the new query. No Mailroom/Flows/schema change | none |

## Implementation Decisions

- **Order: claim → upsert → pending check → message.** Claiming first keeps a redelivery from appending context twice; releasing it on upsert error lets Meta redeliver.
- **Fail-open on dedup and on `WriteMsg`, fail-closed on the upsert**: the upsert is the only step whose loss drops information, and the webhook can be safely redelivered before it succeeds.
- **No `ExternalID`**: see Rejected Alternatives. Traceability is the `wa_handover` marker and `channel_uuid`/`msg_uuid` logs.
- **Effective timestamp 0 → no dedup key**: protection is only the pending check. A missing timestamp must never block a legitimate later handover.
- **`ReceivedOn` when `ts == 0`**: leave the `NewIncomingMsg` default (now) instead of calling `WithReceivedOn(epoch 0)`, to avoid a 1970 `sent_on` in the history. Otherwise `ReceivedOn = occurredOn`.
- **Pending check keyed by channel UUID + URN identity**, not by `contact_urn` column text, because the column stores `URN.String()` (which can carry auth/query) while `contacts_contacturn.identity` is what the upsert resolves through.
- **No feature flag**: decided by the team; the behavior is the default for WAC `control_passed`. Consequence: the rollout safety net is staging plus redeploy, not a config switch.
- **Response strings**: payload A keeps `wa conversation handover persisted` whether or not a message was written; payload B with a written message returns `wa conversation handover message written`; a duplicate returns `wa conversation handover already processed`. Payload B without pending context keeps `control_passed without conversation context`.
- **Payload A with no pending row visible after upsert** (spool case) returns the existing `wa conversation handover persisted`.
- **Known limit of the backend 2–4 s `checkMsgSeen` window** is accepted rather than worked around (would need an `ExternalID` or a backend change).

## Open Questions

None.
