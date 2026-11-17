# Quickstart: WAC Thread Ownership

Validation guide for the slice in [spec.md](./spec.md). Implementation details belong in `tasks.md`. Redis key and flag semantics: [data-model.md](./data-model.md). Webhook and send contracts: [contracts/wac-thread-ownership.md](./contracts/wac-thread-ownership.md).

## Prerequisites

- Local Redis on `localhost:6379` (CI uses the same; `MockBackend` `SELECT`s DB 0 and `FLUSHDB` on construct).
- Postgres is **not** required for the Lua unit tests; flow tests use `MockBackend`.
- From repo root, with the module cache available.

## Tests that prove the feature

```bash
# Lua CAS: created, updated, stale (older and equal), absent, missing timestamp fallback
go test ./handlers/facebookapp/ -count=1 -run 'TestWACThreadOwner'

# Handover + messages + standby flow through POST /c/wac/receive
go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACHandover|TestReceiveWACThreadOwner|TestReceiveWACStandbyContext'

# Send block / fail-open
go test ./handlers/facebookapp/ -count=1 -run 'TestSendWACThreadOwner'

# Defaults
go test . -count=1 -run 'TestNewConfig' 
```

Expect all of the above green. Named `-run` patterns are the intended test names from the spec; if implement names them slightly differently, the same cases MUST still exist.

## Local webhook checks (optional)

Courier running with Redis, a WAC channel whose address is the fixture `phone_number_id` (`12345` in testdata), and both flags off:

1. POST the no-context handover fixture (`handlers/facebookapp/testdata/wac/handoverNoContextWAC.json`) to `/c/wac/receive` with a valid `X-Hub-Signature`.
2. Expect HTTP 200 and Redis:

```bash
redis-cli GET ignore
redis-cli HGETALL 'wac:thread_owner:<channel-uuid>:whatsapp:5678'
# state = owner, ts = 1454119029, TTL in (0, 86400]
```

3. POST `helloWAC.json` for a **different** URN that has no key. Expect HTTP 200 and **no** new `wac:thread_owner:*` key for that URN.
4. Seed `not_owner` for `whatsapp:5678`, POST `helloWAC.json`. Expect `state=owner`.

## Flag checks

```bash
# send block off (default): Graph is called even with not_owner in Redis — covered by TestSendWACThreadOwner
# send block on: not_owner → no Graph, status F
export COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK=true

# standby context on: field=standby with summary → WriteWAConversationHandover
export COURIER_ENABLE_WAC_STANDBY_CONTEXT=true
```

## Expected log events (no URN paths)

| Event | When |
|-------|------|
| `wac_thread_owner_applied` | Lua returned created/updated/stale/absent |
| `wac_thread_owner_update_failed` | Redis write failed on `field=messages` |
| `wac_send_blocked` | Send skipped, `reason=not_owner` |
| `wac_send_ownership_read_failed` | Send Redis read failed; Graph still called |

## Done for this repo's tests

- AC-01…AC-08 mapped in spec Test Strategy are green.
- `go test -p=1 ./handlers/facebookapp/` does not regress existing WAC receive/send cases.
