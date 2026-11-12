# Research: WAC Thread Ownership

All Technical Context items were already decided in [spec.md](./spec.md). This file records those decisions in the plan format and the repository patterns they reuse. No `NEEDS CLARIFICATION` remains.

## Decision 1: Redis hash + Lua CAS in the handler package

- **Decision**: Store `{state, ts}` in a Redis hash keyed `wac:thread_owner:{channel_uuid}:{urn.Identity()}`. Compare-and-set in one `redis.NewScript(1, ...)` EVAL. Wrappers live in `handlers/facebookapp/wa_thread_owner.go` and use `h.Backend().RedisPool()`, same as the WAC media cache (`facebookapp.go` ~4763).
- **Rationale**: Principle XII requires shared external state; Principle XV wants the `<=` rule in one named script; Principle XVI forbids growing `Backend` for a WAC-only consumer. `redigo` scripts take `KEYS` count as the first constructor argument; this script has one key, the rest are `ARGV`.
- **Alternatives considered**: Postgres `wa_conversation_handover` as source of truth (wrong entity); `Backend` get/set methods (forces MockBackend/RapidPro for no other consumer); GET+SET without Lua (lost updates under concurrent webhooks).

## Decision 2: Timestamp compare is `incoming <= stored` → `stale`

- **Decision**: Apply only when the key is missing or `tonumber(incoming) > tonumber(stored)`. Equal timestamps are `stale`. Missing handover `value.timestamp` uses `entry.time` via existing `parseWAHandoverOccurredOn`, including `0`.
- **Rationale**: Meta redelivers the same body; applying equals would refresh TTL and look like a new event. Context persist on `control_passed` still runs after a `stale` so a failed Postgres write can recover (spec Implementation Decisions).
- **Alternatives considered**: `>=` last-write-wins (redelivery churn); skip events with missing timestamp (cannot seed a key); millisecond timestamps (existing handover code is Unix seconds).

## Decision 3: Two apply modes, messages never create

- **Decision**: Lua `mode=set` for `control_passed`/`control_taken` (create or update). `mode=update_if_exists` for `field=messages` (return `absent` and write nothing if the key is missing).
- **Rationale**: AC-06 — numbers without conversation routing must not gain a mark. An inbound message only *likely* means we own the thread (Meta Thread Control docs).
- **Alternatives considered**: Create `owner` on every inbound message (marks non-routing numbers); use standby copies as a create signal (out of scope; standby is not an ownership write).

## Decision 4: Flags default off; ownership writes ignore them

- **Decision**: `EnableWACStandbyContext` and `EnableWACSendOwnershipBlock` in `config.go`, `ezconf` → `COURIER_ENABLE_WAC_STANDBY_CONTEXT` / `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK`, default `false`. Redis apply always runs on WAC. Standby flag only gates `field=standby` context persist. Send flag only gates the Graph skip. Existing `control_passed` summary writes stay ungated (1.68.0).
- **Rationale**: AC-01, AC-07. Staging can accumulate keys before blocking sends. Gating summary persist would regress conversation orchestration.
- **Alternatives considered**: Per-channel flags (spec says global); fail-closed send on Redis read (AC-08 forbids it); `MsgErrored` on block (Mailroom retry / implicit take).

## Decision 5: Failure split by path

- **Decision**: Handover Redis write error → return error (Meta redelivers). `field=messages` Redis write error → log `wac_thread_owner_update_failed`, continue, HTTP 200. Send Redis read error or missing key → send as today. Blocked send → `MsgFailed`, no Graph call.
- **Rationale**: Handover payloads have no inbound msg to duplicate. Message payloads do (`WriteMsg`). Principle XI recoverability vs duplicate inbound. Terminal `MsgFailed` matches Weni Webchat's non-retriable send failures.
- **Alternatives considered**: Non-200 on message Redis failure (duplicates); Redis retry loop (Principle XI is REST, not this store; existing Lua callers do one `Do`).

## Decision 6: WAC only, same URN as inbound attribution

- **Decision**: Gate on `channel.ChannelType() == "WAC"` in `processCloudWhatsAppPayload` and at the start of the send-block check inside `sendCloudAPIWhatsappMsg`. Key URN matches inbound: handover `resolveWAHandoverSenderURN` (`wa_id` then `user_id` then `phone_number`); messages use the already-resolved primary `urn`.
- **Rationale**: `processCloudWhatsAppPayload` and `sendCloudAPIWhatsappMsg` also serve `WCD`. Redis cannot join phone and BSUID; matching attribution is the least-wrong local key.
- **Alternatives considered**: Also mark `WCD` (demo, out of scope); always key by BSUID (breaks when handover has `wa_id` and the message has `from` as digits).

## Decision 7: Tests against MockBackend Redis, not a fake

- **Decision**: Unit-test Lua via `courier.NewMockBackend().RedisPool()` (real local Redis, `FLUSHDB` on construct). Flow-test webhooks with `TestReceiveWACHandover` / `RunChannelTestCases` and `PrepBackend` to seed keys. Send-block tests use `RunChannelSendTestCases` with `PrepBackend` + per-test config on `NewTestServer` (pattern: `handlers/telephony/telephony_test.go`). Flush `wac:thread_owner:*` at the start of each Redis-asserting test because `NewMockBackend` flushes DB 0 once, not per subtest.
- **Rationale**: CI already runs Redis (`.github/workflows/ci.yml`). Lua correctness is the CAS contract; a fake `Do` would not prove atomicity.
- **Alternatives considered**: miniredis (new dependency, Principle II); table-driven pure Go Lua interpreter (would not match production redigo/EVAL).
