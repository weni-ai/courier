# Contract: WAC Pending Context

Public surfaces this feature changes. Column list, check constraint, and partial unique index are unchanged. Details of the row: [data-model.md](../data-model.md).

SemVer: **patch**. No new route, no new `COURIER_*` variable, no removed field. Mailroom keeps selecting one row where `consumed_on IS NULL` and setting `consumed_on`. A longer `context_text` still fits `{text}; Context: {context_text}`.

## Configuration

No new variable. Existing flags keep their jobs:

| Variable | This feature |
|----------|----------------|
| `COURIER_ENABLE_WAC_STANDBY_CONTEXT` | Still gates `field=standby` context persist only. Does not gate `control_passed` or `field=messages`. |
| `COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` | Untouched. |

## Postgres: pending `wa_conversation_handover` row

Writer: `Backend.WriteWAConversationHandover` → `upsertWAConversationHandoverSQL`.

`Backend.WriteWAConversationHandover`’s Go signature is unchanged.

| Situation | Result |
|-----------|--------|
| No row with `consumed_on IS NULL` for that `channel_id`+`contact_id` | INSERT. A consumed row for the same pair stays as it is. |
| Pending row exists | UPDATE that row. `context_text` is appended (newline rules and 32000-character tail in [data-model.md](../data-model.md)). `context_type` becomes `history` if either side is `history`, otherwise `summary`. `context_payload` becomes the new JSON. Owner metadata columns change only when the new event sets them. |
| Same payload delivered again after a successful write | Another append of the same line is possible. Accepted. One pending row still. |

Timeout remains `backendTimeout` (20s) inside `(*backend).WriteWAConversationHandover`. Database error still tries the existing spool; spool success returns nil to the handler.

## Inbound: `POST /c/wac/receive`

Existing route. `X-Hub-Signature` check unchanged, including the existing `X-Router-Token` skip. Channel type `WAC` only for the new side effect. `WCD` `POST /c/wcd/receive` (`receiveDemoEvent`) shares the processor and must not write a handover row from `conversation_context`.

### `field=messages` with `conversation_context`

`conversation_context` is on `changes[].value`, the same object `moPayload` already unmarshals. It is not `messages[].context` (that remains quote/reply metadata on the msg).

```json
{
  "object": "whatsapp_business_account",
  "entry": [{
    "id": "<WABA_ID>",
    "changes": [{
      "field": "messages",
      "value": {
        "messaging_product": "whatsapp",
        "metadata": { "phone_number_id": "<PHONE_NUMBER_ID>" },
        "contacts": [{ "profile": { "name": "Kerry Fisher" }, "wa_id": "5678" }],
        "conversation_context": { "summary": { "text": "AI summary" } },
        "messages": [{
          "from": "5678",
          "id": "wamid.HBgM",
          "timestamp": "1454119029",
          "type": "text",
          "text": { "body": "Hello World" }
        }]
      }
    }]
  }]
}
```

| Condition | HTTP | `wa_conversation_handover` | Inbound msg | Log |
|-----------|------|------------------------------|-------------|-----|
| `WAC`, summary or history renders, writer ok | 200 | one `WriteWAConversationHandover` before `WriteMsg`, per queued message. URN from the message sender. `OccurredOn` from `messages[].timestamp`. | queued as today | no new success line |
| `WAC`, context absent or does not render (blank summary, empty history) | 200 | no handover write | queued as today | none from this feature |
| `WAC`, context renders, `WriteWAConversationHandover` returns an error | 200 if `WriteMsg` succeeds | no new row from this attempt (spool may still hold the event if the spool write succeeded; that return is nil and is not this row) | still queued | ERROR `event=wac_pending_context_write_failed`, `channel_uuid`, `external_id` = `messages[].id`. No context text, contact name, or URN field |
| `WCD` (or any non-`WAC`) with the same body | unchanged | no handover write from this feature | unchanged | none from this feature |
| `type=reaction` or the `unsupported` branch that `continue`s | 200 | no handover write for that message | not queued (today) | none from this feature |

Response body for a normal inbound stays the message-receive data. This path does not add `"wa conversation handover persisted"`.

### Unchanged webhook results

| Field | Persist error | Messages / statuses copies |
|-------|---------------|----------------------------|
| `messaging_handovers` / `control_passed` with renderable context | non-200, Meta redelivers | n/a |
| `messaging_handovers` / `control_passed` without context | 200, no row | n/a |
| `messaging_handovers` / `control_taken` | n/a (no handover write) | no inbound msg |
| `standby` with flag on and renderable context | non-200 on persist error | still not written as msgs |
| `standby` with flag off | n/a | ignored, as today |

Redis `wac:thread_owner:*` behaviour from spec 005 is unchanged on all of these fields.

## Consumers

| Consumer | Obligation |
|----------|------------|
| Mailroom | None. Still one pending row, still `context_text` as a string. |
| RapidPro | None in this repository. Schema already matches. |
| Meta | 200 on the messages outcomes above. Non-200 when standby or `control_passed` persist fails, as today. |
