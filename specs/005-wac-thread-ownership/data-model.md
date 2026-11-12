# Data Model: WAC Thread Ownership

No Postgres migration. The only new persisted entity is a TTL-bounded Redis hash. `wa_conversation_handover` is unchanged.

## ThreadOwner (Redis)

| | |
|---|---|
| **Key** | `wac:thread_owner:{channel_uuid}:{urn.Identity()}` |
| **Type** | hash |
| **TTL** | 86400 seconds from last **applied** write (`wacThreadOwnerTTL`) |
| **Lifetime** | Matches Meta thread idle (24 h of WhatsApp-user inactivity). Expired key = no mark. |

### Fields

| Field | Type | Values | Rules |
|-------|------|--------|-------|
| `state` | string | `owner` \| `not_owner` | Required on every applied write. |
| `ts` | string (decimal integer) | Unix seconds of the last **applied** event | Compare with `tonumber`. Incoming `<=` stored → no write. |

### Identity

- `channel_uuid`: `courier.ChannelUUID` of the WAC channel (`phone_number_id` already resolved before the handler runs).
- `urn.Identity()`: WhatsApp URN identity, e.g. `whatsapp:5678`.
  - Handover: `resolveWAHandoverSenderURN` (`wa_id` → `user_id` → `phone_number` with leading `+` stripped).
  - `field=messages`: primary `urn` already chosen (phone when both phone and BSUID are present).
- Missing sender on a handled handover type → HTTP 400, no key.

### Validation

- `state` must be one of the two constants; Lua receives it from Go, never as a free string from the webhook (`control_passed` → `owner`, `control_taken` → `not_owner`, `field=messages` → `owner`).
- `ts` is `occurredOn.Unix()` after `parseWAHandoverOccurredOn` or `strconv.ParseInt(msg.Timestamp)`. Invalid non-empty handover timestamp → 400. Invalid message timestamp → 400 (existing).
- Channel type must be `WAC`. `WCD` / `FBA` / `IG` never read or write this key.

### State transitions

```text
                    set(control_passed)                 set(control_taken)
   (missing) ----------------------> owner ------------------> not_owner
       ^                               |                          |
       |                               | update_if_exists         | update_if_exists
       | TTL expiry                    | (messages, ts wins)      | (messages, ts wins)
       |                               v                          v
       +--------------------------- owner <--------------------- owner
```

| Source | Mode | Target state | If key missing | If incoming `<=` stored |
|--------|------|--------------|----------------|-------------------------|
| `control_passed` | `set` | `owner` | `created` | `stale` |
| `control_taken` | `set` | `not_owner` | `created` | `stale` |
| `field=messages` | `update_if_exists` | `owner` | `absent` (no write) | `stale` |
| `field=standby` | — | — | no Redis write | — |

TTL is reset only on `created` and `updated`. `stale` and `absent` leave TTL unchanged.

## wa_conversation_handover (Postgres, existing)

Schema: `backends/rapidpro/schema.sql`. Writer: `WriteWAConversationHandover`.

This feature does **not** add columns. It only changes *when* a row is written:

| Event | Flag | Row written? |
|-------|------|----------------|
| `control_passed` with renderable summary/history | ignored | yes (1.68.0, ungated) |
| `control_passed` without renderable context | ignored | **no** (same as today; Redis still written) |
| `control_taken` | ignored | **no** |
| `field=standby` with renderable context | `EnableWACStandbyContext=true` | yes |
| `field=standby` otherwise | default false | **no** |

Empty context never inserts a row (`renderWAHandoverContextText` fails closed).

## Config (process-global, not persisted)

| Field | Env | Default | Effect |
|-------|-----|---------|--------|
| `EnableWACStandbyContext` | `COURIER_ENABLE_WAC_STANDBY_CONTEXT` | `false` | Standby context persist only |
| `EnableWACSendOwnershipBlock` | `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` | `false` | Skip Graph when Redis `state=not_owner` |

Neither field is stored in Redis or Postgres. Ownership writes ignore both.
