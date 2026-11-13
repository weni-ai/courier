# Data Model: Record WAC Standby

No schema change. Three stores are involved. Only the pending row is read by another service. Rules for when a row is written: [contracts/wac-standby.md](./contracts/wac-standby.md).

## Pending context (`wa_conversation_handover`)

Existing table (`backends/rapidpro/schema.sql`). One unconsumed row per `channel_id` + `contact_id` (`uq_wa_conv_handover_pending`).

| Field | This feature |
|-------|----------------|
| `context_type` | `history` (`courier.WAHandoverContextHistory`) for every transcript line. The existing `ON CONFLICT` promotes the row to `history` when either side is `history`. |
| `context_text` | The rendered line, including internal newlines for a template or flow. Appended with the existing newline rule. Not replaced. |
| `context_payload` | NULL for transcript lines. `persistRenderedWAHandover` marshals payload only when the context pointer is non-nil. Legacy `conversation_context` still stores its JSON. |
| `contact_urn` | The URN resolved for that item (shopper `from` / `from_user_id`, echo `to` / `recipient`). |
| `occurred_on` | Item timestamp when it parses; otherwise `entry.time`. |
| `previous_owner_*`, `handover_metadata` | Left empty on the event. The existing `COALESCE` keeps values already stored by `control_passed`. |
| `consumed_on` | Still Mailroom’s. Courier does not set it. |

`contactForURN` inside `writeWAConversationHandoverToDB` may insert the contact. That is not a queued message.

Validation: blank text does not write. Media and the `<flow>` fallback always have a non-empty line. `context_type` stays inside `chk_wa_conv_handover_context_type`.

## Dedup key (Redis string)

| | |
|--|--|
| Key | `wac:standby:seen:{channel_uuid}:{message_id}` |
| Value | `1` |
| TTL | 86400 seconds, set only by `SET key 1 NX EX 86400` |
| Claim | Shopper messages and echoes that rendered a line and have a non-empty id, after ownership Redis succeeded |
| Release | `DEL` when `WriteWAConversationHandover` returns a non-nil error |
| Not used for | Statuses, legacy `conversation_context`, flag-off requests, empty ids |

A second delivery on the same channel finds the key and does not append. The same id on another channel is a different key. The key is not `seen:externalid:*` and not `wac:thread_owner:*`.

## Thread ownership (Redis hash)

Existing key `wac:thread_owner:{channel_uuid}:{urn.Identity()}` (`wa_thread_owner.go`). This feature does not add fields.

| | |
|--|--|
| When | `EnableWACStandbyContext` is true, channel type `WAC`, the item has a URN, and the timestamp is either a parsed Unix second or an empty string that fell back to `entry.time` |
| Write | `applyWACThreadOwner` mode `set`, state `not_owner`, source `standby` |
| Compare | Existing Lua: `incoming <= stored` → `stale` (no `HSET`, no TTL refresh) |
| Create | Missing key becomes `not_owner` with `ts` set and TTL 24h |
| Flag off | No write. A pre-seeded hash is left as it is |

Statuses use the same hash and do not append context. A `stale` result still allows the transcript append.

## State of one standby item

```text
flag off                         → no row, no hash write, no seen key
no URN                           → skip item, webhook stays 200
ownership Redis error            → no seen key, webhook 400, Meta redelivers
render empty                     → hash may update, no seen key, no row
empty message id                 → hash may update, no seen key, no row
SET NX loses                     → hash may update, no new row
write returns nil                → seen key kept, row appended (or spooled)
write returns error              → seen key deleted, webhook 400, Meta redelivers
status                           → hash may update, no seen key, no row
```

## Migration

None. The previous binary reads the same table and ignores both Redis keys. Keys expire within 24 hours. A pending row that already contains transcript text is a legal `history` row.
