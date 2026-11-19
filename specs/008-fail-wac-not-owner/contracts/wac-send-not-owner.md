# Contract: WAC send ownership failure

## Config

| Env | Type | Default | This spec |
|-----|------|---------|-----------|
| `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` | bool | `false` | When true: skip Graph for WAC **service** msgs in Redis `not_owner`; treat Graph 131070 on WAC as terminal `F`+`H`. When false: no Redis read, no 131070 special case (today). |

No new variable. SemVer: **minor**, backward compatible at default.

## `courier.MsgStatus`

```text
FailedReason() string
SetFailedReason(reason string)
```

Empty reason: status SQL does not change `msgs_msg.failed_reason`. Non-empty: set the column to that value.

SemVer: **minor** addition to the Go interface in this module. All in-repo implementers must compile.

## Postgres status UPDATE

All three writers (`updateMsgID`, `updateMsgExternalID`, `bulkUpdateMsgStatusSQL`):

```text
failed_reason = CASE
  WHEN :failed_reason != '' THEN :failed_reason
  ELSE failed_reason
END
```

Consumers: RapidPro UI / Flows history. Courier does not render the label.

## Graph

Unchanged request shape. Behaviour when flag on and WAC:

| Condition | Graph HTTP | Resulting `msgs_msg` |
|-----------|------------|----------------------|
| `not_owner` + service | not called | `status=F`, `failed_reason=H` |
| `not_owner` + allowed template / DirectSend | called | Wired on 2xx as today |
| HTTP 4xx `{"error":{"code":131070}}` | called once | `F`, `H`; Redis `not_owner` |
| other error.code / 5xx | called | `E` as today |

`WCD` Graph traffic is unchanged even if the JSON contains 131070.

## Redis

Internal. Other services MUST NOT read `wac:thread_owner:*`. 131070 may `HSET` `state=not_owner`.

## Logs

| event | reason / source |
|-------|-----------------|
| `wac_send_blocked` | `not_owner` or `131070` |
| `wac_thread_owner_applied` | `source=graph_131070` |
| `wac_thread_owner_update_failed` | `source=graph_131070` |

Fields: `event`, `channel_uuid`, `msg_uuid` (blocked), `outcome`/`state` (applied). No URN, no text.
