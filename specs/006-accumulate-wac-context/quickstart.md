# Quickstart: Accumulate WAC Context

Validation guide for the slice in [spec.md](./spec.md). Implementation details belong in `tasks.md`. Row rules: [data-model.md](./data-model.md). Webhook and writer contract: [contracts/wac-pending-context.md](./contracts/wac-pending-context.md).

## Prerequisites

- Postgres for the rapidpro suite: `postgres://courier:courier@localhost:5432/courier_test` (`POSTGRES_HOST` overrides the host). The suite loads `backends/rapidpro/schema.sql`.
- Redis on `localhost:6379` is started by that suite (`REDIS_HOST` overrides the host) and is not asserted by this feature.
- Handler tests need no Postgres. They use `MockBackend`.
- From repo root, with the module cache available.

## Tests that prove the feature

```bash
# Pending-row append, consumed-row insert, truncation, metadata.
# Testify method filter: TestMsgSuite/<method>
go test ./backends/rapidpro/ -count=1 -run 'TestMsgSuite/TestWriteWAConversationHandover'

# Messages context before WriteMsg, absent context, writer failure, WCD negative.
# Standby copies still do not queue a msg.
go test ./handlers/facebookapp/ -count=1 -run 'TestReceiveWACMessagesContext|TestReceiveWACStandbyContext|TestReceiveWACHandover'
```

Expect both green. Named `-run` patterns are the intended test names from the spec and this plan. If implement names a new method differently, the cases below still have to exist.

`TestReceiveWACHandover` is the existing `control_passed` flow. It must stay green after the helper extraction (persist error still fails that path; a rendered summary still records one handover event).

## What those tests must show

**Postgres** (`wa_conversation_handover_test.go`), each case deleting this channel’s handover rows first:

| Case | Expected pending row |
|------|----------------------|
| `"First summary"` then `"[user] hi"` | one row, `context_type=history`, `context_text` = `First summary\n[user] hi` |
| summary then summary | `context_type=summary`, texts joined by one newline |
| combined text longer than 32000 characters | `[truncated]` plus the last 32000 characters |
| first row `consumed_on` set, then a new write | consumed row unchanged; new pending row holds only the new text |
| first event sets `previous_owner_app_id`, second leaves it empty | stored app id kept; `context_payload` and `occurred_on` match the second event |

**Handler** (`TestReceiveWACMessagesContext`):

| Body | Channel | Expected |
|------|---------|----------|
| `testdata/wac/messagesContextSummaryWAC.json` | `WAC` | HTTP 200, one handover event then one queued msg (`BackendCalls` order), summary text from the fixture, URN `whatsapp:5678`, `OccurredOn` from `1454119029`. `EnableWACStandbyContext` left false. |
| `testdata/wac/helloWAC.json` | `WAC` | HTTP 200, zero handover events, one queued msg |
| summary text whitespace only | `WAC` | HTTP 200, zero handover events, one queued msg |
| summary fixture with `SetErrorOnWAHandover(true)` | `WAC` | HTTP 200, one queued msg, log `event=wac_pending_context_write_failed` and `channel_uuid` set, no context text and no URN on that entry |
| summary fixture | `WCD` via `newWACDemoHandler` on `/c/wcd/receive` | one queued msg, zero handover events |

`TestReceiveWACStandbyContext` with the flag on still persists the standby summary and `LenQueuedMsgs()==0`. With the flag off it still ignores the webhook.

## Expected log event (no URN, no context text)

| Event | When |
|-------|------|
| `wac_pending_context_write_failed` | `WAC` `field=messages`, renderable context, `WriteWAConversationHandover` returned an error. Fields: `event`, `channel_uuid`, `external_id`. |

No success log is required on the messages path.

## Done for this repo's tests

- AC-01…AC-09 mapped in spec Test Strategy are green.
- `go test -p=1 ./handlers/facebookapp/` and `go test -p=1 ./backends/rapidpro/` do not regress existing WAC receive tests or the handover writer tests.
- No file under a migration path is added. `schema.sql` constraints `chk_wa_conv_handover_context_type` and `uq_wa_conv_handover_pending` are unchanged.
