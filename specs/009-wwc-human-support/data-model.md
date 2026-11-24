# Data Model: WWC human-support forwarding

No new table, column, or index. The previous Courier image still reads `msgs_msg`.

## Queue message (existing `msgs_msg` row)

Engine writes these before Courier pops the row. Courier reads `metadata` and does not rewrite it.

| Field | Where | Rule |
|-------|--------|------|
| `metadata.action_type` | string | Absent or `typing_indicator`: today's paths. `human_support_started`, `human_support_ended`, `human_support_state`, `message_status`: control POST, no chat body. `typing_stop`: best-effort WWC action only. |
| `metadata.author` | string | `ai_agent` or `human_agent`. Any other value: `MsgFailed`, no POST. Absent: omit `author` on the wire. |
| `metadata.agent_first_name` | string | Trim. Empty omits the field. Internal Unicode space, or more than 100 runes: `MsgFailed`, no POST. Non-empty together with `ai_agent`: `MsgFailed`, no POST. |
| `metadata.event_id` | string | Required on the four control types. Length 1–128. Missing, empty, or longer: `MsgFailed`, no POST. |
| `metadata.occurred_at_ms` | number | Required on start and end. Positive integer. Copied unchanged into the JSON body. |
| `metadata.open` | bool | Required `true` on `human_support_state`. `false` or absent: `MsgFailed`, no POST. |
| `metadata.status` | string | `delivered` or `read` on `message_status`. `sending` or anything else: `MsgFailed`, no POST. |
| `metadata.message_ids` | string array | Optional on `message_status`. Absent is valid only for `read`. Present and empty, a non-string element, more than 100 ids, or `delivered` without ids: `MsgFailed`, no POST. |

Chat text, attachments, and products on a control row are not sent.

## Inbound shopper row (existing)

| Field | Rule |
|-------|------|
| `msgs_msg.external_id` | Set from callback `message.message_id` when that string is 1–64 characters and has no Unicode space. Otherwise left empty. The message is still queued. |
| `received_on` | Unchanged: `time.Unix` of `message.timestamp` in seconds. Courier does not store `timestamp_ms`. |

`author`, `delivery_status`, `support_id`, and `timestamp_ms` on the callback are ignored.

## Outbound status (existing `msgs_msg.status`)

Privileged chat sends and control events only. Typing does not write a status.

| After the call | Stored status | Next |
|----------------|---------------|------|
| HTTP 202 | `S` (`MsgSent`) | done |
| 401, 400, 409, empty token, invalid local payload | `F` (`MsgFailed`) | not retried |
| `message_status` 404 after 3 in-request tries | `E` (`MsgErrored`) | mailroom `next_attempt`, then `F` on the third `E` write |
| 403, or 404 on a non-status privileged type | `E` | same mailroom schedule, no in-request retry |
| 500, 429, 502, 503, 504, or transport error after 3 tries | `E` | same mailroom schedule |
| Send context deadline (35s) | `E` | same mailroom schedule |

Start, end, and state replay the same `event_id`. Status rank on the socket only moves forward, so a replay of `message_status` does not rewind a tick.

## What is not stored here

`support_id`, delivery status, milestones, and `agent_first_name` stay in the socket. The channel log may contain the redacted request. The name and the bearer are not in that dump.
