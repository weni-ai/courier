# Contract: WWC human-support hop

Courier is the HTTP client. The socket is the server. Engine is the producer of the queue row and the consumer of inbound `external_id`. Minor, additive. Absent new keys keep today's bodies.

## Env

| Name | Default | Use |
|------|---------|-----|
| `COURIER_WWC_HUMAN_SUPPORT_INGRESS_TOKEN` | empty | `Authorization: Bearer` on privileged `/send` only. Same secret as the socket `WWC_HUMAN_SUPPORT_INGRESS_TOKEN`. Empty: privileged work does not open a connection. |

## Inbound `POST /c/wwc/<uuid>/receive`

Method, URL, and `application/json` stay. Optional field:

```json
{ "message": { "message_id": "3f1a8c0e-6b2d-4e5a-9c7d-1b0e4a6c8d20" } }
```

A valid id is stored on `msgs_msg.external_id`. Invalid or absent: field ignored, message accepted.

## Outbound `POST {base_url}/send`

`Content-Type: application/json`. Privileged requests also send `Authorization: Bearer <token>` and `Idempotency-Key` (`event_id`, or the msg UUID for a chat message).

A body is privileged when `type` is `human_support_started`, `human_support_ended`, `human_support_state`, or `message_status`, or when `author` is `human_agent`, or when `agent_first_name` is non-empty.

### Control events

`to` is the URN path. `from` is the channel address. `channel_uuid` is the channel UUID. No `message` object.

```json
{
  "type": "human_support_started",
  "to": "123",
  "from": "250788383383",
  "channel_uuid": "8eb23e93-5ecb-45ba-b726-3b064e0c568c",
  "event_id": "evt-1",
  "occurred_at_ms": 1730000000123
}
```

`human_support_ended` uses the same shape. `human_support_state` sends `"open": true` and no `occurred_at_ms`. `message_status` sends `"status": "delivered"` or `"read"` and, when Engine passed them, `"message_ids": ["<id>"]`.

### Chat

Today's `moPayload`, plus top-level `author` and `agent_first_name` when Engine set them. They are not inside `message`. `ai_agent` with no name stays unauthenticated.

### Typing

Nameless `typing_start` is unchanged: `from` is `ai-assistant`, no `Authorization`. A name adds `agent_first_name` and the bearer. `typing_stop` uses that same shape with `"type": "typing_stop"`.

## Status mapping Courier applies

| Socket | Courier |
|--------|---------|
| 202 | `MsgSent` |
| 401, 400, 409 | `MsgFailed`, one request |
| `message_status` 404 | up to 3 requests in the send, then `MsgErrored` |
| 403, and 404 on any other privileged type | `MsgErrored`, one request |
| 500, 429, 502, 503, 504, transport error | up to 3 requests, then `MsgErrored` |

Courier does not parse a success body. A JSON error body's `error` string is `socket_error` in the log, never the agent name.

## Consumers

- **Engine** must not enqueue the new `action_type` values until this build is serving. It reads `external_id` back as `message_ids`.
- **weni-webchat-socket** already implements the server side. An older Courier that receives a control row will POST it as chat `type: message`.
- **WhatsApp** is not a party to this contract.
