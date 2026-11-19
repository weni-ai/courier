# Engineering Specification: WAC Thread Ownership

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `[005-wac-thread-ownership]`

**Created**: 2026-11-12

**Status**: Draft

## Origin Without Product Spec

- Product Spec: none
- Problem: Courier does not track whether it currently owns a WhatsApp Cloud (`WAC`) multipartner thread. `control_passed` without a renderable summary or history is discarded with no side effect, and `control_taken` only logs `ignored_type`, so a later send cannot know that another responder holds the thread.
- Acceptance criteria:
  - **AC-01**: Two global `COURIER_*` bool flags exist, default **false**: standby-context recording and send blocking by thread ownership. Ownership Redis writes run whether the flags are on or off.
  - **AC-02**: Redis stores, per WAC channel UUID and contact URN identity, state `owner` or `not_owner` with TTL **24 hours**, plus the Unix timestamp of the last **applied** event.
  - **AC-03**: Timestamp compare and state write happen in one Redis Lua script. An incoming timestamp **less than or equal to** the stored timestamp is ignored (`stale`). A missing handover `value.timestamp` falls back to `entry.time` (including `0`).
  - **AC-04**: `control_passed` always attempts to write `owner`. When conversation context renders to summary or history, `WriteWAConversationHandover` still runs. When it does not render, Courier answers HTTP 200 and writes **no** `wa_conversation_handover` row.
  - **AC-05**: `control_taken` writes `not_owner`, answers HTTP 200, queues **no** inbound message, writes **no** channel event, and does **not** call `WriteWAConversationHandover`.
  - **AC-06**: On `field=messages`, Courier writes `owner` only when the Redis key already exists. A message for a contact with no key creates no key (phone numbers without conversation routing stay unmarked).
  - **AC-07**: `EnableWACStandbyContext` gates persistence of conversation context from `field=standby` into `wa_conversation_handover`. `EnableWACSendOwnershipBlock` gates the send-path ownership check. Neither flag disables Redis ownership updates.
  - **AC-08**: With `EnableWACSendOwnershipBlock=false`, WAC sends are unchanged. With the flag **true**, a `not_owner` key skips the Graph call and marks the message `MsgFailed`. A missing key or a Redis **read** error sends exactly as today.

## Scope in This Repository

**This repository implements**:
- Redis thread-ownership state for channel type `WAC`, updated from `messaging_handovers` (`control_passed`, `control_taken`) and from inbound `field=messages`.
- Two global config flags, default off, and the send-path fail-open check behind the send flag.
- Standby-field conversation-context persistence behind the standby flag, reusing `WriteWAConversationHandover`.
- Flow and unit tests for timestamp CAS, missing/equal timestamps, handover without context, `control_taken`, unmarked common messages, and send blocking.

**Out of scope here**:
- Calling Meta's Thread Control API (`pass` / `release` / `take`) or the Routing Roles API.
- Ingesting `field=standby` message copies as inbound `Msg` (copies stay ignored; only renderable `conversation_context` is persisted when the standby flag is on).
- Channel types other than `WAC` (`WCD`, `FBA`, `IG`, on-premises `WA`).
- Per-channel or per-org flags (the flags are process-global).
- Schema changes to `wa_conversation_handover`.
- Special-casing the escalation-partner implicit take (a Service send that takes the thread). When the send flag is on and the key is `not_owner`, Courier does not send.

**Depends on**:
- **Meta Cloud API**: `messaging_handovers` and `messages` webhooks as documented in Thread Control, including `timestamp`, `sender`, `control_passed`, `control_taken`, and optional `conversation_context`. There is no ownership-status API; Courier derives state from these webhooks.
- **No other Weni repository** for the Redis key. Mailroom and RapidPro keep consuming `wa_conversation_handover` as they do today. They do not read the Redis ownership key.

## Current State

- `handlers/facebookapp/facebookapp.go` `processCloudWhatsAppPayload` (from line 1123):
  - `field=standby` logs `ignoring standby webhook` and continues (lines 1154–1157).
  - `field=messaging_handovers` calls `processMessagingHandover` (lines 1160–1182).
  - `field=messages` walks `change.Value.Messages` (from line 1205), writes inbound msgs, and never touches Redis ownership.
- `handlers/facebookapp/wa_handover.go` `processMessagingHandover` (line 335):
  - Parses `value.timestamp` with fallback to `entry.time` via `parseWAHandoverOccurredOn` (line 222).
  - Any type other than `control_passed` logs `ignored_type` and returns 200 (`ignoring handover type %s`).
  - `control_passed` without renderable context logs `skipped_no_context` and returns 200 **before** resolving the sender URN (lines 352–356). That is the path that currently drops ownership.
  - With context, it resolves the sender (`wa_id` then `user_id` then `phone_number`), writes `WAConversationHandoverEvent`, and calls `Backend.WriteWAConversationHandover`.
- `moPayload` (`facebookapp.go` ~285) already has `Sender`, `Timestamp`, `Type`, `ControlPassed`, `ConversationContext`. It has **no** `control_taken` object (not required to set ownership; `value.type` is enough).
- Outbound WAC send is `sendCloudAPIWhatsappMsg` (`facebookapp.go` line 2882), shared with `WCD`. It has no ownership check. Graph errors such as `#131070` are handled only after the HTTP call.
- Redis is already the shared store: `Backend.RedisPool()` (`backend.go` line 134), Lua scripts in `backends/rapidpro/msg.go` (`luaMsgSeen`, `luaWriteExternalIDSeen`) and `backends/rapidpro/backend.go` (`luaSent`). The WAC media cache in `facebookapp.go` (~4763) uses `h.Backend().RedisPool()` from the handler — the pattern this feature follows, without a new `Backend` method.
- Config flags today: `EnableRabbitMQBilling` / `EnableAmazonmqBilling` in `config.go` (bool, `ezconf` → `COURIER_*`). No WAC conversation-routing flags.
- Tests: `TestReceiveWACHandover` in `facebookapp_test.go` (line 476) covers summary, history, no-context (`handoverNoContextWAC.json` already has `sender.wa_id`), and nested summary. `wa_handover_test.go` covers context rendering. `MockBackend.RedisPool()` points at local Redis. Send coverage is `RunChannelSendTestCases` / `SendTestCasesWAC`. There is no `control_taken` fixture.
- `wa_conversation_handover` (schema in `backends/rapidpro/schema.sql` line 157, writer in `backends/rapidpro/wa_conversation_handover.go`) upserts pending rows. Empty context is not written today because `renderWAHandoverContextText` fails closed.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `config.go` | modified: add `EnableWACStandbyContext bool` (default false) and `EnableWACSendOwnershipBlock bool` (default false) | Global flags, injected as `COURIER_ENABLE_WAC_STANDBY_CONTEXT` and `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK`. |
| `handlers/facebookapp/wa_thread_owner.go` | **new**: Redis key helper, Lua CAS script, `apply` / `get` wrappers | Keeps atomic compare-and-set out of the 5k-line handler file (Principle XV, XVI). |
| `handlers/facebookapp/wa_handover.go` | modified: `processMessagingHandover` always resolves the sender for `control_passed` and `control_taken`, applies Redis state, then optionally persists context | Ownership must be recorded even when there is no summary. |
| `handlers/facebookapp/facebookapp.go` (`moPayload`, `processCloudWhatsAppPayload`, `sendCloudAPIWhatsappMsg`) | modified: optional `ControlTaken` JSON field; `field=messages` ownership update-if-exists; standby context persist when the flag is on; send-path block when the send flag is on and state is `not_owner` | Integrates the three signals Meta documents (handover, inbound message, send). |
| `handlers/facebookapp/wa_thread_owner_test.go` | **new** | Lua outcomes: created, updated, stale (equal and older), absent, missing timestamp. |
| `handlers/facebookapp/facebookapp_test.go` | modified: handover flow tests, common-message Redis assertions, send-block cases | Principle XIV flow coverage. |
| testdata `handlers/facebookapp/testdata/wac/handoverTakenWAC.json` (and equal/missing-timestamp variants as needed) | **new** | `control_taken` has no fixture today. |
| `WENI-CHANGELOG.md` | modified | MINOR entry for the new settings (Principle VIII). |

### Flow

**Inbound webhook** (`POST /c/wac/receive` → `receiveEvent` → signature check → `processCloudWhatsAppPayload`), channel type `WAC` only:

1. **`field=messaging_handovers`**
   1. Parse occurred-on: `value.timestamp` if present and a valid integer; else `entry.time`. Invalid non-empty timestamp is still a 400 (`parseWAHandoverOccurredOn`).
   2. If `type` is neither `control_passed` nor `control_taken`, keep today's `ignored_type` 200.
   3. Resolve sender URN with `resolveWAHandoverSenderURN`. Missing sender is a 400 (needed to key Redis).
   4. Redis Lua `apply` in mode `set`: `control_passed` → `owner`; `control_taken` → `not_owner`. Incoming Unix seconds vs stored `ts`; write only when incoming `>` stored, or when the key is missing. TTL reset to 24 h only on apply.
   5. Lua / Redis **write** error: return that error so Meta redelivers (handover-only payload, no inbound msg to duplicate).
   6. **`control_passed`**: if `renderWAHandoverContextText` succeeds, `WriteWAConversationHandover` as today (not gated by the standby flag). If it fails, HTTP 200, no row.
   7. **`control_taken`**: HTTP 200, no `WriteMsg`, no `WriteChannelEvent`, no `WriteWAConversationHandover`.

2. **`field=messages`**
   1. Existing URN and timestamp parsing. Invalid timestamp remains 400; no Redis write.
   2. After the primary URN is resolved, Lua `apply` in mode `update_if_exists` with state `owner` and `msg.timestamp`. Outcomes: `updated` (key existed and ts won), `stale`, `absent` (no key — **do not create**).
   3. Redis **write** error: log `ERROR` with `channel_uuid` and `event=wac_thread_owner_update_failed`; **continue** inbound processing so Meta 200 does not turn into a duplicate message.
   4. Existing `WriteMsg` path unchanged.

3. **`field=standby`**
   1. Flag **off** (default): keep today's ignore.
   2. Flag **on**: if conversation context renders, persist via `WriteWAConversationHandover`; if not, 200 with no row. Do **not** create inbound msgs from standby copies. Do **not** write Redis ownership from standby (ownership signals in this spec are handover + `field=messages` only).

**Outbound send** (`SendMsg` → `sendCloudAPIWhatsappMsg`), channel type `WAC` only:

1. If `EnableWACSendOwnershipBlock` is false, send as today (no Redis read).
2. If true, `HGET` `state`. Missing key, empty state, `owner`, or **any read error** → send as today.
3. `not_owner` → do not call Graph; `status.SetStatus(MsgFailed)`; structured log `event=wac_send_blocked reason=not_owner` with `channel_uuid` and message UUID; return that status.

### Redis key and Lua

- **Key**: `wac:thread_owner:{channel_uuid}:{urn.Identity()}`
- **Hash fields**: `state` (`owner` \| `not_owner`), `ts` (Unix seconds, decimal string)
- **TTL**: named constant `wacThreadOwnerTTL = 24 * time.Hour` (86400 s)
- **Modes** (`ARGV`): `set` (handovers, create or update) and `update_if_exists` (common messages)
- **Return values**: `created`, `updated`, `stale`, `absent`
- Compare is `tonumber(incoming) <= tonumber(stored)` → `stale`. Equal timestamps are therefore ignored (Meta redelivery of the same event is a no-op on Redis; `control_passed` with context still attempts `WriteWAConversationHandover` on redelivery).

### Flag interaction

| Flag | Default | When false | When true | Ownership Redis |
|------|---------|------------|-----------|-----------------|
| `EnableWACStandbyContext` | false | `field=standby` ignored; `control_passed` with renderable context still persists (existing 1.68.0 behaviour) | persist renderable context from `field=standby` | always written |
| `EnableWACSendOwnershipBlock` | false | Graph send unchanged | block Graph send only when state is `not_owner` | always written |

Ownership updates are independent of both flags so that turning a flag on later already has state.

### Rejected Alternatives

- **Postgres `wa_conversation_handover` as the ownership source**: the table is a pending context payload for Mailroom, keyed for unconsumed summaries, not a 24 h owner bit, and `control_taken` must not insert a row.
- **Create `owner` on every inbound `field=messages`**: that would mark phone numbers that are not in conversation routing, which AC-06 forbids. Meta already says an inbound message only *likely* means you own the thread.
- **Backend interface methods for get/set**: the state is WAC-handler-specific. Media cache already uses `RedisPool()` from the handler. A new `Backend` method would force `MockBackend` and RapidPro changes for no shared consumer.
- **Fail closed on Redis read at send time**: AC-08 requires preserving today's send when the key is missing or the read fails. A Redis blip must not drop an entire org's WAC traffic.
- **`MsgErrored` on blocked send**: Mailroom would retry, and a later retry after we become owner could deliver a stale Service message (and, if we were the escalation partner, implicitly take the thread). `MsgFailed` is terminal.
- **Gating existing `control_passed` summary persistence behind `EnableWACStandbyContext`**: that would disable the 1.68.0 orchestration write while the flag is off. AC-04 keeps that path.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| Config `COURIER_ENABLE_WAC_STANDBY_CONTEXT` | added, bool, default false | minor | yes (addition; off preserves today) |
| Config `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` | added, bool, default false | minor | yes (addition; off preserves today) |
| Inbound `POST /c/wac/receive` `messaging_handovers` / `control_passed` without context | modified: still HTTP 200; Redis `owner` write added | patch | yes for Meta (200). Response body text may change from `control_passed without conversation context`. |
| Inbound `POST /c/wac/receive` `messaging_handovers` / `control_taken` | modified: still HTTP 200; Redis `not_owner` write added; still no msg | patch | yes for Meta (200). |
| Inbound `POST /c/wac/receive` `field=messages` | modified: optional Redis `owner` update-if-exists; msg write unchanged | patch | yes. |
| Redis key `wac:thread_owner:*` | added, Courier-internal | none | not a public contract under Principle IV. No other service may read it. |
| Outbound Graph `POST /{phone-number-id}/messages` (and marketing_messages) for `WAC` | modified only when send flag is true and state is `not_owner`: call skipped, `MsgFailed` | minor | yes; default off. |

**Consumers affected**: Courier operators (new env vars, default off). Mailroom/RapidPro: no payload change; they still read `wa_conversation_handover` only when a context row is written. Meta: still 200 on the handover and message paths this spec changes.

## Data Model and Migrations

- **Thread ownership (Redis hash)**: ephemeral, not in Postgres.
  - Key: `wac:thread_owner:{channel_uuid}:{urn.Identity()}`
  - `state`: `owner` \| `not_owner`
  - `ts`: last applied event Unix seconds
  - TTL 24 h from last **applied** write, matching Meta's idle timeout after 24 h of WhatsApp-user inactivity. Expired key = no mark = send fail-open.
- **`wa_conversation_handover` (Postgres)**: unchanged schema. Still written only when context renders. `control_taken` and context-less `control_passed` insert nothing.

**Migrations**: none. Previous Courier tags ignore the new Redis keys. Rollback leaves orphan keys that expire in ≤24 h; they are not read by the previous tag.

## Non-Functional Requirements

### Peak Load

- **Courier service-wide inbound**: peak of **242.9 req/s** (1-minute rate) at 2026-10-28 14:06 UTC, from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}` over 2026-10-07 to 2026-11-06 (same series as spec `003-wac-integrations-webhook-auth`). Istio has no path label, so `/c/wac/receive` cannot be isolated.
- **Added cost, inbound**: at most one Redis `EVAL` per WAC `messages` item or `messaging_handovers` change. Worst case, if the service peak were 100% those events: **242.9 EVAL/s**. Script is `HGET` + optional `HSET`/`EXPIRE` on a single key.
- **Added cost, outbound**: one Redis `HGET` per WAC send **only when** `EnableWACSendOwnershipBlock` is true. Default off: zero extra Redis on the send path.
- Redis is the existing shared pool (`MaxActive` 8, `Wait: true`). This feature does not add process memory or local-disk state.

### Observability

Structured `logrus` fields only. **Do not** log URN paths, phone numbers, `sender.phone_number`, message text, or context text (Principle III). Existing `messagingHandoverLogFields` already attach `contact_urn` and sender identifiers; new ownership logs must not add those fields.

| Event | Level | Fields |
|-------|-------|--------|
| `wac_thread_owner_applied` | INFO | `event`, `channel_uuid`, `outcome` (`created`/`updated`/`stale`/`absent`), `state`, `source` (`control_passed`/`control_taken`/`messages`) |
| `wac_thread_owner_update_failed` | ERROR (Sentry) | `event`, `channel_uuid`, `source`, error message without URN |
| `wac_send_blocked` | INFO | `event`, `channel_uuid`, `msg_uuid`, `reason=not_owner` |
| `wac_send_ownership_read_failed` | ERROR (Sentry) | `event`, `channel_uuid`, `msg_uuid` — then the send continues |

**Correlation identifier**: `channel_uuid` on inbound; `channel_uuid` + message UUID on send. No contact UUID is guaranteed on a handover that does not persist a row.

### Failure Handling

- **Redis**: one attempt through the existing pool, same as `luaMsgSeen`. No new retry loop (Principle XI applies to REST between services, not to this store).
- **Inbound handover Lua/Redis error**: return the error; `receiveEvent` surfaces a non-200; Meta redelivers. Idempotent because equal timestamps are `stale` and context upsert is already idempotent.
- **Inbound `field=messages` Lua/Redis error**: log `wac_thread_owner_update_failed`, process the message, HTTP 200. A non-200 would duplicate the inbound msg on redelivery.
- **Send Redis read error or missing key**: send as today (AC-08).
- **Postgres `WriteWAConversationHandover` error**: unchanged (spool fallback in RapidPro backend, then error to Meta).
- **Timeouts**: inbound and send keep using the handler request context and the shared HTTP client for Graph. Redis has no extra command deadline beyond the pool (same as current Lua callers).
- **Idempotency**: Lua CAS on `ts`; Meta redelivery of the same handover reapplies nothing on Redis and retries context persist if needed.

### Security and Authorization

- **Trust boundary**: Meta `POST /c/wac/receive`. `receiveEvent` already verifies `X-Hub-Signature` (unless `X-Router-Token`). Ownership is derived only after that check, from the signed body.
- **Authorization**: no new client credential. Redis is the internal store Courier writes; send-path reads are not a client input.
- **Validation**: `type` must be `control_passed` or `control_taken` to write Redis from handovers; sender URN required; timestamp parse as today; `field=messages` uses the already-resolved primary URN.
- **Secrets**: none added. Flags are not secrets. Redis URL remains `COURIER_REDIS`.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| AC-01 | Config unit/default assertion: both flags false in `NewConfig()`. Flow tests run ownership Redis writes with flags left at default. |
| AC-02 | After `control_passed` / `control_taken` flow tests, `HGETALL` on `wac:thread_owner:{uuid}:{identity}` shows `state` and `ts`, and `TTL` is in (0, 86400]. |
| AC-03 | `wa_thread_owner_test.go`: apply ts=100 then ts=50 → `stale`, state unchanged; apply ts=100 then ts=100 → `stale`; apply with empty timestamp using `entry.time`; apply ts=101 → `updated`. |
| AC-04 | Extend `TestReceiveWACHandover`: no-context fixture still HTTP 200, `WAHandoverEvents()` empty, Redis `owner`. Summary fixture still persists one event **and** Redis `owner`. |
| AC-05 | New fixture `handoverTakenWAC.json`: HTTP 200, `LenQueuedMsgs()==0`, `WAHandoverEvents()` empty, Redis `not_owner`. |
| AC-06 | Receive `helloWAC.json` with empty Redis → still "Handled", key absent. Seed `not_owner` then receive `helloWAC.json` → key `owner`. |
| AC-07 | Standby fixture with context, flag false → no handover row (today). Flag true → one `WriteWAConversationHandover`. Send tests: flag false + Redis `not_owner` still hits the Graph `httptest` server. |
| AC-08 | `RunChannelSendTestCases` / dedicated send tests with `PrepBackend`: flag true + `not_owner` → no Graph request, status `F`. Flag true + no key → Graph called. Flag true + Redis closed/error → Graph called. |

**Flow coverage**: signed WAC webhook into `/c/wac/receive` through `processCloudWhatsAppPayload` / `processMessagingHandover` to Redis and (when applicable) `WriteWAConversationHandover` or `WriteMsg`. Outbound: `sendCloudAPIWhatsappMsg` with the send flag on and off, Graph stubbed via `httptest` as in `SendTestCasesWAC`.

**Failure paths covered**: invalid handover timestamp (existing 400); missing sender on `control_passed`/`control_taken` (400); Redis write error on handover (non-200); Redis write error on messages (200, msg written, log asserted); stale and equal timestamps; send-path Redis read error (send proceeds); `not_owner` block (no Graph).

**Stubbed at the boundary**: Meta Graph via `httptest`; backend via `courier.MockBackend` (its `RedisPool` is real local Redis, flushed per test). No live Cloud API.

## Rollout and Rollback

1. Deploy Courier with both flags **false** (the `NewConfig` defaults). Ownership Redis writes start. Watch `wac_thread_owner_update_failed` and handover 5xx from Meta retries. Confirm `control_passed` with summary still inserts `wa_conversation_handover` and that no-context `control_passed` no longer needs a row.
2. After ownership keys look sane on a routing-enabled WAC number (key present after `control_passed` / `control_taken`), enable `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` in staging, then production. Confirm blocked sends log `wac_send_blocked` and that numbers **without** a key still send.
3. Enable `COURIER_ENABLE_WAC_STANDBY_CONTEXT` only when standby-context persistence is wanted. Independent of step 2.

**Rollback**: redeploy the previous tag, or set both flags false. Previous code ignores the Redis keys. Keys expire in ≤24 h. No Postgres migration to reverse. Turning only the send flag off restores today's Graph sends immediately while leaving ownership writes in place.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| II. Secrets and Supply Chain | No new secret. Flags and Redis URL are existing `COURIER_*` injection. | none |
| III. Structured, Privacy-Safe Observability | New logs use `channel_uuid`, `outcome`, `state`, `source`, message UUID. URN paths and context text are not added. | Pre-existing `messagingHandoverLogFields` still attach `contact_urn`; this change does not extend that. Removing it is a separate Principle XVI change. |
| IV. Versioned Contracts | New flags are a MINOR addition, default off. Webhook status codes stay 200. Redis key is internal. | Response body strings for ignored handovers may change; Meta and operators key off HTTP status. |
| V. Specification Traceability | Opens with `## Origin Without Product Spec` in the mandated format. | none |
| VIII. Release Changelog | `WENI-CHANGELOG.md` MINOR entry listing the flags and ownership behaviour. | none |
| IX. Never Trust the Client | Ownership is taken only from signature-checked Meta payloads. Clients cannot POST a Redis state. | `X-Router-Token` traffic keeps today's signature skip (unchanged, Principle XVI). |
| X. Fail Gracefully and Predictably | Handover Redis errors surface to Meta; message Redis errors do not drop the inbound msg; send read errors fail open. No panics. | none |
| XI. Bounded Retry Over REST | No new REST hop. Graph send is still one `MakeHTTPRequest`. Redis is not retried in a loop. | Message-path Redis write failure is logged and not made redeliverable, because a non-200 would duplicate `WriteMsg`. Handover-path failure is redeliverable. |
| XII. Stateless, Peak-Sized Services | State lives in Redis with TTL. Peak declared above. No new local-disk state. | Per-route peak unavailable (Istio has no path label); service-wide peak reused. |
| XIII. Diagnosable Errors | Sentry fields: `channel_uuid`, `event`, `source` or `msg_uuid`. | No contact UUID on handover-only events that do not persist a row; channel UUID is the locator. URN must not be attached. |
| XIV. Tests Exercise Flows | Webhook → Redis/DB and send → Graph-or-block, plus failure paths in the table above. | none |
| XV. Explicit Over Clever | Named constants for TTL, key prefix, states, Lua outcomes, flag names, log event names. Lua lives in one file with a why-comment on `<=` (redelivery). | none |
| XVI. Contained Changes | WAC handler + config + changelog. No FBA/IG, no Thread Control API client, no `Backend` interface growth, no `shouldRetryTransient` change. | none |

## Implementation Decisions

- **Key uses channel UUID + `urn.Identity()`**, with the same URN the inbound path already prefers (handover: `wa_id` then `user_id` then `phone_number`; messages: the primary `urn`, phone when both phone and BSUID are present). Redis cannot join two identifiers; matching Meta's inbound attribution is the least-wrong local key.
- **Equal timestamps are `stale`**: redelivery of the same webhook must not refresh TTL or flip state. Context persist on `control_passed` still runs so a failed Postgres write can recover.
- **Missing handover timestamp uses `entry.time`**, including `0`, rather than skipping the write. That reuses `parseWAHandoverOccurredOn` and still seeds a key so a later dated event can win.
- **Common messages never create a key**: this is what keeps non-routing numbers out of Redis, and what matches Meta's "likely owner" wording.
- **Standby is not an ownership signal in this spec**: `control_taken` and TTL cover loss of ownership. Standby copies without a prior handover would otherwise invent `not_owner` for traffic this spec does not yet ingest as messages.
- **`EnableWACStandbyContext` does not gate `control_passed` summary writes**: those already shipped in 1.68.0. The flag only adds standby-field context persistence.
- **Send block is WAC-only inside `sendCloudAPIWhatsappMsg`**, so `WCD` is untouched.
- **Blocked send → `MsgFailed`**, not `MsgErrored`, so Mailroom does not retry a send that would be invalid until the next ownership change, and so we cannot implicitly take as escalation partner by retrying.
- **Lua in the handler package**, not `Backend`: single consumer, matches the WAC media-cache Redis use.
- **Ownership writes ignore both flags**: turning send-block on later requires keys that were already maintained.

## Open Questions

None.
