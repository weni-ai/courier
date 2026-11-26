# Contract: Email Cc

Courier's email channel JSON. email-proxy owns MIME and SMTP. This repository does not implement the proxy.

Normative behavior for identity, the author line and list growth is `specs/005-email-thread-cc/spec.md`. Field shapes are here so the plan does not drift from the bytes on the wire.

SemVer: minor. Every new field is optional and omitted when empty. No field is removed. No deprecation window.

## Inbound `POST /c/em/{uuid}/receive`

Caller: email-proxy. Auth: the channel UUID in the path must resolve (`GetChannel`). Unknown UUID never reaches the handler.

`from` and `body` stay required. A missing one is HTTP 400, as today.

New field:

| Field | JSON | Meaning |
|-------|------|---------|
| `cc` | array of strings, optional | Bare mailboxes from the message's Cc header. Omitted, `null` and `[]` contribute no addresses. |

A `cc` that is not an array fails `DecodeAndValidateJSON` and is HTTP 400. Nothing is written.

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

Courier does not parse display names. An entry with whitespace or angle brackets is dropped, counted in `cc_dropped`, and the message is still accepted.

The address Courier stores and later sends is the entry after trim and after stripping `+wt-`. The channel mailbox and the central contact are not stored. Rules: [data-model.md](../data-model.md).

## Outbound `POST {EMAIL_PROXY_URL}/send`

Caller: Courier `SendMsg`. Auth: `Authorization: Bearer ${EMAIL_PROXY_AUTH_TOKEN}`.

`to` is the central URN with the `+wt-` tag removed (`realAddressFromURNPath`). It is not a `cc` entry.

New field:

| Field | JSON | Meaning |
|-------|------|---------|
| `cc` | array of strings, optional | The snapshot on the latest visible message of the URN. Omitted when that snapshot is missing or empty. |

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

`in_reply_to`, `references` and `subject` still come from the parent being replied to. They are independent of which row supplied `cc`.

HTML escaping stays off, so Message-IDs keep literal angle brackets.

When the list is empty the body matches today's payload: no `cc` key. Existing `RequestBody` assertions in `handlers/email/email_test.go` depend on that.

### Proxy obligations (not this repository)

- Receive: include `cc` as a list of bare addresses when the mail has a Cc header.
- Send: set the SMTP `Cc` header to the `cc` array. Set SMTP `To` only to `to`.
- Absent or empty `cc` on send: current behavior, no `Cc` header.
- Deploy this before Courier starts sending a non-empty `cc`, if the proxy's decoder rejects unknown fields.

## Stored metadata

Written on the inbound message, and on the outbound message only after HTTP 2xx.

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

`cc` is omitted when empty. Sibling keys on the metadata object are preserved by `mergeEmailMetadata`.

## Errors

| Case | Result |
|------|--------|
| Missing `from` or `body` | HTTP 400, nothing written |
| `cc` not an array | HTTP 400, nothing written |
| Blocked real mailbox of `from` | Same ignored response as today, before URN resolution, nothing written |
| Latest-message query fails on receive | HTTP 400, nothing written |
| Latest-message query finds no row | Receive continues with an empty snapshot. Send omits `cc` |
| Latest-message query fails on send | Status `E`, proxy not called, metadata not updated |
| Proxy non-2xx or transport error | Status `E`, `cc` not written on that attempt. Worker retries with the existing `error_count` bound |
| Proxy HTTP 2xx | Status wired. Returned `message_id` stored when it fits 255. Metadata includes the same `cc` list that was sent |
