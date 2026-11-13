# Quickstart: Fail WAC Not-Owner Sends

## Prerequisites

- Local Redis (MockBackend pool) and, for SQL tests, Postgres with Courier test schema.
- RapidPro production must already have `msgs_msg.failed_reason` allowing `H` before enabling the flag there.

## Commands

```bash
go test ./handlers/facebookapp/ -count=1 -run 'TestSendWACThreadOwner'
go test ./backends/rapidpro/ -count=1 -run 'TestMsgStatus'
```

## Expected send cases (`TestSendWACThreadOwner`)

Channel `testChannelsWAC[0]`, URN `whatsapp:250788123123`, Graph `httptest`.

| Setup | Graph hits | Status | Extra |
|-------|------------|--------|--------|
| Flag off, seed `not_owner` | 1 | `W` | unchanged |
| Flag on, `not_owner`, plain text | 0 | `F`, reason `H` | log `wac_send_blocked` `reason=not_owner` |
| Flag on, `not_owner`, templating MARKETING / UTILITY / AUTHENTICATION | 1 | `W` | three subtests |
| Flag on, `not_owner`, `DirectSend` | 1 | `W` | |
| Flag on, no key / `owner` / Redis closed | 1 | `W` | closed: log `wac_send_ownership_read_failed` |
| Flag off, Graph 400 `{"error":{"code":131070}}` | 1 | `E` | Redis unchanged, no `H` |
| Flag on, key missing or `owner`, Graph 400 131070 | 1 | `F`, `H` | Redis `not_owner`; log `reason=131070` |
| Flag on, Graph 400 `error.code` 131047 | 1 | `E` | Redis unchanged |
| Flag on, 131070, Redis write forced to fail | 1 | `F`, `H` | log `wac_thread_owner_update_failed` `source=graph_131070` |

PII: `assertNoWACOwnershipPII` on blocked and 131070 logs.

WCD: do not add 131070 assertions to WCD send cases; flag stays off in `SendTestCasesWAC`.

## Expected SQL (`TestMsgStatus` / dedicated subtest)

- `WriteMsgStatus` `F` + `SetFailedReason("H")` → row `failed_reason='H'`, `error_count` unchanged.
- Later `WriteMsgStatus` Wired with empty reason → `failed_reason` still `'H'` (column not nulled).

## Manual flag check (staging)

1. Deploy Courier, flag **false**. Confirm 131070 still errors (`E`).
2. Confirm RapidPro accepts `H`.
3. Set `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK=true`. Seed or wait for `not_owner`. Send a session text → history failed with the Flows label. Send a marketing template → Graph called.
