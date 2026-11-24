# Engineering Specification: Email Thread Cc

**Repository**: courier — backend

**Feature Branch**: `[005-email-thread-cc]`

**Created**: 2026-11-11

**Status**: Draft

## Origin Without Product Spec

- Product Spec: none
- Problem: An email conversation opened by one person can include other addresses in copy. Those addresses never receive the agent's reply, and each person in copy who replies on the same thread is given a new URN. The conversation contact must stay the person who opened the email, at the mailto address that already forms the central URN (the `+wt-` tag of the thread anchor).
- Acceptance criteria:
  - A message that cites a Message-ID already stored on a `+wt-` thread is written on that thread's central URN even when `from` is a different mailbox. The stored text is `{from}:\n\n{body}`. The contact name stays the opener's address. The replier gets no new URN and that address is not added as a URN of the contact.
  - When `from` is the central contact, the stored text is the body as received.
  - The copy list lives in `msgs_msg.metadata.email.cc`, the same object that already holds `subject`, `in_reply_to` and `references`. It starts with the first message's `cc` minus the channel mailbox and the central contact, and it only grows: later `cc` addresses and any author who is not the central contact are appended. Nobody already on the list is removed. The channel mailbox and the central contact are never entries. Contact fields are not used.
  - The agent send posts `to` as the real address of the central URN, without the `+wt-` tag, and `cc` as the list stored on the latest message of that URN. The wired outbound message stores that same list. Replying to an older message does not drop someone who joined later.
  - An email that cites no Message-ID stored in this service opens a new conversation, and the sender is that conversation's initial contact.
  - Blocking stays by the mailbox of the person who actually sent, and it runs before the message is attached to a thread.
  - Threads with no copy, and the current threading of Message-ID, In-Reply-To, References and a `Re:` subject, stay as they are. Absent or empty `cc` on the send keeps today's JSON.

## Scope in This Repository

**This repository implements**: identity and copy-list behavior for the email channel (`EM`) inside Courier. Inbound `POST /c/em/{uuid}/receive` chooses the central URN, prefixes the body when the author is not that contact, and stores the accumulated copy list on the message. Outbound `SendMsg` reads that list from the latest message of the URN and sends it to the email-proxy. Courier does not read MIME.

**Out of scope here**:

- email-proxy. It owns parsing the mail and the SMTP headers. The contract this repository needs from it is stated below; this repository does not implement it.
- RapidPro and Mailroom. They already deliver `msgs_msg.text` and `metadata` to the agent UI and already queue the outbound msg. No change to contact fields, contact URNs, or the flow engine is required from them.
- Merging contacts that were already split before this deploy. A person who already received their own URN for an older reply stays a separate contact. Only mail received after deploy follows the rules above.
- Other channel handlers.

**Depends on**: email-proxy, for the contract in Contracts. Courier already calls it with `EMAIL_PROXY_URL` (`/send`) and `EMAIL_PROXY_AUTH_TOKEN`.

## Current State

- `handlers/email/email.go` `receiveEvent` decodes `moPayload` (`from`, `to`, `body`, `subject`, `attachments`, `message_id`, `in_reply_to`, `references`). There is no `cc` field. `from` and `body` are required.
- Block runs first: `Backend.IsEmailMailboxBlocked(ctx, channel, payload.From)` (`backends/rapidpro/contact.go` `isEmailMailboxBlockedSQL`). A blocked mailbox returns `blocked contact sending message` and writes nothing. The check matches the plain `mailto:` identity and any `mailto:{local}+wt-{8 hex}@{domain}` variant.
- `contactURNForInbound` walks `replyParentIDs` (In-Reply-To, then References newest to oldest) via `LookupContactURNByExternalID`. It returns a stored URN only when that URN has no `+wt-` tag and the real mailbox equals `from` (legacy plain mailto). A stored URN that already has `+wt-` is not adopted. `buildContactURN` then rebuilds `from` plus the thread anchor (`threadAnchor`: oldest Reference, else In-Reply-To, else own Message-ID) as `{local}+wt-{sha1(anchor)[:8]}@{domain}`. A different `from` that cites the same anchor therefore becomes a new contact. `handlers/email/email_test.go` pins this, including "Reply that cites a threaded contact keeps the thread URN", which expects the rebuilt tag rather than the URN stored on the parent row.
- The incoming msg is `NewIncomingMsg(channel, urn, payload.Body).WithContactName(payload.From)`. `contactForURN` writes that name only when the contact name is still empty.
- `buildThreadMetadata` stores `metadata.email` as `emailThreadMetadata`: `in_reply_to`, `references`, `subject`. The block is omitted when all three are empty. Nothing is written to `contacts_contact.fields`.
- `SendMsg` posts JSON to `{EmailProxyURL}/send` with bearer `EmailProxyAuthToken`, `utils.MakeHTTPRequest` (60s client timeout, no in-handler retry). `to` is `realAddressFromURNPath` (the `+wt-` tag stripped). `buildReplyContext` rebuilds In-Reply-To, References and the `Re:` subject from the parent (`response_to_external_id`, else `response_to_id` via `LookupMsgByID`, else `LookupLastMsgWithExternalID`). On HTTP 2xx the status is wired, the proxy `message_id` is saved with `SetExternalID` when it fits `maxExternalIDLength` (255), and `mergeEmailMetadata` writes the email block onto the outbound row. Non-2xx leaves status errored and does not write that metadata.
- `LookupLastMsgWithExternalID` (`backends/rapidpro/msg.go` `selectLastMsgWithExternalIDSQL`) is the latest visible (`visibility = 'V'`) message on the channel for the URN identity that has a non-empty `external_id`. A message whose Message-ID was longer than 255 is stored without `external_id` and is invisible to this query. It is the wrong source for the copy list.
- Route registration: `POST /c/em/{uuid}/receive` in `Initialize`. `server.channelHandleWrapper` resolves the channel with `GetChannel` and puts a 30s timeout on the request. Outbound auth is the bearer token; inbound auth is the channel UUID.
- Flow tests: `TestHandler` (`RunChannelTestCases`) and `TestSending` (`RunChannelSendTestCases`, `seedParents`).

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `handlers/email/email.go` (`moPayload`, `emailThreadMetadata`, `receiveEvent`, `contactURNForInbound`, `buildThreadMetadata`, `buildOutboundThreadMetadata`) | modified | Accept `cc`, adopt the central URN, prefix the body, accumulate and persist the list |
| `handlers/email/email.go` `SendMsg` | modified | Send `cc` from the latest message of the URN and store that same list after a wired send |
| `courier.Backend` and `backends/rapidpro` | modified | New latest-message read that is not filtered by `external_id` |
| `MockBackend` in `test.go` | modified | Same read for the handler tests |
| `handlers/email/email_test.go` | modified | Flow coverage for copy identity, body prefix, list growth, send `cc`, and the failure paths |

### Flow

Inbound, `receiveEvent`:

1. Decode JSON. `from` and `body` stay required. `cc` is optional. Omitted, JSON `null` and `[]` all mean no addresses on this message.
2. Block on `payload.From` via `IsEmailMailboxBlocked`, before URN resolution and before `WriteMsg`. Unchanged response when blocked.
3. Resolve the URN with the rules below.
4. Contact name passed to `WithContactName` is `from` when `from` equals the central contact's real address (case-insensitive), otherwise the central contact's real address (`realAddressFromURNPath` of that URN). For a new thread that is the sender. For a copy reply it is the opener, so an empty contact name cannot be filled with the copy author's address. `contactForURN` still updates the name only when it is empty. The first rule keeps the existing legacy case-only difference (`Fulano123@Gmail.com`) unchanged.
5. Body: if `sameEmailAddress(from, real address of the URN)`, text is `payload.Body`. Otherwise text is `strings.TrimSpace(from) + ":\n\n" + payload.Body`. The original body is not trimmed. Example stored text:

```
fulano_de_tal@email.com:

olá a todos estamos aguardando um prazo para a solução, por favor me manter informado.
```

6. Load the copy list already stored on the latest visible message of this channel and URN (see the new lookup). No row, or a row whose `metadata.email.cc` is absent, means the previous list is empty. A lookup error fails the receive through `WriteAndLogRequestError` and writes nothing, so a partial list is not persisted.
7. Accumulate, then `WithMetadata` with the existing email object plus `cc` when the list is non-empty. `buildThreadMetadata` also returns the object when `cc` is the only populated field.
8. `WriteMsgsAndResponse` as today. Do not call `AddURNtoContact` for `from` or for any `cc` entry.

URN resolution, still in `replyParentIDs` order. The message's own Message-ID is not a parent.

- `LookupContactURNByExternalID` error: fail the receive. Do not guess a URN.
- No row: try the next id.
- The stored URN's path matches `threadTagPattern` (`+wt-` and 8 hex digits): that URN is the central URN of the thread. Return it immediately, whatever `from` is. Do not call `buildContactURN`.
- The stored URN has no `+wt-` tag (legacy plain mailto) and `sameEmailAddress` matches `from`: return it. Unchanged.
- Legacy URN, different mailbox, and this id is the direct In-Reply-To: stop the walk. Do not follow older References onto that contact. Fall through to `buildContactURN`. Unchanged.
- Legacy URN, different mailbox, and this id is only an older Reference: keep walking. Unchanged.
- Nothing resolved: `buildContactURN` as today. The sender is the initial contact of the new conversation. A person who was only in copy on another thread, and whose new mail cites no stored Message-ID, gets this new URN.

Copy list accumulation. One function, used only on the inbound path. Order is part of the contract:

1. Start from the previous list, order preserved.
2. Append each `payload.cc` entry that survives normalization and is not already present.
3. Then, if `from` is not the central contact, append `from` under the same rules.

Normalization of one address: trim; strip a `+wt-` tag with `realAddressFromURNPath`; drop it when it is not a single mailbox (exactly one `@`, non-empty local part, non-empty domain, no whitespace); drop it when it equals the channel mailbox or the central contact, compared with `sameEmailAddress`. The channel mailbox is the channel `username` config (`courier.ConfigUsername`) and, when set, `payload.To`. Dedup is case-insensitive and keeps the first spelling. Courier does not parse display names or MIME; `from` and `cc` are already bare addresses in this JSON. An entry that still contains spaces or angle brackets is dropped, and the message is still accepted.

Outbound, `SendMsg`:

1. `buildReplyContext` is unchanged. In-Reply-To, References and the subject still come from the parent being replied to, not from the latest message.
2. Separately, load the latest visible message of `msg.URN()` on this channel. Its `email.cc` is the list to send. Not-found or a missing/empty `cc` omits the JSON field. Any other lookup error returns status errored and does not call the proxy, so a failed read cannot go out as an empty `cc` that drops recipients.
3. `to` stays `realAddressFromURNPath(msg.URN().Path())`. `cc` is that list, not recomputed from the parent. The encoder stays `SetEscapeHTML(false)`.
4. On HTTP 2xx only, `buildOutboundThreadMetadata` includes the same `cc` list beside the threading fields, and `mergeEmailMetadata` writes it. On transport error or non-2xx, metadata is not updated, so the worker retry reads the previous latest row again.

### Rejected Alternatives

- Rebuild the URN from the new `from` plus the same anchor. That is today's behavior, and it is what creates a contact per copy author. The tag would match the thread but the local part would not, so the URN would differ.
- Store the copy list on `contacts_contact.fields` or as extra contact URNs. The requirement is that the list lives on the message and that a copy author does not become a URN of the contact.
- Read `cc` from the parent being replied to. An agent reply to an older message would drop someone who was appended later. The latest row of the URN is the snapshot that already contains them.
- Union `email.cc` across every message of the thread. The latest snapshot is the source the send path is required to use. A full scan would hide a bug that stopped updating that snapshot.
- Put the author line in a metadata field and leave `text` untouched. The agent reads `msgs_msg.text`. The address line has to be the first line of that text.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| Inbound `POST /c/em/{uuid}/receive` JSON | modified, additive optional `cc` | minor | yes — omitted, `null` and `[]` mean this message contributes no copy addresses; unknown fields are already ignored by the previous binary |
| Outbound `POST {EMAIL_PROXY_URL}/send` JSON | modified, additive optional `cc` | minor | yes — the field is omitted when the list is empty, so the bytes match today's body; a non-empty list is the only new shape |
| `msgs_msg.metadata.email` | modified, additive `cc` | minor | yes — `encoding/json` on `emailThreadMetadata` ignores the unknown key; previous Courier keeps threading and simply does not send `cc` |
| `msgs_msg.text` for a non-central author | behavior of new rows only | minor | yes — column type unchanged; rows already stored are not rewritten; no field is removed, so there is no deprecation window |

**Consumers affected**: email-proxy (must implement the contract below before Courier starts sending a non-empty `cc`); Mailroom and the agent UI, which display `msgs_msg.text` and therefore show the author line. Release note in `WENI-CHANGELOG.md`.

### email-proxy contract (not implemented in this repository)

Receive body gains:

```json
{
  "from": "opener@example.com",
  "to": "support@company.com",
  "cc": ["fulano_de_tal@email.com", "other@example.com"],
  "subject": "Pedido",
  "body": "texto",
  "message_id": "<id@host>",
  "in_reply_to": "<parent@host>",
  "references": ["<root@host>"]
}
```

`cc` is a list of bare mailbox addresses. Absent, `null` or empty means this message has no copy header.

`POST /send` accepts the same optional `cc`. The proxy sets the SMTP `Cc` header to those addresses and sets `To` only to the central contact (`to`). Absent or empty `cc` keeps the proxy's current send, which has no `Cc` header.

### Courier send body when the list is non-empty

`to` has no `+wt-` tag. `cc` is the accumulated list. Threading fields are unchanged.

```json
{
  "uuid": "00000000-0000-0000-0000-000000000000",
  "from": "support@company.com",
  "to": "opener@example.com",
  "cc": ["fulano_de_tal@email.com"],
  "body": "resposta do atendimento",
  "subject": "Re: Pedido",
  "channel_uuid": "8eb23e93-5ecb-45ba-b726-3b064e0c56ab",
  "in_reply_to": "<parent@host>",
  "references": ["<root@host>", "<parent@host>"]
}
```

### Metadata object

```json
{
  "email": {
    "subject": "Pedido",
    "in_reply_to": "<parent@host>",
    "references": ["<root@host>"],
    "cc": ["fulano_de_tal@email.com"]
  }
}
```

`cc` is omitted when empty, including on outbound metadata, so existing exact-JSON send tests stay valid.

## Data Model and Migrations

- **`msgs_msg.metadata.email.cc`**: JSON array of strings on the message row, inbound and outbound. It is not a column and not a contact field. Each new message of the URN stores the list as of that message. The latest visible row (`visibility = 'V'`, highest `id`, both directions) is the list the next send uses.
- **`msgs_msg.text`**: for a non-central author, the prefixed body above. The central author's text is unchanged.
- **`msgs_msg.contact_urn_id`**: copy replies point at the central URN's existing `contacts_contacturn` row. No new URN row is inserted for the copy author.
- **Contact name**: still the opener's mailbox, because the name written on a copy reply is the central real address and `contactForURN` does not overwrite a non-empty name.

**Migrations**: none. `metadata` is already JSON (`jsonb` in production, text in some tests). The previous binary unmarshals `emailThreadMetadata` and ignores `cc`. Rollback does not need a down migration. After rollback, new mail follows the old per-sender URN rule again; rows written by this version keep their text and their `cc` key, which the old binary does not read. There is no backfill of contacts split before deploy.

The new read is the same shape as `selectLastMsgWithExternalIDSQL` without the `external_id` predicates: channel id, contact URN identity, `visibility = 'V'`, `ORDER BY m.id DESC LIMIT 1`. No new index. The external-id lookup used for threading stays as it is.

## Non-Functional Requirements

### Peak Load

This change adds no HTTP hop and no new request class. It adds at most one indexed `msgs_msg` read on receive and one on send.

The ceiling it must sustain is the measured Courier inbound peak of **242.9 req/s** (1-minute rate) at 2026-10-28 14:06 UTC, from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}` over 2026-10-07 to 2026-11-06. Istio has no path label, so `/c/em/{uuid}/receive` and email `SendMsg` are a subset of that peak, not a separate seasonal class. The extra read sits inside the existing 30s receive deadline and the existing 60s send client timeout. Email volume follows support load inside that same window, including campaign spikes already covered by the service peak.

### Observability

New logs are structured logrus fields. They carry `channel_uuid` and, on the send path, `msg_id`. They do not carry `from`, `cc`, contact names, URN paths or message text. A dropped `cc` entry is counted as an integer field `cc_dropped` on the receive, with the channel UUID, and the address is not logged. The correlation identifiers already used on this path stay: channel UUID, message id / UUID, and the channel-log UUID written by `NewChannelLogFromRR` for the proxy call. Sentry reports for a lookup or send failure carry the channel UUID, the contact UUID when the backend already has it, and the message UUID or channel-log UUID. They do not carry mailbox addresses or the body. The existing blocked-mailbox log line that includes `from` is left as it is; this change does not add another one.

### Failure Handling

- Receive DB lookups (`LookupContactURNByExternalID` and the new latest-message read) use the request context, which `channelHandleWrapper` cancels at 30s. A timeout or other error fails the receive before `WriteMsg`. email-proxy can retry the webhook. Nothing was written, so the retry is safe.
- Invalid `cc` entries are dropped and counted. They do not fail the receive. A `cc` value that is not a JSON array fails decoding at `DecodeAndValidateJSON` and returns the existing 400.
- Send to email-proxy keeps `utils.MakeHTTPRequest` and `GetHTTPClient`'s 60s timeout. This change does not wrap the call in `MakeHTTPRequestWithRetry`. The POST is not idempotent: a retry inside `SendMsg` can deliver the same `Cc` twice when the proxy already accepted. Recovery stays the existing worker path. Status `E` increments `error_count` and sets `next_attempt` to `now + 5 * (error_count + 1) minutes` using the pre-update count (`backends/rapidpro/status.go`). When `error_count` is already `>= 2`, or status is already `F`, the row becomes `F`. That is three attempts, then failed. `cc` metadata is written only after HTTP 2xx, so a retry still reads the previous latest list. A latest-message lookup error on send does not POST; it returns status `E` and uses that same bound.
- The read of the latest list and the insert of the new inbound row are not locked together. Two copy replies processed at the same time can each persist a snapshot that misses the other's addition. The next message that still carries the address in `cc` or as `from` appends it again. Email threads are not concurrent at the peak above, and this change does not add a contact-row lock.

### Security and Authorization

The trust boundary is the email-proxy JSON on `POST /c/em/{uuid}/receive` and the outbound response body (`message_id` only, already parsed). `server.channelHandleWrapper` calls `GetChannel` before the handler; an unknown channel UUID is rejected and the handler does not run. There is no new signature check; the email channel has none today. `from` and `body` stay required. Each `cc` entry is checked with the mailbox rule above before it is stored or sent. Addresses are not written into contact fields and are not added as URNs, so a copy author cannot become a contact by being listed. The block check on the real sender still runs first. `EMAIL_PROXY_AUTH_TOKEN` stays an environment variable, sent as `Authorization: Bearer` on the outbound call, and is not added to logs. No new secret.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| A copy reply that cites a stored Message-ID is written on the central URN, with `{from}:\n\n{body}`, the opener's name, and no new URN for the author | `RunChannelTestCases` in `handlers/email/email_test.go`: seed a `+wt-` parent via `AddMsgByExternalID`, post a receive whose `from` is a different mailbox and whose `in_reply_to` is that Message-ID, assert URN, `Name`, `Text` and a single queued msg |
| The central author's body is stored unchanged | Same suite: `from` equal to the opener (case-insensitive) asserts `Text` equals `body` and the URN is the central one |
| The list starts from the first `cc`, drops the channel mailbox and the opener, only grows, and lives in `metadata.email.cc` | Same suite: first receive asserts `Metadata.email.cc` without the channel mailbox or the opener; a later receive whose `cc` is a subset asserts the earlier addresses are still present and a new author or new `cc` address was appended. The list is on the message metadata, not a contact field |
| Send uses `to` without `+wt-` and `cc` from the latest message, and stores that list | `RunChannelSendTestCases`: parent being replied to has a short `cc`, a newer message on the same URN has a longer `cc`; `RequestBody` `to` is the real opener and `cc` is the longer list; `StatusMetadata.email.cc` equals that list; subject and `references` still come from the parent |
| Mail with no stored Message-ID opens a new conversation whose contact is the sender | Same receive suite: a payload from a mailbox that appears only as `cc` on another seeded thread, with no `in_reply_to`/`references` that resolve, asserts `urnWithTag` of that sender |
| Block by the real sender happens before the thread attach | Same receive suite: `SetEmailMailboxBlocked` on the copy author, `in_reply_to` citing the thread, assert the existing ignored response and no queued msg |
| No-copy threads and current threading stay | Existing cases in `receiveTestCases` and `defaultSendTestCases` stay, including legacy plain-mailto reuse, "Reply that cites another mailbox keeps a new thread URN", `Re:` subject, References chain, and send JSON with no `cc` key when the latest message has no list |

**Flow coverage**: receive webhook → blocked check → URN choice → text and metadata written, through `RunChannelTestCases`. Send → proxy request body and wired status metadata, through `RunChannelSendTestCases`. Both go through `handlers/email`, not a direct call of the accumulator only. A small test of the accumulator may cover normalization (channel mailbox, opener, `+wt-` strip, case, invalid entry) but is not the only coverage.

**Failure paths covered**: missing `from` / missing `body` (existing 400); `cc` JSON of the wrong type (400, nothing written); blocked sender, including a blocked copy author (nothing written); latest-message lookup error on receive (error response, nothing written); latest-message lookup error on send (status `E`, proxy not called); proxy HTTP 500 (existing status `E`, and `email.cc` not written on that attempt).

**Stubbed at the boundary**: email-proxy is the `httptest` server already used by `RunChannelSendTestCases` (`setSendURL`). Postgres is the `MockBackend`. No real SMTP and no network.

## Rollout and Rollback

Ship the email-proxy contract first: receive may include `cc`, and `POST /send` accepts `cc`, writes the `Cc` header, and leaves `To` as only the central contact. Absent or empty `cc` is the proxy's current behavior. Courier deploys second, through a pull request to `main` on `weni-ai/courier`, and the release adds a `WENI-CHANGELOG.md` entry because agents see the author line and people in copy start receiving replies.

No feature flag. Threads with an empty list already omit `cc`, so old conversations keep today's send without a second code path.

Rollback is the previous Courier binary. It ignores `metadata.email.cc`, stops prefixing new bodies, and again builds a URN from `from`. No schema change has to be undone. Messages already prefixed stay prefixed. The proxy can keep accepting `cc`; Courier simply stops sending the field. Do not roll the proxy back to a decoder that rejects unknown JSON while this Courier version is still sending `cc`, or sends with a non-empty list fail until Courier is rolled back too.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| I. Reviewed changes on a protected main branch | The implementation enters `main` only through a pull request, as in Rollout | none |
| II. Secrets and supply chain | No new secret. `EMAIL_PROXY_AUTH_TOKEN` stays an environment variable and is not written into the spec, fixtures or logs | none |
| III. Structured, privacy-safe observability | New logs are structured and carry channel UUID and message id only. Addresses, URN paths and body are not logged. Dropped entries are a count | none |
| IV. Versioned contracts | The three public shapes gain an optional `cc` and are backward compatible. Empty `cc` is omitted rather than sent as a new required field | none |
| V. Specification traceability | This spec opens with `## Origin Without Product Spec`. Product Spec is `none`. The problem and acceptance criteria are the what | none |
| VI. No silent divergence | No Product Spec, so nothing is inherited and there is no Divergences section | none |
| VII. Conventional, atomic commits | Implementation commits use Conventional Commits (`feat`), imperative, at most 50 characters, one logical change each | none |
| VIII. Release changelog | The user-facing author line and the new recipients are a `WENI-CHANGELOG.md` entry on the release. `CHANGELOG.md` is not edited | none |
| IX. Never trust the client | `from`, `body` and each `cc` entry are validated in `receiveEvent` before `WriteMsg`. The channel UUID is resolved in `channelHandleWrapper` before the handler | none |
| X. Fail gracefully and predictably | Lookups use the 30s request context. The proxy call uses `GetHTTPClient` (60s), not `http.DefaultClient`. Lookup failure and proxy failure surface as the existing error response or status `E`, not a panic | none |
| XI. Bounded retry over REST | Proxy failure stays on the worker retry: status `E`, then `F` once `error_count >= 2`, backoff `5 * (error_count + 1)` minutes. The POST is not wrapped in `MakeHTTPRequestWithRetry` because it is not idempotent. `cc` is persisted only after HTTP 2xx, so a retry does not need a new dedup key to rebuild the list | none |
| XII. Stateless, peak-sized services | The list is on `msgs_msg` in Postgres. No process memory and no new local-disk state. Peak is the 242.9 req/s service ceiling of 2026-10-28, with the window above | none |
| XIII. Diagnosable errors | Sentry context is channel UUID, contact UUID when already available, and message or channel-log UUID. No mailbox and no body | none |
| XIV. Tests exercise flows | Receive and send are covered through `RunChannelTestCases` and `RunChannelSendTestCases`, including the failure paths listed in Test Strategy | none |
| XV. Explicit over clever | Accumulation, the body prefix and the mailbox check each live in one function. The `+wt-` pattern, `maxExternalIDLength` and the existing retry SQL stay the named rules. No new numeric threshold | none |
| XVI. Contained changes | The diff is the email handler, the one new backend read, the mock, and the email tests. Other channel handlers and the legacy mailto rules are not rewritten | none |

## Implementation Decisions

- **Adopt the stored `+wt-` URN.** When the cited Message-ID belongs to a contact whose URN path contains `+wt-`, that URN is the central one. Rebuilding from the new `from` is what assigns a new contact. The legacy plain-mailto rules (same mailbox reuses the URN; a different mailbox on the direct In-Reply-To does not walk older References) stay, so this does not pull unrelated mail onto a one-mailbox legacy contact.
- **Author line only in `text`.** The prefix is `TrimSpace(from)`, a colon, a blank line, then the original body. Comparison with the central address is `sameEmailAddress`, so a case difference does not prefix the opener's own reply.
- **Latest row, not the parent.** Threading headers still come from the message being replied to. Only `cc` comes from the latest visible message of the URN, which is why the new read does not require `external_id`: a Message-ID longer than 255 is skipped for `external_id` today and must still be able to carry the list.
- **Omit empty `cc`.** Both the proxy body and `metadata.email` omit the key when the list is empty, so no-copy threads keep the current JSON.
- **Drop bad entries, accept the message.** A garbage `cc` entry must not discard the customer's mail. A `cc` that is not an array still fails decoding.
- **No lock around the snapshot.** The lost update under two concurrent copy replies is accepted. The following message that still names the address puts it back. No contact-row lock in this change.
- **No backfill and no flag.** Contacts already split stay split. Old threads send with `cc` omitted until a new message writes a list.
