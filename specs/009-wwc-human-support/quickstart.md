# Quickstart: WWC human-support forwarding

## Prerequisites

- Go toolchain from `go.mod` (1.24).
- No Postgres, Redis, or live socket. `httptest` stands in for `POST /send`.

## Commands

```bash
go test ./handlers/weniwebchat/ -count=1
go test . -count=1 -run 'TestBestEffortAction'
```

`TestSending` and `TestActions` must still pass: unlabeled chat bodies and nameless `typing_start` stay the same, with no `Authorization` header.

## Receive

`RunChannelTestCases` against `receiveURL`.

| Callback | Result |
|----------|--------|
| Text plus `message.message_id` `3f1a8c0e-6b2d-4e5a-9c7d-1b0e4a6c8d20` | `ExternalID()` is that string |
| Same text, no `message_id` | `ExternalID()` empty |
| `message_id` length 65, or containing a space | message accepted, `ExternalID()` empty |

## Send

Privileged cases build `handlers.NewTestServer`, set `WwcHumanSupportIngressToken` to `test-token`, and call `SendMsg` or `SendAction`. Channel address in fixtures is `250788383383`. Channel UUID is `8eb23e93-5ecb-45ba-b726-3b064e0c568c`.

| Setup | HTTP | Status |
|-------|------|--------|
| `author` `ai_agent`, no name, socket 202 | no `Authorization`, body has `"author":"ai_agent"` | `MsgSent` |
| `author` `human_agent`, name `Julia`, socket 202 | bearer `test-token`, top-level name | `MsgSent`; channel log has neither `test-token` nor `Julia` |
| Product list with the same human-agent metadata | bearer and the two fields on the product body | `MsgSent` |
| `human_support_started`, `event_id` `evt-1`, `occurred_at_ms` 1730000000123, socket 202 | control JSON, no `type: message` | `MsgSent` |
| Same start, socket body `result: replay`, 202 | one POST | `MsgSent` |
| `human_support_ended` with a different `occurred_at_ms` | that number unchanged | `MsgSent` |
| `human_support_state` `open: true` | `"open":true`, no `message` object | `MsgSent` |
| `message_status` `read` without ids; `delivered` with one id | matching bodies, 202 | `MsgSent` |
| Empty token, privileged metadata | no request | `MsgFailed` |
| Name `Julia Silva`, 101-rune name, `author` `shopper`, `ai_agent` plus a name, `open: false`, `status` `sending`, `delivered` without ids, `event_id` length 129, `event_id` present and empty | no request | `MsgFailed` |
| Socket 401, 400, 409 | one request each | `MsgFailed` |
| Socket 403 | one request | `MsgErrored` |
| `message_status` 404, then 404, then 202 | three requests | `MsgSent` |
| `message_status` 404 three times | three requests | `MsgErrored` |
| Socket 500 twice then 202 | three requests | `MsgSent` |
| Socket 500 three times | three requests | `MsgErrored` |
| Send context already cancelled or expired | at most 3 requests | `MsgErrored` |

## Typing

| Setup | Body |
|-------|------|
| `TestActions`, no name, socket 200 | today's `typing_start`, `from` `ai-assistant`, no bearer |
| `SendAction`, name `Julia` | `typing_start` plus the name and the bearer |
| `SendAction`, `typing_stop` with and without a name | `type` `typing_stop`; bearer only when the name is set |
| Nameless `typing_start`, socket 500 | one `MakeHTTPRequest`, error returned, no bearer |

## Sender predicate

`TestBestEffortAction`:

- `typing_indicator` on WWC and on a non-WWC channel is best-effort.
- `typing_stop` is best-effort only for channel type `WWC`.
- `human_support_started` is not best-effort.

## Staging check

After deploy, with the token set and the socket flag still false, a normal WWC text send still returns 202 and the shopper receives it. Do not enqueue control events until the socket flag is on. Then one start, one shopper message whose `external_id` equals the socket `message_id`, one `delivered` or `read`, one named human-agent message, one named typing event, and one end.
