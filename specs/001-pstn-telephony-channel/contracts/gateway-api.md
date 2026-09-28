# Gateway API Contract: PSTN Telephony

Base path: Courier exposes `POST /c/tph/receive` (no channel UUID in path).

## Inbound: gateway → Courier

**Endpoint**: `POST /c/tph/receive`  
**Content-Type**: `application/json`

```json
{
  "type": "message",
  "origin": "pstn",
  "did": "+15551234567",
  "caller_id": "+15559876543",
  "call_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "message": {
    "type": "text",
    "timestamp": "1721567890",
    "text": "I need help with my order",
    "message_id": "turn-001"
  }
}
```

**Success response**: `200` with Courier standard `"Handled"` body.

## Outbound: Courier does not call the gateway

Agent replies are streamed by Nexus over gRPC to the voice gateway (same as Weni Web Chat). Courier `SendMsg` for `TPH` acknowledges the queued message as sent and must not `POST {base_url}/send`.

Channel config `base_url` / `auth_token` are not required and are ignored if present on older channels.

## Error semantics

| Condition | HTTP | Courier behavior |
| --------- | ---- | ---------------- |
| Unknown DID | 404/400 | No message written |
| Empty text | 400 | No message written |
| Invalid origin | 400 | No message written |
| Gateway send failure | — | N/A (no outbound gateway HTTP) |
