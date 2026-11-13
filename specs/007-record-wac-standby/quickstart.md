# Quickstart: Record WAC Standby

Validation guide for the slice in [spec.md](./spec.md). Row and Redis rules: [data-model.md](./data-model.md). Webhook outcomes: [contracts/wac-standby.md](./contracts/wac-standby.md).

## Prerequisites

- Redis on `localhost:6379` for handler tests (`MockBackend` uses it). No Postgres: this feature does not change the handover writer.
- From repo root, with the module cache available.
- No media server and no live Cloud API. An image or audio fixture must succeed without one.

## Tests that prove the feature

```bash
# Renderer: flow fallbacks, template with and without structure, media markers.
go test ./handlers/facebookapp/ -count=1 -run 'TestRenderWACStandby'

# Webhook → context line and/or Redis, channel log, flag off, writer failure, WCD.
go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACStandbyTranscript|TestReceiveWACStandbyContext|TestReceiveWACMessagesContext|TestReceiveWACHandover'
```

Expect both green. Named `-run` patterns are the intended test names. If implement names a method differently, the cases below still have to exist.

`TestReceiveWACMessagesContext` and `TestReceiveWACHandover` are the existing paths. They must stay green: messages still queue, `control_passed` still persists, and this change does not start calling `WriteMsg` from standby.

## What those tests must show

**Renderer** (`wa_standby_test.go`), no Redis:

| Input | Line |
|-------|------|
| Documented flow echo | `[outro atendimento] Book Appointment — Schedule your visit with us. — Tap below to continue — Book Now` |
| Flow with only a name | `[outro atendimento] Appointment Booking` |
| Flow with no text and no name | `[outro atendimento] <flow>` |
| Documented `summer_sale_2026` with sibling `template` | `[outro atendimento] <image>\nHi Maria, enjoy 25% off!\nLimited time\nShop Now: https://example.com/summer2026` |
| Same parameters, no sibling `template` | `[outro atendimento] template:summer_sale_2026 <image> \| Maria \| 25% \| summer2026` |
| Template name and no parameters | `[outro atendimento] template:summer_sale_2026` |
| Shopper image / audio | `[shopper] <image>`, `[shopper] <audio>` |

**Handler** (`TestReceiveWACStandbyTranscript`), signed `POST /c/wac/receive`, `testChannelsWAC[0]`, unless noted:

| Body | Flag | Expected |
|------|------|----------|
| `standbyShopperTextWAC.json` | on | HTTP 200, one history line `[shopper] Test standby message`, URN `whatsapp:5678`, `LenQueuedMsgs()==0`, `BackendCalls` has `wa_handover` and not `msg`, no msg status, no channel event |
| `standbyEchoTextWAC.json` | on | one line `[outro atendimento] Hello! Your order #12345 has shipped.` |
| `standbyEchoFlowWAC.json` | on | the flow line from the renderer table |
| `standbyEchoTemplateWAC.json` | on | the structured template line |
| `standbyEchoTemplateNoStructureWAC.json` | on | the name-and-values line |
| `standbyShopperImageWAC.json`, `standbyShopperAudioWAC.json` | on | markers only; test passes with no media `httptest` server |
| `standbyStatusWAC.json` | on | HTTP 200, zero handover events, `not_owner`, no seen key |
| shopper text, Redis seeded `owner` / ts `200`, item ts `100` | on | hash stays `owner` / `200`, line still appended |
| no seed, item ts `150` | on | `not_owner` / `150` |
| seeded `owner` / `100`, item ts `150` | on | `not_owner` / `150` |
| second message id, same ts `150` | on | `ts` stays `150`, second line appended |
| same shopper body posted twice | on | one handover event; `TTL` of `wac:standby:seen:{uuid}:{id}` in `(0, 86400]` |
| same body, second WAC channel | on | one event on that channel |
| shopper body, `SetErrorOnWAHandover(true)` then cleared and posted again | on | first HTTP 400, seen key absent, zero events; second HTTP 200 and one line |
| shopper body, Redis pool closed | on | HTTP 400, zero handover events |
| non-integer item timestamp, seeded `owner` | on | `owner` unchanged, line appended |
| blank text body | on | zero handover events |
| shopper body | off | HTTP 200, `ignoring standby webhook`, zero events, seeded `owner` / `200` unchanged |
| shopper body, flag on and flag off | either | `GetLastChannelLog().Request` contains `[standby body omitted]` and not `Test standby message` |
| writer error | on | logrus `request` also omits the body; `event=wac_standby_context_write_failed` has `channel_uuid` and `external_id`, no URN |
| `testdata/wac/helloWAC.json` | off | channel log still contains `Hello World` |
| shopper body on `WCD` | on | zero handover events, zero queued msgs |

`TestReceiveWACStandbyContext` flag-on (`handoverStandbyContextWAC.json`) still persists the summary and now also sets `not_owner` for `whatsapp:5678` at `1454119029`. Flag-off still ignores the webhook and does not change a seeded hash. `LenQueuedMsgs()` stays 0. The writer-error case stays HTTP 400.

## Expected log events (no URN, no line text)

| Event | When |
|-------|------|
| `wac_thread_owner_applied` | Lua applied or returned `stale`. `source=standby`. Existing fields only. |
| `wac_standby_context_write_failed` | Transcript `WriteWAConversationHandover` returned an error. |
| `wac_standby_skipped` | `no_urn` and `no_message_id` skip the append. `bad_timestamp` skips only the Lua ownership update; the line is still appended with `entry.time`. |

Run `assertNoWACOwnershipPII` on the standby logs, including those two new event names.

## Done for this repo's tests

Both commands above pass. `field=messages` and `control_passed` tests named in the second command still pass. No Mailroom process is required.
