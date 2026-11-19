# Contract: WAC Standby Transcript

Inbound webhook, channel log, and the opt-in flag. Redis keys are internal: Mailroom and RapidPro MUST NOT read them. Row shape: [data-model.md](../data-model.md).

SemVer: patch on `POST /c/wac/receive` and on the channel-log body; minor on the existing flag’s opt-in behaviour. No breaking change and no deprecation path. The channel log stops storing the standby body on purpose.

## Configuration

No new variable. `COURIER_ENABLE_WAC_STANDBY_CONTEXT` stays a bool, default `false` (`config.go`).

| Flag | Transcript lines | `not_owner` from standby | Channel-log body |
|------|------------------|--------------------------|------------------|
| false | not written | not written (PR 335) | omitted |
| true, channel `WAC` | appended | `set` via the existing Lua script | omitted |
| true, channel `WCD` | not written (legacy `conversation_context` only, as today) | not written | omitted |

`COURIER_ENABLE_WAC_SEND_OWNERSHIP_BLOCK` is unchanged. A `not_owner` key blocks a Graph send only when that flag is already true.

## Inbound: `POST /c/wac/receive`

Existing route. `receiveEvent` still verifies `X-Hub-Signature` unless `X-Router-Token` is the existing skip. This contract does not add a field the caller must send. `field` remains `standby`. Content is under `value.standby`.

Success is HTTP 200. A returned handler error is HTTP 400 (`WriteError`). Info strings are `standby context recorded`, `standby status recorded`, `wa conversation handover persisted`, `standby without conversation context`, or `ignoring standby webhook`. They do not include message text, template parameters, phone numbers, or URNs.

### Shopper message (`value.standby.messages`)

Flag true, `WAC`. One history line per message that renders. Origin `[shopper]`. Text body is the trimmed `text.body`. Media is `<image>`, `<audio>`, `<video>`, `<document>`, `<voice>`, or `<sticker>` with no download.

```json
{
  "field": "standby",
  "value": {
    "standby": {
      "contacts": [{ "profile": { "name": "Test User" }, "wa_id": "5678" }],
      "messages": [{
        "from": "5678",
        "id": "wamid.SHOPPER",
        "timestamp": "1454119029",
        "type": "text",
        "text": { "body": "Test standby message" }
      }]
    }
  }
}
```

Effect: `context_text` `[shopper] Test standby message`, `context_type` `history`, URN `whatsapp:5678`, no queued msg. Redis `not_owner` when `1454119029` wins the compare.

### Text echo (`value.standby.message_echoes`)

Origin `[outro atendimento]`. The contact URN is `message.to`, else `message.recipient`.

Effect for body `Hello! Your order #12345 has shipped.` and `to` `5678`: one history line with that prefix and body, no queued msg.

### Flow echo

One line. Non-empty interactive header text, body text, footer text, and `flow_cta`, joined by ` — ` (U+2014). Else sibling `flow.name`. Else `<flow>`.

The documented payload (header `Book Appointment`, body `Schedule your visit with us.`, footer `Tap below to continue`, CTA `Book Now`) renders:

`[outro atendimento] Book Appointment — Schedule your visit with us. — Tap below to continue — Book Now`

### Template echo

Sibling `template` present: header media marker, body with `{{n}}` replaced from send-time text parameters (1-based), footer, then each button as `{label}` or `{label}: {url}`. The documented `summer_sale_2026` echo renders:

```text
[outro atendimento] <image>
Hi Maria, enjoy 25% off!
Limited time
Shop Now: https://example.com/summer2026
```

Sibling `template` absent: `[outro atendimento] template:{name}` plus ` | `-separated values in send-time component order. The same parameters without the sibling object render:

`[outro atendimento] template:summer_sale_2026 <image> | Maria | 25% | summer2026`

No Graph request in either case.

### Status (`value.standby.statuses`)

HTTP 200. No `context_text`. No `WriteMsg`. No `WriteMsgStatus`. `not_owner` uses `recipient_id`, else `recipient_user_id`, and the status timestamp. No `wac:standby:seen:*` key.

### Ownership ordering

Same Lua script as PR 335 (`wa_thread_owner.go`): incoming Unix seconds `<=` stored `ts` does not change `state` or `ts`. A greater timestamp sets `not_owner`. A missing key is created. The transcript line is still appended when the render rules say so.

### Flag off

HTTP 200, info `ignoring standby webhook`, no handover event, no ownership write, no seen key, no queued msg. Body still omitted from the channel log.

### Errors

| Failure | HTTP | Seen key | Context row from this attempt |
|---------|------|----------|-------------------------------|
| Ownership Redis error, or `SET` error other than “not claimed” | 400 | absent | none |
| `WriteWAConversationHandover` returns an error | 400 | deleted if this item claimed it | none (spool may still hold the event when the writer returns nil; that is not this row) |
| Duplicate id (`SET NX` not claimed) | 200 | unchanged | no new append |
| Missing signature | 401 | absent | none (existing `receiveEvent`) |

## Channel log

`channelHandleWrapper` still writes a log when standby returns no events (`Request Ignored`, or `Channel Error` when the handler returns an error). The HTTP body in `request` is `[standby body omitted]`. Request line and headers stay. The logrus `request` field on the wrapper’s error and panic lines uses the same bytes.

A `field=messages` request is not marked and still logs its body (`Hello World` in `helloWAC.json`).

## Internal Redis (not a public contract)

| Key | Written when |
|-----|----------------|
| `wac:standby:seen:{channel_uuid}:{message_id}` | Transcript line claimed, TTL 24h. Deleted if the context write returns an error. |
| `wac:thread_owner:{channel_uuid}:{urn.Identity()}` | Flag on, `WAC`, usable timestamp. State `not_owner`. |

## Consumers

Meta: 200 on the success rows above, 400 when a flagged write or ownership call fails. Mailroom: still one pending `context_text`, which may be longer. Operators: standby channel logs no longer contain the body, including while the flag is false.
