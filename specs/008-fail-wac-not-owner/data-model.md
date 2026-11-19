# Data Model: Fail WAC Not-Owner Sends

## Redis `wac:thread_owner:{channel_uuid}:{urn.Identity()}`

Unchanged schema from spec 005.

| Field | Values | This feature |
|-------|--------|----------------|
| `state` | `owner` \| `not_owner` | 131070 writes `not_owner` (mode `set`) |
| `ts` | Unix seconds string | `time.Now().Unix()` on 131070 |
| TTL | 24h from last apply | reset on that write |

**Validation**: send path only **reads** `state` (existing `HGET`). Write happens only after a parsed Graph `error.code == 131070` on WAC with the flag on.

## Postgres `msgs_msg.failed_reason`

| Property | Value |
|----------|--------|
| Type | `varchar(1)` nullable |
| Courier write | `H` (`FailedReasonOtherOwner`) when this feature fails a send |
| Other statuses | empty on the status object → SQL leaves the stored value |
| Production owner | RapidPro (choice + optional CHECK). Courier test schema only |

## `courier.MsgStatus`

| Method | Rule |
|--------|------|
| `FailedReason() string` | getter |
| `SetFailedReason(string)` | setter; `""` means do not update the column |

`DBMsgStatus` and `mockMsgStatus` implement both. Compile fails until every in-repo implementer is updated.

## Send outcome state

```text
queued send
  ├─ flag off                          → Graph, status as today (E on HTTP error)
  ├─ flag on, Redis read error         → Graph (fail-open)
  ├─ flag on, missing/owner            → Graph
  ├─ flag on, not_owner + allowed tpl  → Graph
  ├─ flag on, not_owner + service      → F + H, no Graph
  └─ Graph 131070 (WAC, flag on)       → F + H, Redis not_owner
```

`F` does not increment `error_count` or set `next_attempt` (existing SQL). That is what stops Mailroom retries.

## Allowed template categories

Named constants, compared with `strings.ToUpper`:

- `MARKETING`
- `UTILITY`
- `AUTHENTICATION`

Plus `msg.DirectSend() == true`.
