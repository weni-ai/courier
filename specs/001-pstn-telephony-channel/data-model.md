# Data Model: PSTN Telephony Channel (Courier)

## Channel (existing `channels_channel` via RapidPro backend)

| Field | Value for PSTN |
| ----- | -------------- |
| `channel_type` | `TPH` |
| `address` | DID (E.164), e.g. `+15551234567` |
| `config.base_url` | Voice gateway base URL for outbound send |
| `config.auth_token` | Optional bearer token for gateway auth |
| `schemes` | `["tel"]` |

## Inbound payload (`POST /c/tph/receive`)

| Field | Type | Required | Description |
| ----- | ---- | -------- | ----------- |
| `type` | string | yes | Must be `message` |
| `origin` | string | yes | Must be `pstn` |
| `did` | string | yes | Dialed number; resolves channel |
| `caller_id` | string | no | Caller phone number (E.164 preferred) |
| `call_id` | string | yes | Active call session identifier |
| `message.type` | string | yes | Must be `text` for v1 |
| `message.timestamp` | string | yes | Unix epoch seconds |
| `message.text` | string | yes | Committed transcript |
| `message.message_id` | string | no | Idempotency key stored as external ID |

## Outbound payload (`POST {base_url}/send`)

| Field | Type | Required | Description |
| ----- | ---- | -------- | ----------- |
| `type` | string | yes | `message` |
| `origin` | string | yes | `pstn` |
| `to` | string | yes | Caller `tel` path (URN path) |
| `from` | string | yes | Channel DID |
| `call_id` | string | no | From message metadata when available |
| `message.type` | string | yes | `text` |
| `message.timestamp` | string | yes | Unix epoch seconds |
| `message.text` | string | yes | Agent text for TTS |

## URN construction

| Input | URN |
| ----- | --- |
| `caller_id=+15559876543` | `tel:+15559876543` |
| `caller_id` empty, `call_id=abc-123` | `tel:withheld-abc-123` |
