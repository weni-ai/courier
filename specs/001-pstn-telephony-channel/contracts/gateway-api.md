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

## Outbound: Courier → gateway

**Endpoint**: `POST {channel.config.base_url}/send`  
**Content-Type**: `application/json`  
**Authorization**: `Bearer {channel.config.auth_token}` when configured

```json
{
  "type": "message",
  "origin": "pstn",
  "to": "+15559876543",
  "from": "+15551234567",
  "call_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "message": {
    "type": "text",
    "timestamp": "1721567895",
    "text": "Sure, I can help with your order."
  }
}
```

## Error semantics

| Condition | HTTP | Courier behavior |
| --------- | ---- | ---------------- |
| Unknown DID | 404/400 | No message written |
| Empty text | 400 | No message written |
| Invalid origin | 400 | No message written |
| Gateway send failure | — | Outbound msg status `errored` |
