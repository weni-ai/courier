# Engineering Specification: WhatsApp Coupon Template Button

**Repository**: courier — backend

**Feature Branch**: `[002-whatsapp-coupon-button]`

**Created**: 2026-10-30

**Status**: Draft

## Origin Without Product Spec

- Product Spec: none
- Problem: Courier's WhatsApp Cloud template send builds button components with `wacParam.Type` and `wacParam.Text` only. Meta coupon templates require a button with `sub_type` `copy_code` and a parameter `{ "type": "coupon_code", "coupon_code": "<code>" }`. The `coupon_code` field is never serialized, so Meta returns `(#100) Invalid parameter` (`Parameter 'coupon_code' is mandatory for component parameter type 'coupon_code'`).
- Acceptance criteria:
  - A WhatsApp Cloud template send whose `metadata.buttons` include `sub_type` `copy_code` and a parameter of type `coupon_code` produces a Graph API payload whose button component has `sub_type` `copy_code` and `parameters[0].coupon_code` equal to the inbound code, so Meta does not return `#100` for a missing `coupon_code`.
  - The same send still attaches header media and body variables when those are present on the message (coupon template with image header + `{{1}}`/`{{2}}`).
  - Existing template sends without a coupon button (`url`, `quick_reply`, `payment_request`, no buttons) keep the current Graph payload shape.
  - A `copy_code` button with an empty or missing code fails the send in Courier without calling Meta, and the failure is recorded on the message status / channel log.

## Scope in This Repository

**This repository implements**: mapping of outbound WhatsApp Cloud (WAC) template button components for `copy_code` / `coupon_code` in `handlers/facebookapp`, including the Graph JSON field `coupon_code` on `wacParam`; send-path tests that pin the payload; and the documented inbound contract on `msg` metadata `buttons` that RapidPro and Mailroom already persist.

**Out of scope here**:
- RapidPro (`POST /api/v2/whatsapp_broadcasts.json`) — `WhatsappBroadcastWriteSerializer.validate_msg` treats `msg` as a `DictField` and does not filter `msg.buttons`. No serializer change is required if callers use the contract below.
- Mailroom — `WppBroadcastMessage.Buttons` is `[]flows.ButtonComponent` with `SubType` and `Parameters[].Type`/`Text`; evaluation in `core/models/msgs.go` copies those fields into `msgs_msg.metadata.buttons`. No Mailroom change is required for the same contract.
- Creating or syncing Meta coupon templates (template catalog / `COPY_CODE` on `temba.templates.models.TemplateButton` already exists in RapidPro).
- Live WhatsApp delivery to a handset (CI stubs Meta). Production verification after deploy is rollout, not this repo's test suite.
- Slack reply to Gabriel Bertolucci (operational, after production send).

**Depends on**: RapidPro/Mailroom continuing to persist `metadata.buttons` as `{ "sub_type", "parameters": [{ "type", "text" }] }` on `msgs_msg`. Callers of the broadcast API must place the offer code in `parameters[].text` with `type` `coupon_code` and `sub_type` `copy_code`.

## Current State

- `handlers/facebookapp/facebookapp.go` `buildTemplateComponents` (around 3945–3997) iterates `msg.Buttons()`. For `sub_type == "payment_request"` it builds an `action` parameter; every other subtype copies `parameter.Type` and `parameter.Text` onto `wacParam`. There is no `copy_code` / `coupon_code` branch.
- `wacParam` (around 2442–2453) JSON fields: `type`, `parameter_name`, `text`, `payload`, `phone_number`, `url`, media, `action`. No `coupon_code`.
- Inbound button model: `courier.ButtonComponent` / `courier.ButtonParam` in `msg.go` (`sub_type`, `parameters[].type`, `parameters[].text` only).
- `backends/rapidpro/msg.go` `DBMsg.Buttons()` unmarshals `metadata.buttons` with type assertions on `sub_type`, `type`, and `text`. A payload that uses `coupon_code` instead of `text` panics or drops the button.
- Analogous covered sends in `handlers/facebookapp/facebookapp_test.go`: `Template Send - Multiple Buttons`, `Template Send - Payment Request *`, carousel button cases. No coupon/copy_code case.
- Outbound HTTP uses `utils.MakeHTTPRequest` on the WAC `/messages` URL (same path as other template sends). Provider 4xx (including Meta `#100`) is not retried.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `handlers/facebookapp/facebookapp.go` (`wacParam`, `buildTemplateComponents`) | modified | Serialize Meta's mandatory `coupon_code` parameter for `copy_code` buttons |
| `backends/rapidpro/msg.go` (`DBMsg.Buttons`) | modified | Decode buttons without panicking; keep reading `type`/`text` (code lives in `text`) |
| `handlers/facebookapp/facebookapp_test.go` | modified | Flow send tests for coupon + header + body; empty-code failure; existing button regression |

### Flow

1. Mailroom writes an outgoing `msgs_msg` with `metadata.templating` and `metadata.buttons` (existing path).
2. Courier loads the message; `DBMsg.Buttons()` returns `[]courier.ButtonComponent`.
3. `sendWACMsg` / `buildTemplateComponents` builds body params from `templating.variables`, header media from `msg.Attachments()`, then one Graph `button` component per inbound button.
4. For `sub_type` `copy_code` (alias: inbound `sub_type` `coupon_code` normalized to `copy_code`): each parameter becomes `{ "type": "coupon_code", "coupon_code": <ButtonParam.Text> }`. Index is the button position in the slice (same as url/payment buttons).
5. POST Graph `/{phone-number-id}/messages` via `utils.MakeHTTPRequest`. Success → wired status + external id; Meta 4xx → failed/errored with channel log (no retry).

### Rejected Alternatives

- **Pass through a raw `coupon_code` JSON key from RapidPro into `wacParam`**: `ButtonParam` and `DBMsg.Buttons()` only have `text`. Adding a third inbound field would force Mailroom/goflow and RapidPro changes for no gain; Meta still needs `type: coupon_code` plus `coupon_code`.
- **Put the code in `templating.variables` instead of `buttons`**: body `{{n}}` and the copy-code button are distinct Meta components; the incident already sent both and Meta still required the button parameter.
- **Call Meta with `type: coupon_code` as the button `sub_type`**: Graph expects button `sub_type` `copy_code`; `coupon_code` is the parameter type/field.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| Outgoing WAC template JSON (`template.components[]` button) | modified: new `parameters[].coupon_code` when subtype is copy_code | minor (Courier service) | yes — field omitted for other button types (`omitempty`) |
| Inbound `msgs_msg.metadata.buttons` | unchanged shape; new documented values | minor (documentation of existing queue/DB payload) | yes — additive values `copy_code` / `coupon_code` |
| `POST /api/v2/whatsapp_broadcasts.json` `msg.buttons` | documented, not implemented here | n/a in this repo | yes |

**Consumers affected**: RapidPro broadcast clients (must send the payload below); Mailroom (passthrough). Notify via `WENI-CHANGELOG.md` on the Courier release that adds the mapping.

### Inbound contract (RapidPro → Mailroom → Courier)

```json
{
  "urns": ["whatsapp:5511999999999"],
  "msg": {
    "template": {
      "uuid": "<template-uuid>",
      "locale": "pt-BR",
      "variables": ["Gabriel", "1234"]
    },
    "attachments": ["image/png:https://example.com/header.png"],
    "buttons": [
      {
        "sub_type": "copy_code",
        "parameters": [
          {
            "type": "coupon_code",
            "text": "1234"
          }
        ]
      }
    ]
  }
}
```

- `sub_type`: `copy_code` (Meta button subtype). Courier also accepts `coupon_code` and emits Graph `sub_type` `copy_code`.
- `parameters[0].type`: `coupon_code`.
- `parameters[0].text`: the offer code (this is **not** sent to Meta as `text`; it is copied to Graph `coupon_code`).
- Do **not** send `"coupon_code": "1234"` as the only value field: `DBMsg.Buttons()` reads `text`.
- Header image remains `msg.attachments`; body variables remain `msg.template.variables`. They are independent of the button.

### Outbound contract (Courier → Meta Graph)

```json
{
  "type": "button",
  "sub_type": "copy_code",
  "index": 0,
  "parameters": [
    {
      "type": "coupon_code",
      "coupon_code": "1234"
    }
  ]
}
```

Reference: [Meta coupon templates](https://developers.facebook.com/documentation/business-messaging/whatsapp/templates/marketing-templates/coupon-templates).

## Data Model and Migrations

No schema change. Buttons remain JSON on `msgs_msg.metadata`. Previously deployed Courier ignores `copy_code` (emits `type`+`text` only); rolling forward only adds Graph fields. Rollback to the previous binary restores the old payload; messages already sent are unaffected.

## Non-Functional Requirements

### Peak Load

This feature does not add a send path or extra Graph round-trip. Peak is the existing WhatsApp Cloud template-broadcast peak for this handler: one Graph `POST /messages` per outgoing msg, same concurrency and campaign windows as current WAC template sends (including bulk `template_batch` / `wpp_broadcast_batch`). No additional QPS budget.

### Observability

- Structured logs and channel logs already attached to the WAC send (`channel UUID`, message UUID / ID, channel-log UUID). Do not log the coupon code, URN path, or message body (Principle III).
- On missing/empty coupon code, add a channel log from error with a static message (e.g. `missing coupon_code for copy_code button`) — no interpolated code.
- Sentry: existing send-error path; identifiers remain channel UUID + message UUID. Do not attach the code.

### Failure Handling

- Graph send timeout: existing `utils.MakeHTTPRequest` / shared HTTP client timeout. Do not switch this path to `http.DefaultClient`.
- Meta `#100` / other 4xx: not retriable (request defect). Status failed/errored; Mailroom/ops retry only after a code fix.
- Transient 429/502/503/504: current WAC send retry behaviour is unchanged (this spec does not introduce `MakeHTTPRequestWithRetry` on WAC; constitution TODO on 5xx stays out of scope).
- Empty `text` on a `copy_code` button: fail before HTTP; idempotent (no Graph side effect).
- Duplicate sends of the same msg id remain the existing Courier dequeue semantics.

### Security and Authorization

- Trust boundary: `metadata.buttons` from Postgres (written by Mailroom after RapidPro API auth). Treat as untrusted structure: validate `sub_type`, require non-empty code for `copy_code`, do not panic on extra/missing keys.
- Authorization: unchanged (org-scoped RapidPro token / internal Mailroom write). Courier uses the channel WAC access token from channel config (`COURIER_*` / DB config), never from the request body.
- Coupon codes are not secrets in the Meta sense but are sensitive campaign data: keep them out of logs.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| Graph payload includes `sub_type` `copy_code` and `parameters[0].coupon_code` | `RunChannelSendTestCases` case: templating + `buttons` copy_code/coupon_code + stubbed Graph 200; `RequestBody` asserts the button JSON |
| Header image + body variables still present | Same case: `attachments` image + `templating.variables`; `RequestBody` includes header media component and body text params plus the copy_code button |
| Templates without coupon buttons unchanged | Existing cases `Template Send - Multiple Buttons` and `Template Send - Payment Request *` remain green with current `RequestBody` |
| Empty/missing code does not call Meta | Send test: `copy_code` with empty `text` → status errored/failed, no (or empty) Graph body / error path asserted |

**Flow coverage**: outgoing WAC template send from `courier.Msg` metadata through `buildTemplateComponents` to the stubbed Graph request and resulting msg status (`RunChannelSendTestCases`).

**Failure paths covered**: empty coupon code; Graph 4xx on template send remains covered by existing WAC error cases (do not add a live Meta test).

**Stubbed at the boundary**: Meta Graph HTTP via the send-test httptest server; no real WhatsApp.

## Rollout and Rollback

1. Deploy Courier with the mapping. RapidPro and Mailroom need no coordinated deploy if clients already send `buttons` with `type`/`text`.
2. No feature flag: only messages with `sub_type` `copy_code` (or alias `coupon_code`) change payload.
3. After deploy, send one coupon template (image header + body vars + copy_code) to a test URN; confirm Graph 200 and the handset copy-code button. Then reply on the Slack thread.
4. Rollback: revert the Courier release. In-flight msgs with copy_code buttons will fail at Meta again (`#100`); other templates unaffected.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| I. Reviewed Changes | Change enters `main` via PR on `weni-ai/courier` with CI | none |
| II. Secrets | No new secrets; WAC token stays in channel config | none |
| III. Structured observability | Channel logs + correlation ids; no code/URN/body in logs | none |
| IV. Versioned contracts | Additive Graph field and documented inbound values; minor | none |
| V. Specification Traceability | Opens with Origin Without Product Spec | none |
| VII. Conventional commits | `fix:` for the mapping | none |
| VIII. Release changelog | User-facing send fix listed in `WENI-CHANGELOG.md` on tag | none |
| IX. Never Trust the Client | Validate button shape and non-empty code server-side | none |
| X. Fail Gracefully | Empty code and Meta 4xx become msg status + channel log | none |
| XI. Bounded Retry | 4xx not retried; no new REST hop | none |
| XII. Stateless / peak | No new local state; peak declared as existing WAC template peak | none |
| XIII. Diagnosable Errors | Channel UUID + message UUID on send errors | none |
| XIV. Tests Exercise Flows | `RunChannelSendTestCases` success + empty-code failure | none |
| XV. Explicit Over Clever | Named subtype/param constants (`copy_code`, `coupon_code`) | none |
| XVI. Contained Changes | Only WAC template button mapping + tests; no carousel/payment rewrite | none |

## Implementation Decisions

- **Inbound code field is `parameters[].text`**: matches `courier.ButtonParam` and Mailroom `flows.ButtonParam`; Courier maps it to Graph `coupon_code`.
- **Graph `sub_type` is always `copy_code`**: inbound alias `coupon_code` is normalized so a mistaken API `sub_type` still hits Meta's documented subtype.
- **Do not add `CouponCode` to `courier.ButtonParam`**: avoids a cross-repo schema change.
- **`DBMsg.Buttons()` must not panic** on missing `text` or extra keys: decode defensively (json into structs or comma-ok maps) so a bad coupon payload becomes a failed send, not a crashed worker.
- **Meta length/charset of coupon codes** is enforced by Meta; Courier only requires non-empty `text` and does not re-implement Meta's catalog rules.

## Open Questions

None.
