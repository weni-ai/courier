# Research: Record WAC Standby

All Technical Context items were already decided in [spec.md](./spec.md). This file records those decisions in the plan format and the repository patterns they reuse. No `NEEDS CLARIFICATION` remains.

## Decision 1: Omit the standby body in `channelHandleWrapper`

- **Decision**: In `server.go` `channelHandleWrapper`, after the 30s timeout context is created and before `handlerFunc`, store `*channelLogBodyControl` on that context (`omit bool`, unexported type, unexported context key). Export `MarkChannelLogBodyOmitted(ctx)` in package `courier`. It sets `omit` when the pointer is present and does nothing when it is not (handler unit calls that skip the wrapper).
  The dumped `request` bytes stay as `httputil.DumpRequest` produced them until log time. `omitChannelLogBody(dump []byte) []byte` splits on the first `\r\n\r\n`. The part before it (request line and headers) is kept; the part after it becomes `[standby body omitted]`. If the separator is missing, the whole dump becomes that placeholder, so a body we failed to split is not stored.
  Call that helper from the panic `defer` before the logrus `request` field, and once on the normal path immediately after `handlerFunc` returns, assigning back to `request`, before the error logrus line and before every `NewChannelLog`. The handler calls `courier.MarkChannelLogBodyOmitted(ctx)` as the first statement of `field == standby`, before the flag check.
- **Rationale**: `DumpRequest` runs before the handler (spec Current State, `server.go` lines 323–328). The handler cannot un-dump. A pointer is visible to the wrapper without parsing WhatsApp JSON in `server.go`. The panic defer runs instead of the code after `handlerFunc`, so redaction has to be inside the defer too. AC-12.
- **Alternatives considered**: String-search `"field":"standby"` in the wrapper (false positive if that text is inside a normal message); skipping the channel log entirely (operators lose the status line); redacting only `NewChannelLog` and leaving the logrus `request` field (the body still lands in application logs).

## Decision 2: Parse `value.standby` in a new file, leave the messages loop alone

- **Decision**: Add `Standby json.RawMessage \`json:"standby,omitempty"\`` on the anonymous `value` struct in `moPayload` (`handlers/facebookapp/facebookapp.go`). Unmarshal that raw message in `handlers/facebookapp/wa_standby.go` into a dedicated `wacStandbyBody`:
  - `contacts` using the existing `wacHandoverContact` shape (name, `wa_id`, `user_id`)
  - `messages[]` with `id`, `from`, `from_user_id`, `timestamp`, `type`, `text.body`, and the media objects already modeled by `wacMedia` / `wacSticker` (`image`, `audio`, `video`, `document`, `voice`, `sticker`). Also `button.text` and interactive reply `title` fields needed for the “other type” rule.
  - `message_echoes[]` with `id`, `timestamp`, `message` (`to`, `recipient`, `type`, `text`, `template`, `interactive`), plus sibling `template` and `flow`.
  - `statuses[]` with `id`, `status`, `timestamp`, `recipient_id`, `recipient_user_id`.
  Do not point `standby.messages` at `change.Value.Messages`. The `field=standby` branch `continue`s before `for _, msg := range change.Value.Messages`.
- **Rationale**: The nested object is discarded today because `moPayload` has no `standby` field. Reusing the messages loop would call `WriteMsg` and `resolveMediaURL` (AC-06, AC-09). A raw message keeps the echo shape out of the giant anonymous struct.
- **Alternatives considered**: Duplicate the inbound message struct by embedding it (echo `message` is a send-API body, not an inbound webhook message); fall through when `value.messages` is also set (AC-09).

## Decision 3: Branch order inside `field=standby`

- **Decision**: Replace the body of the existing standby `if` (`facebookapp.go` lines 1147–1168) with:
  1. `MarkChannelLogBodyOmitted(ctx)`.
  2. Flag false: today’s `ignoring standby webhook` info log and `continue`. No Redis, no `WriteWAConversationHandover`.
  3. Flag true and `channel.ChannelType() != "WAC"`: today’s `persistWAHandoverContext` only, then `continue`. `WCD` does not gain transcript lines or `not_owner`.
  4. Flag true and `WAC`: `processWACStandby(...)` in `wa_standby.go`, then `continue`.
  `processWACStandby` walks echoes and shopper messages first, then statuses, then the legacy `persistWAHandoverContext` when `renderWAHandoverContextText` would succeed. A non-nil error from any of those returns immediately (`return nil, nil, err` from `processCloudWhatsAppPayload`), which `WriteError` turns into HTTP 400. When nothing was appended and no status applied, the info string stays `standby without conversation context`.
  Info strings added on success, with no message text: `standby context recorded` if any line was written, `standby status recorded` if any status applied. The legacy path may still add `wa conversation handover persisted`.
- **Rationale**: AC-11 and AC-13. Items with ids run before the legacy summary so a failed line does not append an undeduped summary first, and a failed summary does not run before a line that can be deduped. Flag off stays the PR 335 standby rule (no ownership write).
- **Alternatives considered**: One code path for WAC and WCD (AC scope is WAC); treating “no conversation_context” as the end of the webhook (drops `standby.messages`).

## Decision 4: Ownership call, then dedup, then the existing writer

- **Decision**: For each shopper message and echo, in payload order:
  1. URN: shopper `urns.NewWhatsAppURN(from)` when `from` is set, else `from_user_id`. Echo: `message.to`, else `message.recipient`. Empty result: log `wac_standby_skipped` `reason=no_urn` and continue. Do not fail the webhook.
  2. Timestamp: `strconv.ParseInt` on the item timestamp. Empty string uses `entry.Time` (including `0`). A non-empty parse error does not call Lua; the line’s `OccurredOn` is `time.Unix(entry.Time, 0).UTC()`. A usable timestamp (parsed or empty-fallback) calls `applyWACThreadOwner(rc, channel.UUID(), urn, ts, wacThreadOwnerStateNone, wacThreadOwnerModeSet, "standby")`. Borrow the Redis connection and `Close` it before the Postgres write. Redis error: return it. No dedup key yet.
  3. Render. `(text, false)`: do not append and do not `SET`.
  4. Empty message id: log `wac_standby_skipped` `reason=no_message_id` and do not append.
  5. `SET wac:standby:seen:{channelUUID}:{messageID} 1 NX EX 86400`. `redigo`: `redis.String` on that command; `"OK"` means claimed; `redis.ErrNil` means already present (skip the append). Any other error returns from the handler (same as a Redis ownership error: non-200, key not left half-claimed).
  6. `persistRenderedWAHandover(ctx, channel, urn, contactName, courier.WAHandoverContextHistory, text, nil, occurredOn, nil)`. Contact name is `wacHandoverContactName` on the standby contacts. Nil error: keep the key. Non-nil: `DEL` the key, log `wac_standby_context_write_failed` with `channel_uuid` and `external_id`, return the error.
  Statuses use steps 1–2 only (`recipient_id`, else `recipient_user_id`) and never `SET`. They do not call `WriteMsgStatus`.
  Constants in `wa_standby.go`: `wacStandbySeenKeyPrefix = "wac:standby:seen"`, `wacStandbySeenTTL = 24 * time.Hour`, `wacStandbyOriginShopper = "shopper"`, `wacStandbyOriginOther = "outro atendimento"`, `wacStandbyLogBodyOmitted` is the server placeholder `[standby body omitted]` (the handler does not format the log).
- **Rationale**: AC-08, AC-10, AC-11. `SET NX` before the write is the single winner. `DEL` on a non-nil write error is what lets Meta’s redelivery append. A nil return includes the existing spool success in `writeWAConversationHandover`, so the key stays. Holding a pool connection (`MaxActive` 8) across `backendTimeout` (20s) would block other Redis users at the declared peak, so each command closes before `WriteWAConversationHandover`.
- **Alternatives considered**: `CheckExternalIDSeen` (requires a `Msg`); `update_if_exists` (would not create `not_owner`); skipping the line when Lua returns `stale` (drops a real copy); one Redis connection for the whole webhook including the DB write.

## Decision 5: Render rules are pure functions

- **Decision**: `renderWACStandbyShopper` and `renderWACStandbyEcho` return `(string, bool)`. Prefix is `[{origin}] ` on the first line only. Later lines of the same message are separated by `\n` and do not repeat the prefix.
  - Text: trimmed `text.body`. Empty → `( "", false )`.
  - Media types `image`, `audio`, `video`, `document`, `voice`, `sticker`: `[{origin}] <type>` and nothing else. Ignore caption and id.
  - Flow (`message.type=interactive` and `interactive.type=flow`, or a sibling `flow` object): join non-empty header text, body text, footer text, and `action.parameters.flow_cta` with ` — ` (U+2014). If none, use `flow.name`. If that is empty, `<flow>`.
  - Template with sibling `template.components`: HEADER `format` `IMAGE`/`VIDEO`/`DOCUMENT`/`AUDIO` → that marker; HEADER/BODY `text` with `{{n}}` replaced by the nth text parameter of the matching send-time component (`n` starts at 1, array order). A parameter with `parameter_name` also replaces `{{parameter_name}}`. Unmatched `{{n}}` stays. FOOTER text is its own line. Each button is `{text}` or `{text}: {url}` after the same substitution on that button’s URL, aligned by button index with the send-time `button` component. Skip empty parts.
  - Template without sibling `template`: `[outro atendimento] template:{name}` plus, when any values exist, a space and the values joined by ` | `. Values follow send-time component order. Text parameters contribute `text`. Media parameters contribute `<image>` / `<video>` / `<document>` / `<audio>`. Name missing → do not append (`false`).
  - Any other type: button text or interactive reply title when non-empty; otherwise `[{origin}] <type>`.
  The documented summer-sale echo renders exactly:

  ```text
  [outro atendimento] <image>
  Hi Maria, enjoy 25% off!
  Limited time
  Shop Now: https://example.com/summer2026
  ```

  The same echo without the sibling object renders `[outro atendimento] template:summer_sale_2026 <image> | Maria | 25% | summer2026`. The documented flow renders `[outro atendimento] Book Appointment — Schedule your visit with us. — Tap below to continue — Book Now`.
- **Rationale**: AC-03 through AC-06. A pure function is what `wa_standby_test.go` can assert without Redis or HTTP. One append per message id matches the dedup key.
- **Alternatives considered**: Repeating the origin marker on every internal line (the spec says the first line carries it); calling Graph for a missing template (AC-05); putting the raw JSON on `context_payload` (phones and body in Postgres).

## Decision 6: Tests reuse the WAC seams that already exist

- **Decision**: Flow tests live in `TestReceiveWACStandbyTranscript` and the updated `TestReceiveWACStandbyContext` in `facebookapp_test.go`. They use `handlers.NewTestServer`, `handlers.TestHandlerRequest` on `/c/wac/receive` with `addValidSignatureWAC`, `testChannelsWAC[0]`, and `handlers.CaptureLogs`. Redis assertions use `threadOwnerHash` and `seedThreadOwner` from `wa_thread_owner_test.go`. The seen key is read with `GET` and `TTL` on the same pool. A second channel is `courier.NewMockChannel` with a new UUID, not a new entry in `testChannelsWAC`. `WCD` uses `newWACDemoHandler` and the UUID path, same as `TestReceiveWACMessagesContext`. Extend `assertNoWACOwnershipPII` with `wac_standby_context_write_failed` and `wac_standby_skipped`. Writer failure uses the existing `SetErrorOnWAHandover(true)`. Redis-down uses `mb.RedisPool().Close()` before the request, same as `TestSendWACThreadOwner`. No new `MockBackend` method and no Postgres test: the append SQL is unchanged.
- **Rationale**: AC-01 through AC-13 are handler-flow outcomes. `GetLastChannelLog` is already filled because `TestHandlerRequest` goes through `channelHandleWrapper`. `SetErrorOnWAHandover` already returns an error without spooling, which is the AC-11 path.
- **Alternatives considered**: A rapidpro test that inserts the line (the writer is not modified); a new `Backend` method for dedup (spec rejected that; media cache and thread owner already use `RedisPool()` from the handler).
