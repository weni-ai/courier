# Research: Email Thread Cc

Resolved before design. No item is left as NEEDS CLARIFICATION. The engineering spec already chose the behavior; this file records how that behavior sits on the code that exists.

## 1. Where the copy list is read

- **Decision**: Add `LookupLastMsgByContactURN(ctx, channel, urn) (Msg, error)` on `courier.Backend`. SQL is `selectLastMsgWithExternalIDSQL` without the `external_id IS NOT NULL` and `external_id != ''` predicates. Same `visibility = 'V'`, `ORDER BY m.id DESC LIMIT 1`, same `scanDBMsgWithMetadata`. No row maps to `courier.ErrMsgNotFound`. Any other error is returned as-is.
- **Rationale**: `LookupLastMsgWithExternalID` (`backends/rapidpro/msg.go`) skips a message whose Message-ID was longer than `maxExternalIDLength` (255) and therefore has metadata but no `external_id`. The spec requires the latest visible row, because that row holds the copy list. Threading must keep using the external-id lookup; mixing the two would change parent resolution.
- **Alternatives considered**: Reusing `LookupLastMsgWithExternalID` drops the snapshot that has no Message-ID. Selecting every row of the thread and unioning `email.cc` contradicts the spec, which names the single latest message. A new table or contact field was rejected in the spec.

## 2. Not-found versus a failed read

- **Decision**: The handler treats `courier.ErrMsgNotFound` as an empty previous list (omit `cc`). Any other error fails the receive with `WriteAndLogRequestError` (HTTP 400 from `courier.WriteError`) and writes nothing. On send, any other error returns the status object still at `E` and does not call `MakeHTTPRequest`.
- **Rationale**: A missing row is the first message of a thread and the normal case for today's send tests. A database error treated as an empty list would send with no `Cc` and drop recipients. `WriteError` is already a 400; this path does not invent a 500.
- **Alternatives considered**: Swallowing every error the way `resolveParentExternalID` swallows a parent-lookup error. That is safe for threading (the send still goes out) and unsafe for the copy list.

## 3. Mock storage and the tests that must change

- **Decision**: `MockBackend` gets its own map and `AddLastMsgByContactURN`. The seed accepts a message with an empty `external_id`. `AddLastMsgForContact` and `LookupLastMsgWithExternalID` stay as they are, including the empty-`external_id` rejection. A field `LastMsgByContactURNErr error` on the mock, when set, is returned before the map lookup so tests can force the failure path. `PrepBackend` sets it; `handlers/test.go` is not modified.
- **Rationale**: `AddLastMsgForContact` (`test.go`) returns immediately when `ExternalID()` is empty, and `LookupLastMsgWithExternalID` returns `ErrMsgNotFound` in that case. Overloading that slot would make the "latest row has no external_id" case unseedable and would couple the copy-list fixture to the threading fixture. `seedParents` only calls `AddLastMsgForContact` for the ticket fallback. Existing send cases therefore see not-found from the new lookup, omit `cc`, and keep their exact `RequestBody`.
- **Alternatives considered**: One shared map. A message seeded for threading would be overwritten by the copy-list seed, or the reverse. A new field on `ChannelSendTestCase` to assert "proxy not called" would edit the shared harness for one case. The send failure case is a direct `SendMsg` test in `handlers/email/email_test.go`: the proxy handler fails the test if it is invoked, and the returned status is `E`.

Two existing receive cases seed a parent URN `mailto:fulano123+wt-aaaaaaaa@gmail.com` and expect `urnWithTag`, which is the tag rebuilt from `from` plus the anchor. Today `contactURNForInbound` does not adopt a URN that already has `+wt-`; it breaks on the direct In-Reply-To and rebuilds. After this change those cases expect the stored URN `mailto:fulano123+wt-aaaaaaaa@gmail.com`:

- "Reply that cites a threaded contact keeps the thread URN"
- "Direct reply to a threaded message ignores an older legacy reference"

"Reply to Meu Produto thread resolves back to that same contact" has no seeded parent, so it still expects `urnWithTag`. Legacy plain-mailto cases are unchanged.

## 4. JSON shape when the list is empty

- **Decision**: `Cc []string \`json:"cc,omitempty"\`` on `moPayload` and `emailThreadMetadata`. `buildThreadMetadata` and `buildOutboundThreadMetadata` include `cc` only when non-empty, and `buildThreadMetadata` returns the object when `cc` is the only populated field.
- **Rationale**: `TestSending` compares `RequestBody` as an exact string. A `"cc":[]` or `"cc":null` key fails those cases. `omitempty` on a nil slice omits the key. The proxy treats absent and empty as today's send.
- **Alternatives considered**: Always sending `cc`. That is a breaking change to the bytes existing tests and an old proxy would see.

## 5. Parsing a bad `email` object

- **Decision**: `parseEmailThreadMetadata` stays lossy. Invalid JSON or a missing `email` key yields an empty list, not a failed receive or a failed send.
- **Rationale**: That function already returns nil for bad metadata, and threading continues. Failing closed on one corrupt row would block every later message on the URN. The hard failure is reserved for the query itself (decision 2).
- **Alternatives considered**: Treating bad JSON as a lookup error. It protects against a silent empty `Cc` and stops delivery of mail we can still route.

## 6. Author line and contact name

- **Decision**: `WithContactName` receives `from` when `sameEmailAddress(from, realAddressFromURNPath(urn.Path()))`, otherwise `realAddressFromURNPath` of the URN being written. Body prefix is `strings.TrimSpace(from) + ":\n\n" + body` only when `sameEmailAddress` says `from` is not that real address. The original body is not trimmed.
- **Rationale**: `contactForURN` writes a name only when the stored name is empty. Passing the copy author's address would name a nameless contact after the wrong person, so a copy reply always passes the central address. When `from` is the central contact, passing `from` keeps its spelling: the existing legacy case expects `Fulano123@Gmail.com`, which a lowercased central address would break.
- **Alternatives considered**: Always passing the central real address. Simpler, but it changes the legacy case-only expectation with no benefit. Omitting `WithContactName` on copy replies. An empty name on a brand-new central contact would then stay empty if the first write was somehow a copy reply.
