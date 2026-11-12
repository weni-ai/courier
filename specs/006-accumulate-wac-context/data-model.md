# Data Model: Accumulate WAC Context

No migration. The only persisted entity is the existing `wa_conversation_handover` row. This feature changes how an unconsumed row is updated.

Schema: `backends/rapidpro/schema.sql` (lines 157–177). Writer: `writeWAConversationHandoverToDB` in `backends/rapidpro/wa_conversation_handover.go`.

## wa_conversation_handover (Postgres, existing)

| | |
|---|---|
| **Identity of the buffer** | One row with `consumed_on IS NULL` per `(channel_id, contact_id)`, index `uq_wa_conv_handover_pending` |
| **Consumed rows** | `consumed_on` set by Mailroom. Courier does not update them. A later write inserts a new pending row. |
| **context_type** | `summary` or `history` only (`chk_wa_conv_handover_context_type`) |
| **context_text** | `text NOT NULL`. Concatenated buffer. May be `[truncated]` plus 32000 characters. |

### Conflict assignments

Applied only when the partial unique index matches. Insert column list is unchanged.

| Column | On conflict |
|--------|-------------|
| `context_text` | Append with one newline (U+000A), then tail-truncate. See rules below. |
| `context_type` | `history` if stored or new is `history`; otherwise `summary` |
| `context_payload` | Replace with the new event’s JSON (NULL if the event has none). Do not merge. |
| `previous_owner_app_id` | `COALESCE(new, stored)` |
| `previous_owner_app_role` | `COALESCE(new, stored)` |
| `previous_owner_business_id` | `COALESCE(new, stored)` |
| `handover_metadata` | `COALESCE(new, stored)` |
| `contact_urn` | Replace with the new event’s URN |
| `occurred_on` | New event’s `OccurredOn` |
| `created_on` | `NOW()` |
| `org_id`, `channel_id`, `contact_id` | Unchanged |
| `consumed_on`, `consumed_msg_id` | Unchanged (not in the INSERT list) |

Empty string on the Go event means the `null.String` is left unset, so `EXCLUDED` is NULL and `COALESCE` keeps the stored metadata. A non-empty `control_passed` value overwrites that column only.

### context_text rules

Let `stored` be `wa_conversation_handover.context_text` and `new` be `EXCLUDED.context_text`.

1. `stored = ''` → `combined = new`
2. `new = ''` → `combined = stored`
3. otherwise → `combined = stored || chr(10) || new`
4. `char_length(combined) <= 32000` → store `combined`
5. else → store `'[truncated]' || right(combined, 32000)`

`32000` is `maxWAConversationHandoverContextRunes` in the writer file. The handler still truncates each rendered piece with `maxWAHandoverContextTextLen` before the call. The writer truncates the combination.

### Who writes a row

| Event | Gate | Row |
|-------|------|-----|
| `control_passed` with renderable context | none (1.68.0 / 005) | upsert pending; may set `previous_owner_*` |
| `control_passed` without renderable context | — | no write |
| `control_taken` | — | no write |
| `field=standby` with renderable context | `EnableWACStandbyContext=true` | upsert pending; owner columns empty on the event, so stored owner metadata is kept |
| `field=standby` otherwise | flag false, or context does not render | no write; `Messages` / `Statuses` still ignored |
| `WAC` `field=messages` with renderable `conversation_context`, per message that reaches `WriteMsg` | none | upsert pending before that `WriteMsg`; owner columns empty on the event |
| `WAC` `field=messages` without renderable context | — | no write; `WriteMsg` unchanged |
| `WCD` / `FBA` / `IG` / `WA` | — | no write from this feature |

`OccurredOn` for a messages write is the message timestamp already parsed in the loop (`time.Unix(msg.Timestamp).UTC()`). Contact URN is that message’s resolved URN (phone when both phone and BSUID are present). `ContextPayload` is `json.Marshal` of `change.Value.ConversationContext`.

### State of one contact’s buffer

```text
(no pending row) --insert--> pending(text = new)
pending --append--> pending(text = stored + "\n" + new, truncated)
pending --Mailroom sets consumed_on--> consumed
consumed --later write--> consumed unchanged + new pending row
```

A second pending row for the same channel and contact cannot exist. The index rejects it; the writer takes the `DO UPDATE` path instead.

### Validation

- `renderWAHandoverContextText` still refuses nil context, blank summary, and empty history. Those paths do not call the writer.
- The SQL `CASE` for `context_type` emits only `history` or `summary`.
- Message timestamp and sender URN failures still fail the webhook before the new hook, via the existing parse and `urns.NewWhatsAppURN` checks.

## Not stored

- Redis `wac:thread_owner:*` is unchanged and is not read or written by this feature.
- No new `COURIER_*` setting.
- Standby message bodies, echoes, and template copies are not columns and are not appended as lines.
