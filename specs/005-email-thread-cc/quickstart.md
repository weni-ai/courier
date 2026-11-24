# Quickstart: Email Thread Cc

Validation guide for this repository. Payloads and field rules: [contracts/email-cc.md](./contracts/email-cc.md). List rules: [data-model.md](./data-model.md).

## Prerequisites

- Go 1.24, from `go.mod`.
- No email-proxy and no SMTP. Send tests stub the proxy with `httptest`.
- `go test ./handlers/email/` does not need Postgres.
- The rapidpro lookup test needs the suite database: `postgres://courier:courier@localhost:5432/courier_test` (`POSTGRES_HOST` overrides the host). CI already provides it. Skip that package locally when the database is down; do not skip it in CI.

## Run

From the repository root:

```bash
go test ./handlers/email/ -count=1
```

With the test database up:

```bash
go test ./backends/rapidpro/ -count=1 -run 'TestMsgSuite/TestLookupLastMsgByContactURN'
```

CI runs `go test -p=1 ./...` (`.github/workflows/ci.yml`). Both packages are included.

## Receive outcomes

`TestHandler` in `handlers/email/email_test.go`. Channel username in `testChannels` is `support@company.com`.

| Case | Seed | Assert |
|------|------|--------|
| First message with `cc` | none | URN is `urnWithTag` of the opener. `Name` is the opener. `Text` equals `body`. `Metadata.email.cc` has the payload addresses minus `support@company.com` and the opener. |
| Copy reply | parent on a `+wt-` URN with `external_id` equal to `in_reply_to` | URN is that stored URN, not `urnWithTag` of the new `from`. `Text` is `{from}:\n\n{body}`. `Name` is the opener's real address. `email.cc` still contains the earlier addresses and includes this author when they were not already listed. One queued message. |
| Opener replies again, `cc` a subset of the snapshot | latest row on the central URN already has the longer list | `Text` equals `body`. `email.cc` still has every earlier address. |
| Author was only a `cc` elsewhere, no stored Message-ID | a different thread lists them in `cc` | URN is `urnWithTag` of this sender. They are the contact of the new conversation. |
| Blocked copy author | `SetEmailMailboxBlocked` on `from`, `in_reply_to` cites the thread | Same ignored response as "Receive from blocked mailbox is ignored". No queued message. |
| `cc` is a string, not an array | none | HTTP 400. No queued message. |
| Latest-message lookup returns an error | `PrepBackend` sets `LastMsgByContactURNErr` | HTTP 400. No queued message. |

These existing cases stay green, with the two URN expectation updates from [research.md](./research.md) decision 3:

- plain receive, message-id only, full threading, bracket normalization, deduped references
- legacy plain-mailto reuse, including case-insensitive `from` and References-only
- "Reply that cites another mailbox keeps a new thread URN"
- blocked mailbox on a brand-new thread

## Send outcomes

`TestSending` in `handlers/email/email_test.go`, plus one direct `SendMsg` test for the lookup failure.

| Case | Seed on the new lookup | Assert |
|------|------------------------|--------|
| Latest row has a longer `cc` than the parent | parent (by `response_to_external_id`) has a short list; a newer row on the same URN has the long list | `RequestBody` `to` is the real opener, no `+wt-`. `cc` is the long list. `subject` and `references` still come from the parent. `StatusMetadata.email.cc` equals the long list. Status `W`. |
| Latest row has no `cc` | not seeded, or seeded without `cc` | `RequestBody` has no `cc` key. Existing exact-body cases stay. |
| Lookup returns an error | `LastMsgByContactURNErr` set | Status `E`. The `httptest` handler is not called. `StatusMetadata` is empty. |

Existing send cases stay green: subject from the first line, attachments, proxy 500 → status `E`, returned `message_id`, `Re:` not doubled, ResponseToID, last-message threading fallback, `+wt-` stripped on `to`, a real `+alias` preserved.

Proxy 500 does not write `email.cc` on that attempt. Assert `StatusMetadata` empty on that case if it is not already implied by status `E`.

## Lookup outcome

`TestLookupLastMsgByContactURN` in `backends/rapidpro/backend_test.go`.

Update the highest visible fixture row for a URN so `external_id` is empty and `metadata` contains `email.cc`. `LookupLastMsgByContactURN` returns that row and its `cc`. `LookupLastMsgWithExternalID` does not return it. Use `scanDBMsgWithMetadata`'s text-metadata path, the same way `TestLookupLastMsgWithExternalIDWithTextMetadata` does. Restore or isolate the row so later tests in the suite still see the fixture.

## Not validated here

- SMTP `Cc` on a real mailbox. That is email-proxy, after the contract in [contracts/email-cc.md](./contracts/email-cc.md).
- The 242.9 req/s peak. The check is the `LIMIT 1` query and a single `MakeHTTPRequest`, stated in [plan.md](./plan.md).
