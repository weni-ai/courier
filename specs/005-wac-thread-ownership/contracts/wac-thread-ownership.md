# Contract: WAC Thread Ownership

Public Courier surfaces this feature changes. The Redis key is **not** a public contract (see [data-model.md](../data-model.md)); Mailroom and RapidPro MUST NOT read it.

SemVer: MINOR for the two new settings. Inbound HTTP status codes stay 200 on the success paths below (patch-level body-text change is allowed).

## Configuration

Injected by `ezconf` from `COURIER_*`. Defaults are `NewConfig()` values.

| Variable | Type | Default | Behaviour |
|----------|------|---------|-----------|
| `COURIER_ENABLE_WAC_STANDBY_CONTEXT` | bool | `false` | When true, persist renderable `conversation_context` from `field=standby` via `WriteWAConversationHandover`. When false, standby stays ignored. Does not gate `control_passed` summary writes. |
| `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` | bool | `false` | When true, a WAC send whose Redis state is `not_owner` does not call Graph and the message becomes `MsgFailed` (`F`). Missing key or Redis read error: send as today. |

Ownership Redis updates run for channel type `WAC` regardless of both flags.

No new secrets.

## Inbound: `POST /c/wac/receive`

Existing route. Signature check unchanged. Channel type `WAC` only for the side effects below.

### `field=messaging_handovers`, `type=control_passed`

```json
{
  "object": "whatsapp_business_account",
  "entry": [{
    "id": "<WABA_ID>",
    "time": 1454119029,
    "changes": [{
      "field": "messaging_handovers",
      "value": {
        "messaging_product": "whatsapp",
        "sender": { "wa_id": "5678" },
        "recipient": { "phone_number_id": "<PHONE_NUMBER_ID>" },
        "type": "control_passed",
        "timestamp": "1454119029",
        "control_passed": {},
        "conversation_context": { "summary": { "text": "optional" } }
      }
    }]
  }]
}
```

| Condition | HTTP | Redis | `wa_conversation_handover` | Inbound msg |
|-----------|------|-------|------------------------------|-------------|
| Valid sender, ts applies | 200 | `owner` created/updated | row only if context renders | none |
| Valid sender, ts stale | 200 | unchanged | row still attempted if context renders | none |
| No renderable context | 200 | `owner` as above | **no row** | none |
| Missing sender | 400 | none | none | none |
| Invalid `value.timestamp` | 400 | none | none | none |
| Redis write error | non-200 | none/partial | none (write is after Redis) | none |

Missing `value.timestamp` uses `entry.time`. Sender may be `wa_id`, `user_id`, or `phone_number`.

### `field=messaging_handovers`, `type=control_taken`

```json
{
  "object": "whatsapp_business_account",
  "entry": [{
    "id": "<WABA_ID>",
    "time": 1454119029,
    "changes": [{
      "field": "messaging_handovers",
      "value": {
        "messaging_product": "whatsapp",
        "sender": { "phone_number": "+15551234567" },
        "recipient": { "phone_number_id": "<PHONE_NUMBER_ID>" },
        "type": "control_taken",
        "timestamp": "1454119029",
        "control_taken": {
          "previous_owner_role": "customer_service",
          "new_owner_role": "escalation"
        }
      }
    }]
  }]
}
```

| Condition | HTTP | Redis | Handover row | Inbound msg / channel event |
|-----------|------|-------|--------------|-----------------------------|
| Valid sender, ts applies | 200 | `not_owner` | none | none |
| ts stale | 200 | unchanged | none | none |
| Missing sender | 400 | none | none | none |
| Redis write error | non-200 | — | none | none |

Inner `control_taken` object is optional for this contract; `value.type` is the discriminator.

Other `messaging_handovers` types: HTTP 200 `ignored_type`, no Redis write (today).

### `field=messages`

Existing inbound message handling. After the primary URN is resolved:

- Redis `update_if_exists` → `owner` using `message.timestamp`.
- Key missing → no Redis write, message handled as today.
- Redis write error → message handled as today, HTTP 200, log `wac_thread_owner_update_failed`.

### `field=standby`

| Flag | Renderable `conversation_context` | HTTP | Handover row | Redis |
|------|-----------------------------------|------|--------------|-------|
| false (default) | any | 200 | none | none |
| true | yes | 200 | yes | none |
| true | no | 200 | none | none |

Standby message copies are never turned into inbound `Msg`.

## Outbound: WAC Graph send

Route unchanged: `SendMsg` → `sendCloudAPIWhatsappMsg` → `POST /{phone-number-id}/messages` (or `marketing_messages`). Channel type `WAC` only.

| `EnableWACSendOwnershipBlock` | Redis `state` | Graph HTTP | Msg status |
|-------------------------------|---------------|------------|------------|
| false | any / missing / read error | called as today | as today |
| true | missing / empty / `owner` / read error | called as today | as today |
| true | `not_owner` | **not called** | `F` (`MsgFailed`) |

Mailroom sees a terminal failed outgoing message. No new RabbitMQ/Postgres payload fields.

## Consumers

| Consumer | Contract they see |
|----------|-------------------|
| Meta | HTTP 200/400 as today on these paths; body text on handover 200 may change |
| Operators | two new env vars, default off |
| Mailroom | `MsgFailed` when send is blocked; `wa_conversation_handover` rows only when context still persists |
| Other Weni services | no Redis key access |
