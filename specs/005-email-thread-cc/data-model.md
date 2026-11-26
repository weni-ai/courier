# Data Model: Email Thread Cc

No new table, column, or contact field. The previous Courier binary still reads these rows.

## Message (`msgs_msg`)

The copy list and the author line live on the message that was just written.

| Field | Change | Rule |
|-------|--------|------|
| `text` | value only, new rows | Central author: `body` as received. Anyone else: `TrimSpace(from) + ":\n\n" + body`. The original body is not trimmed. Rows already stored are not rewritten. |
| `metadata.email.cc` | additive key | JSON array of bare mailboxes, omitted when empty. Same object as `subject`, `in_reply_to`, `references`. |
| `metadata.email.subject`, `in_reply_to`, `references` | unchanged | Still taken from the parent on send (`buildReplyContext`), not from the latest row. |
| `external_id` | unchanged | Still the Message-ID when its length is ≤ 255. A longer id is skipped. That row can still hold `cc`. |
| `contact_urn_id` | which row is chosen | A copy reply points at the central URN already stored on the cited message. No new `contacts_contacturn` row for the author or for `cc` entries. |
| `visibility` | unchanged | Only `V` rows are read as the latest snapshot. |
| `direction` | unchanged | Inbound and outbound both count. The outbound row written after HTTP 2xx becomes the latest snapshot. |

`metadata` stays schemaless JSON (`jsonb` in production, text in some tests). `scanDBMsgWithMetadata` already scans both. `emailThreadMetadata` ignores unknown keys, so a previous binary ignores `cc`.

Other metadata keys on the same row (`ticketer_id`, `chats_msg_uuid`) stay. `mergeEmailMetadata` replaces the `email` object and keeps the rest. The outbound `email` object must include `cc` when the list is non-empty, or the merge would drop it.

## Contact

Unchanged schema.

| Fact | Rule |
|------|------|
| URN | The central URN is the one created for the opener: `mailto:{local}+wt-{8 hex}@{domain}`, hash of the thread anchor. Legacy plain `mailto:` with no tag is not a central URN. |
| Name | The name passed is `from` when it equals the central real address (case-insensitive), otherwise the central real address (tag stripped). A copy reply never passes the author's address. `contactForURN` still updates the name only when it is empty. |
| Extra URNs | Not created for `cc` authors. `AddURNtoContact` is not called. |
| Fields | `contacts_contact.fields` is not written. |

Contacts split before this deploy are not merged.

## Copy list

The list on a message is a snapshot. The latest visible row of the channel + URN identity whose metadata contains a `cc` key (`ORDER BY id DESC LIMIT 1`) is the snapshot the next send and the next receive use. Rows without the key, such as the queued reply being sent, are skipped. The key test is `m.metadata::text LIKE '%"cc"%'`, which works for a text or jsonb column. The scan is bounded by the rows of one URN.

**Start**: first message of the URN, previous snapshot empty. Seed with that message's `cc`.

**Growth**: previous snapshot, then this message's `cc` in payload order, then `from` when `from` is not the central contact. Case-insensitive dedup, first spelling kept. Nobody already present is removed.

**Dropped before append**:

- blank after trim
- not a single mailbox: exactly one `@`, non-empty local part, non-empty domain, no whitespace
- equals the channel mailbox (`username` config, and `to` when set), compared with `sameEmailAddress` after stripping `+wt-`
- equals the central contact's real address
- already in the snapshot

A `+wt-` tag on an entry is stripped before those comparisons, and the stored value is the real address.

**Empty**: the key is omitted. Absent, JSON `null` and `[]` on input all mean this message contributes no addresses.

**Bad JSON**: a metadata value that does not parse as `email` is an empty snapshot, not an error. A failed query is an error and does not write a shorter list. See research decision 5 and decision 2.

**Concurrency**: the read and the insert are not locked. Two overlapping copy replies can each miss the other's addition. A later message that still carries the address in `cc` or as `from` appends it again.

## What is not stored

- The author line is not a metadata field.
- The list is not rebuilt by scanning the thread.
- Blocking stays a read of `contacts_contact.status = 'B'` for the real mailbox of `from`, before the URN is chosen. No new block state.
