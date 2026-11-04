# Engineering Specification: WWC custom_data on outbound /send

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `[002-wwc-custom-data]`

**Created**: 2026-11-04

**Status**: Draft

<!--
  This is an ENGINEERING spec. It answers HOW this repository implements a
  change. Cycle: SDD Lite (specify → implement from spec.md).
-->

## Origin Without Product Spec

- Product Spec: none
- Problem: integrators sending WhatsApp broadcasts to Weni Web Channel contacts cannot receive opaque structured `custom_data` on the socket `/send` payload; Courier’s WWC handler never copies `metadata.custom_data` onto `message.custom_data`, so the field is dropped at this hop even when Mailroom stored it.
- Acceptance criteria:
  - **AC-C1**: A `courier.Msg` whose metadata contains a JSON-object `custom_data` produces a `POST {base_url}/send` whose `message.custom_data` is that same object (RequestBody / JSONEq against `handlers/weniwebchat/weniwebchat_test.go` fixtures).
  - **AC-C2**: Metadata without `custom_data` yields the same `/send` body as today (`message.custom_data` omitted via `omitempty`).
  - **AC-C3**: `metadata.custom_data` that is JSON `null`, a string, or an array still results in a successful send with no `custom_data` field on `message`.
  - **AC-C4**: A msg with three attachments plus valid `custom_data` includes `custom_data` only on the third POST body; the first two omit it.
  - **AC-C5**: Text + `quick_replies` + valid `custom_data` is a single POST whose `message` contains text, `quick_replies`, and `custom_data`.
  - **AC-C6**: A product list that `buildProductBatches` splits into multiple batches includes `custom_data` only on the last batch POST.
  - **AC-C7**: Non-WWC handlers (at least WhatsApp Cloud via `handlers/facebookapp`) do not add `custom_data` to provider API bodies even when metadata contains it; no handler outside `handlers/weniwebchat` is changed for this feature.

Architecture (binding input, not a Product Spec): pinned at tag `006-wwc-custom-data/arch-v1`, commit `1ebcd6346d46d269f6421d7f3c10344ebdda394f` — https://github.com/weni-ai/vtex-cx-experience-specs/blob/1ebcd6346d46d269f6421d7f3c10344ebdda394f/specs/006-wwc-custom-data/architecture.md. Courier implements only the courier row of that Impact Map (AD-001, AD-002 defensive type drop, AD-003 ignore on non-WWC, AD-005 last payload only).

## Scope in This Repository

**This repository implements**: outbound WWC send in `handlers/weniwebchat` only. Read optional `custom_data` from `msg.Metadata()`; if the value is a JSON object, set `message.custom_data` on the same last outbound payload that already receives `quick_replies`, `list_message`, and `cta_message`. Drop the field (do not fail the send) when the key is present but not a JSON object. Product carousel: one POST, attach when the object is valid. Product list: attach only on the last batch from `buildProductBatches`, even though batches currently do not call `addInteractiveElements`. Resends that reload `msgs_msg.metadata` via `DBMsg.Metadata()` keep the same behaviour because the handler reads that JSON, not a one-shot queue-only field.

**Out of scope here**:
- Flows `WhatsappBroadcastWriteSerializer` and AD-002 size/depth/key validation
- Mailroom `WppBroadcastMessage` / `CreateWppBroadcastMessages`
- weni-webchat-socket persistence and WebSocket frames
- webchat-service `customData` rename
- `handlers/facebookapp`, `handlers/whatsapp`, and every other channel handler (they MUST keep ignoring unknown metadata keys)
- `backends/rapidpro/msg.go` unless a helper proves necessary (rejected; see Implementation Decisions)
- Inbound WWC receive, `SendAction` / typing indicator payloads
- Duplicating OpenAPI or JSON Schema files into this repo

**Depends on**:
- Mailroom writing optional `custom_data` into `msgs_msg.metadata` and the courier queue JSON as specified by `contracts/events/courier-msg-metadata.schema.json` (architecture 1.0.0 delta).
- weni-webchat-socket accepting optional `message.custom_data` on `POST {base_url}/send` (`contracts/openapi.weni-webchat-socket.yaml`). Older socket versions drop unknown JSON fields; Courier must still be deployed after socket/service per Delivery Sequence step 2.
- Flows as the only validator of AD-002 limits; Courier MUST NOT re-check size, depth, or forbidden keys.

## Current State

- `handlers/weniwebchat/weniwebchat.go` — `SendMsg` builds `moPayload` / `moMessage`, POSTs JSON to `{channel ConfigBaseURL}/send` via `utils.MakeHTTPRequestWithRetry` (3 attempts, 500ms base backoff, `Idempotency-Key`). Attachment loop: caption on first part; `addInteractiveElements()` only when `i == lenAttachments-1`. Plain text: one payload then `addInteractiveElements()`. `sendProductMessage`: carousel is a single `sendPayload`; product list loops `buildProductBatches` (max 30 products / 10 sections) and never attaches interactive extras. `moMessage` has no `custom_data` field. `SendAction` posts a typing map, not `moMessage`.
- `handlers/weniwebchat/weniwebchat_test.go` — `sendTestCases` use `ChannelSendTestCase.RequestBody` (exact string match of the **last** captured POST) and `Metadata` for list/CTA/products. Multi-attachment cases do not assert each body. Product cases use `Metadata` with `products` / `product_carousel`.
- `handlers/test.go` — `ChannelSendTestCase.Metadata` is applied with `msg.WithMetadata`; `RequestBody` reads `testRequest` after send (last request). `Responses` + `MockedRequest` can match multiple POSTs by exact `Body` or `BodyContains`.
- `backends/rapidpro/msg.go` — `Metadata()` returns `*Metadata_` as `json.RawMessage`. Typed keys (`QuickReplies`, products, `cta_message`, …) use `jsonparser.Get` on that blob. No `custom_data` accessor.
- Other handlers (`handlers/facebookapp`, `handlers/whatsapp`) decode **named** metadata keys into typed Graph/Cloud API structs; they do not marshal the whole metadata object onto the provider body. AD-003 storage of `custom_data` on WhatsApp msgs is therefore a no-op at send time today.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `handlers/weniwebchat/weniwebchat.go` `moMessage` | modified | Add `CustomData json.RawMessage \`json:"custom_data,omitempty"\`` so a JSON object is emitted under `message.custom_data` (AD-001 snake_case). |
| `handlers/weniwebchat/weniwebchat.go` helper (e.g. `customDataFromMetadata`) | new | `jsonparser.Get(msg.Metadata(), "custom_data")`; copy bytes only when `ValueType == jsonparser.Object`; otherwise return nil (drop). Do not parse into `map[string]any` (preserves key order and avoids re-encoding). |
| `addInteractiveElements` | modified | After quick replies / list / CTA, set `payload.Message.CustomData` from the helper. Covers text and last attachment. |
| `sendProductMessage` | modified | After building each batch/carousel payload, set `CustomData` only on the last POST (carousel: the only POST; list: last index of `allBatches`). Same helper. |
| `handlers/weniwebchat/weniwebchat_test.go` | modified | New `ChannelSendTestCase` rows for AC-C1–AC-C6; AC-C4/AC-C6 use `Responses` (or an equivalent last-vs-earlier body assertion) because `RequestBody` only sees the last POST. |
| `backends/rapidpro/msg.go` | none | Handler reads `msg.Metadata()` like other opaque keys. |
| Other `handlers/*` | none | Ignore `custom_data` (AD-003). |

### Flow

1. Mailroom (or a resend from Postgres) supplies `courier.Msg` with optional `metadata.custom_data`.
2. WWC `SendMsg` / `sendProductMessage` builds one or more `moPayload`s as today.
3. Immediately before marshal of the **last** payload of that logical message, copy object-typed `custom_data` onto `moMessage.CustomData`.
4. `json.Marshal` + `POST {base_url}/send` with existing retry/idempotency. Channel log from `MakeHTTPRequestWithRetry` already stores the request dump (treat as message content; do not add structured log fields with the object).
5. Non-object `custom_data`: helper returns nil; send proceeds; status remains `MsgSent` if HTTP succeeds.
6. Missing key: omitempty; body matches current fixtures.

### Rejected Alternatives

- **`DBMsg.CustomData()` in `msg.go`**: extra public surface for one handler; other channels must not grow accessors for a WWC-only field (Principle XVI).
- **Re-validate AD-002 in Courier**: architecture forbids it; a 16 KB object that passed Flows must still be forwarded.
- **Fail the send on bad type**: would retry and fail broadcasts for data Courier is told to drop (constitution IX + architecture error model).
- **Attach on every attachment/batch**: contradicts AD-005; would duplicate history rows on the socket.
- **`map[string]interface{}` on `moMessage`**: reorders/re-encodes keys; `json.RawMessage` is verbatim.

## Contracts

External schemas are **pinned**, not copied:

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| Input: outgoing msg metadata (`msgs_msg.metadata` / queue JSON) | consume optional `custom_data` | n/a in this repo (producer is Mailroom, contract 1.0.0) | yes — key optional |
| Output: `POST {base_url}/send` body `message.custom_data` | add optional field on last payload | Courier release **MINOR** (additive provider payload) | yes — omitempty; old socket drops unknown fields |

Pinned files (same architecture commit `1ebcd6346d46d269f6421d7f3c10344ebdda394f`):

- https://github.com/weni-ai/vtex-cx-experience-specs/blob/1ebcd6346d46d269f6421d7f3c10344ebdda394f/specs/006-wwc-custom-data/contracts/events/courier-msg-metadata.schema.json
- https://github.com/weni-ai/vtex-cx-experience-specs/blob/1ebcd6346d46d269f6421d7f3c10344ebdda394f/specs/006-wwc-custom-data/contracts/openapi.weni-webchat-socket.yaml
- Shared value shape (do not enforce in Courier): https://github.com/weni-ai/vtex-cx-experience-specs/blob/1ebcd6346d46d269f6421d7f3c10344ebdda394f/specs/006-wwc-custom-data/contracts/custom-data.schema.json

Inbound `/c/wwc/<uuid>/receive` is unchanged. `COURIER_*` config is unchanged.

**Consumers affected**: weni-webchat-socket (must already accept the field before this deploy). Integrators are not notified until architecture Delivery Sequence step 5.

## Data Model and Migrations

This feature persists nothing new in Courier. `msgs_msg.metadata` already stores JSON; Mailroom may add `custom_data` there. Courier only reads it at send time. No schema.sql change, no backfill, previous Courier binaries remain valid against the same tables (they ignore the extra key).

## Non-Functional Requirements

### Peak Load

No new send path and no extra HTTP calls. Peak outbound WWC traffic remains the current production peak of WWC `SendMsg` invocations (same messages/s as today during any sales window, including Black Friday). Each last payload may grow by at most 16 KiB of JSON (Flows cap). Horizontal scaling and timeouts stay those of existing `/send` retries. This spec does not introduce a new throughput class; if production WWC send peak is later quantified, record it in the constitution `TODO(PEAK_LOAD)` without blocking this slice.

### Observability

- Do **not** add logrus fields, Sentry extras, or metrics that contain `custom_data` (opaque integrator data; Principle III).
- Existing channel logs (`Message Sent` / `Message Send Error`) continue to attach the HTTP request dump (message-body class data, same as text).
- Correlation: channel UUID + msg UUID / ID on those logs. No new correlation ID.

### Failure Handling

- Outbound HTTP: unchanged `MakeHTTPRequestWithRetry` — 3 attempts, 500ms exponential backoff with jitter, `Idempotency-Key` (`msg.UUID` + nano in `SendMsg`; `ChannelUUID` + nano in `sendPayload`). Retries: connection errors, timeout, 429, 502–504. 4xx (including socket 400 if a non-object slipped through) are not retried; status `MsgFailed` as today.
- Timeouts: shared `utils.GetHTTPClient()`, not `http.DefaultClient`.
- Malformed `custom_data`: **not** a send failure; drop field and continue.
- Marshal failure of the payload: existing path — channel log + fail (unchanged).
- Exhausted retries: `MsgFailed` + channel log; Mailroom may retry the msg; metadata (including `custom_data`) is still on the row.

### Security and Authorization

- Trust boundary: `msg.Metadata()` is internal (Mailroom/DB), not a public webhook, but Principle IX still applies: type-check before copying onto the socket request.
- Authorization for `/send` is unchanged (cluster call to channel `base_url`; no new secrets). Channel tokens stay in `COURIER_*` / channel config.
- Do not treat `custom_data` as a control-plane field. Do not forward it to Meta APIs.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| AC-C1 | New `ChannelSendTestCase` in `weniwebchat_test.go`: `Metadata` with object `custom_data` (architecture example `order_id` / `coupon` / `tags`); `RequestBody` includes `"custom_data":{...}` under `message`. Run via existing `RunChannelSendTestCases`. |
| AC-C2 | Existing “Plain Send” fixture remains byte-identical (no `custom_data` key). Optional extra case: metadata with other keys only (`topic`) still omits `custom_data`. |
| AC-C3 | Three cases (or table-driven helper): `custom_data` `null`, `"oops"`, `[1]`. `Status` `sent`, `RequestBody` without `custom_data`. |
| AC-C4 | Three image attachments + object `custom_data`. Assert via `Responses` three POSTs `/send`: first two `BodyContains` media and **not** `custom_data`; third contains `custom_data`. (Do not rely on `RequestBody` alone.) |
| AC-C5 | Extend “Quick Replies Send”: same `quick_replies` fixture plus metadata `custom_data`; one POST with text, replies, and object. |
| AC-C6 | Metadata `products` with enough sections/items to force `buildProductBatches` length ≥ 2 (e.g. 11 sections or 31 product items) plus `custom_data`. `Responses`: only the last POST body contains `custom_data`. |
| AC-C7 | No edits under `handlers/facebookapp` or `handlers/whatsapp`. Verification: `rg custom_data handlers/facebookapp handlers/whatsapp` is empty; existing Cloud API `RequestBody` fixtures stay unchanged. Optional: one facebookapp send case with extra metadata key `custom_data` and assert the Graph body still matches today’s fixture (proves ignore). Prefer the optional case in `facebookapp_test.go` only if it can be added without touching production handler code; otherwise document the grep + fixture freeze in the PR. |

**Flow coverage**: outbound WWC send from `courier.Msg` to httptest `/send` (`RunChannelSendTestCases`), including product and attachment multi-POST paths.

**Failure paths covered**: AC-C3 (bad type still succeeds); existing 500 “Invalid Text Send” remains (provider failure independent of custom_data). Do not add a test that fails send solely because `custom_data` is invalid.

**Stubbed at the boundary**: socket `POST /send` is an `httptest.Server` (`SendPrep` sets `ConfigBaseURL`). No real socket, Flows, or Mailroom.

## Rollout and Rollback

Architecture Delivery Sequence:

1. webchat-service (npm MINOR) and weni-webchat-socket (any order) — **must be live** so `/send` persists/forwards the field.
2. **This Courier deploy** — starts emitting `message.custom_data` when metadata has an object.
3. Flows validation.
4. Mailroom propagation (opens end-to-end).
5. Integrator announcement.

Courier code may merge before or after Flows; until Mailroom writes the key, production WWC payloads stay unchanged (AC-C2). If Courier ships before the socket, old sockets ignore the extra JSON field (typed decode drop) — still deploy socket first to avoid silent loss once Mailroom fills metadata.

**Compatibility window**: additive field; old Courier ignores metadata key; new Courier + old socket = field dropped by socket without 4xx.

**Rollback**: revert the Courier release. Effect: `custom_data` stops appearing on `/send`; no schema to undo; in-flight msgs send without the field. Safe independently of Flows/Mailroom (architecture Rollback).

**Release notes**: `WENI-CHANGELOG.md` MINOR entry: WWC `/send` may include `message.custom_data` from msg metadata.

**PR**: reference this spec (`specs/002-wwc-custom-data/spec.md`).

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| I. Reviewed Changes | Change enters `main` via PR + CI | none |
| II. Secrets | No new secrets | none |
| III. Observability | No structured logs of `custom_data`; identifiers remain channel/msg UUIDs | none |
| IV. Versioned contracts | Additive `message.custom_data`; MINOR | none |
| V. Traceability | Origin Without Product Spec; architecture pinned by tag+SHA | none |
| VI. No Silent Divergence | N/A (no Product Spec) | none |
| VII–VIII. Commits / changelog | Conventional commits; WENI-CHANGELOG on release | none |
| IX. Never Trust the Client | Type-check metadata before copy; drop non-objects | none |
| X. Fail gracefully | Existing HTTP client/timeouts; bad custom_data does not panic or fail send | none |
| XI. Bounded retry | Existing 3-attempt `/send` retry; 4xx not retried | none |
| XII. Stateless / peak | No new local state; peak declared as unchanged WWC send peak + ≤16 KiB | none |
| XIII. Diagnosable errors | No new Sentry events; existing channel logs keep opaque IDs | none |
| XIV. Flow tests | AC-C1–C6 via `RunChannelSendTestCases`; failure type-drop via AC-C3 | none |
| XV. Explicit | Named helper; comment why last-batch product path must set the field (no `addInteractiveElements`) | none |
| XVI. Contained | Only `handlers/weniwebchat` (+ tests); no drive-by handler edits | none |

## Implementation Decisions

- **Parse in the WWC handler** from `msg.Metadata()` with `jsonparser`, not a `DBMsg` helper.
- **`json.RawMessage` + `omitempty`**: verbatim object bytes; nil/empty omits the key.
- **Object means JSON object only**: arrays, strings, numbers, booleans, `null` are dropped. Empty object `{}` is an object; if Mailroom ever stored it, Courier would emit `{}`. Flows treats `{}` as absence, so this should not appear in the happy path.
- **Product last batch**: set `CustomData` in the `sendProductMessage` loop (`i == len(allBatches)-1`), not by calling `addInteractiveElements` (that would also attach QR/list/CTA the product path does not send today).
- **AC-C4/C6 assertions**: use `ChannelSendTestCase.Responses` / `MockedRequest.BodyContains` (or capture all bodies in a small test helper) because `RequestBody` is last-request-only.
- **AC-C7**: no production code outside WWC; verification by grep + unchanged fixtures; optional facebookapp send case with unused `custom_data` metadata if it requires zero handler changes.
- **Do not implement in this specify command**: Go changes wait for `/speckit-implement` (lite cycle).
