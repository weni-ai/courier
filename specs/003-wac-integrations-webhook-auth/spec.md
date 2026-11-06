# Engineering Specification: Authenticated WhatsApp Cloud Webhook Forward to Integrations

**Repository**: courier (weni-ai/courier) — backend

**Feature Branch**: `003-wac-integrations-webhook-auth`

**Created**: 2026-11-06

**Status**: Draft

## Origin Without Product Spec

- Product Spec: none
- Problem: weni-integrations-engine now rejects `POST /api/v1/webhook/facebook/api/notification/` with 401 unless it carries `Authorization: Bearer <FACEBOOK_WEBHOOK_TOKEN>` (its SEC-07 fix). Courier is that route's only caller and sends no credential. Courier also forwards WhatsApp Cloud template and account events before checking Meta's `X-Hub-Signature`, and its signature error echoes the expected signature to the caller. Once Courier attaches the token, anyone could get a forged event accepted by integrations by sending it through Courier. A failed forward is also dropped without retry, and Meta still gets a 200.
- Acceptance criteria:
  - **AC-01**: A WhatsApp Cloud webhook with a valid `X-Hub-Signature` and an integration field (`integrationWebhookFields`) produces exactly one forward to `{WhatsappCloudWebhooksUrl}/api/v1/webhook/facebook/api/notification/` with `Authorization: Bearer <WhatsappCloudWebhooksToken>` and `Content-Type: application/json`. The body is byte-for-byte what Courier forwards today. When integrations answers 2xx, Meta gets the same 200 as today.
  - **AC-02**: When `WhatsappCloudWebhooksToken` is empty, Courier makes no request to integrations and logs one `ERROR` with `reason=token_not_configured`. A payload carrying only integration or Flows fields gets a non-200, so Meta redelivers it.
  - **AC-03**: A WhatsApp Cloud webhook with integration or Flows fields and a missing or invalid `X-Hub-Signature` is forwarded to neither integrations nor Flows. If the payload has no channel address, Courier answers 400. If it has one, channel resolution and `receiveEvent` continue exactly as today.
  - **AC-04**: On FBA, IG and WAC, an invalid-signature response body and the matching log entry contain neither the expected signature nor the request body. The response still contains `invalid request signature`.
  - **AC-05**: A forward that gets a connection error, a timeout, or HTTP 429, 502, 503 or 504 is retried, for at most `integrationsForwardAttempts` (3) attempts in total, with jittered exponential backoff starting at `integrationsForwardBaseBackoff` (500 ms). Every attempt carries the same `Authorization` and `Idempotency-Key`. A success on a later attempt gives Meta a 200.
  - **AC-06**: A forward answered with 401, or any other status that is not retriable, is attempted exactly once. It is logged at `ERROR` with `status_code`, and a 401 also carries `reason=unauthorized`. A payload carrying only integration or Flows fields gets a non-200, so Meta redelivers it.
  - **AC-07**: The whole forward, including retries and backoff, ends within `integrationsForwardTimeout` (10 s).
  - **AC-08**: The token never appears in a log entry, a channel log, a Sentry event or a response body.

## Scope in This Repository

**This repository implements**:
- A new setting, `WhatsappCloudWebhooksToken` (`COURIER_WHATSAPP_CLOUD_WEBHOOKS_TOKEN`).
- A dedicated integrations forward that sends the bearer token, fails closed when the token is unset, and retries with a bound.
- Meta signature verification before forwarding to integrations or Flows.
- Removal of the signature oracle from `validateSignature`.

**Out of scope here**:
- The integrations side of the contract. It is owned by weni-integrations-engine: spec `specs/002-fix-security-audit`, contract `contracts/facebook-webhook.md` at commit `558b22acb8aab81ab3026d316233241832332944` ([link](https://github.com/weni-ai/weni-integrations-engine/blob/558b22acb8aab81ab3026d316233241832332944/specs/002-fix-security-audit/contracts/facebook-webhook.md)).
- How the Flows forward authenticates (the `?token=` query parameter in `SendWebhooks`). This spec only moves that forward behind the signature check.
- Adding HTTP 500 to `utils.shouldRetryTransient`. That is constitution follow-up `TODO(RETRY_5XX)`. It changes WWC retries too, so it belongs to its own change.
- Verifying `X-Hub-Signature-256` instead of the SHA-1 `X-Hub-Signature`.
- Rotation with two valid tokens at once. Integrations accepts only one token.

**Depends on**:
- **weni-integrations-engine**: the contract above. Integrations expects the header `Authorization: Bearer <token>`: the scheme is case-sensitive and followed by exactly one space. It answers 401 with `WWW-Authenticate: Bearer` on a missing, wrong or unconfigured token, and the caller must treat that 401 as a configuration error, not a transient one.
- **Platform or secrets team**: inject the same secret value into Courier as `COURIER_WHATSAPP_CLOUD_WEBHOOKS_TOKEN` and into integrations as `FACEBOOK_WEBHOOK_TOKEN`, in staging and production.

## Current State

- `handlers/webhook.go:102` `SendWebhooks(r, url_, tokenFlows, isIntegrations)` builds both forwards.
  - The integrations branch appends `/api/v1/webhook/facebook/api/notification/`.
  - It re-encodes the body through `moTemplatesPayload`, sets only `Content-Type`, and calls `utils.MakeHTTPRequest` once, with no retry and no credential.
  - The Flows branch appends `/whatsapp_flows/?token=<tokenFlows>`.
- `handlers/facebookapp/facebookapp.go:858` `GetChannel` handles `object == "whatsapp_business_account"`.
  - `routeWhatsAppChanges` (line 146) flags integration fields (`integrationWebhookFields`, line 195) and `flows`.
  - Lines 894–908 call `SendWebhooks` for each target, then `handleMMLiteTermsSigned`. A forward error only goes to `courier.LogRequestError`.
  - When the payload has no channel address, `GetChannel` returns the sentinel `"template update, so ignore"`, and `server.go:313` turns it into a 200 to Meta. A failed forward is therefore never redelivered.
- `handlers/facebookapp/facebookapp.go:1037` `receiveEvent` calls `validateSignature` only after `GetChannel` has resolved a channel. Requests with `X-Router-Token` skip it entirely (line 1022). The forward in `GetChannel` therefore runs on unauthenticated input.
- `handlers/facebookapp/facebookapp.go:3859` `validateSignature` returns `invalid request signature, expected: <hmac> got: <sig> for body: '<body>'`.
  - `WriteAndLogRequestError` writes that string into the 400 response and the log.
  - A caller can therefore read the valid signature for any body it chooses and resend the body signed. The log also carries message text, which Principle III forbids.
  - `handlers/facebookapp/facebookapp_test.go:59,120` assert only the `invalid request signature` prefix.
- `utils/http.go:207` `MakeHTTPRequestWithRetry(ctx, req, attempts, baseBackoff, idempotencyKey)`:
  - It retries connection and timeout errors and 429, 502, 503 and 504 (`shouldRetryTransient`), and does not retry other 4xx or 500.
  - It clones headers on every attempt and sets `Idempotency-Key` on POST.
  - It is already used by `handlers/weniwebchat/weniwebchat.go:285` with 3 attempts and 500 ms.
  - `MakeHTTPRequestWithClient` dumps the full outgoing request, `Authorization` included, into `RequestResponse.Request`.
- `config.go:42-44` declares `WhatsappCloudWebhooksUrl`, `WhatsappCloudWebhooksUrlFlows` and `WhatsappCloudWebhooksTokenFlows`. `config.go:87` `TelephonyResolveToken` and `handlers/telephony/telephony.go:68` are the existing pattern for a bearer token whose empty value fails closed.
- `handlers/test.go:223` `NewTestServer` sets `WhatsappCloudApplicationSecret = "wac_app_secret"`, and `addValidSignatureWAC` (`facebookapp_test.go:364`) signs WAC test bodies. `handlers/telephony/telephony_test.go:151` shows a test setting config on the server. `handlers/webhook_test.go` covers `SendWebhooks` against `httptest` servers, but only the success path.

## Technical Approach

### Components Affected

| Component | Change | Why |
|-----------|--------|-----|
| `config.go` | modified: add `WhatsappCloudWebhooksToken string` (help: bearer token sent to integrations on forwarded WhatsApp Cloud webhooks; empty disables the forward). No default. | The secret Courier sends. It is injected from the secrets manager (Principle II). |
| `handlers/webhook.go` | modified: new `ForwardToIntegrations(ctx context.Context, r *http.Request, baseURL, token string) error`. `SendWebhooks` keeps only the Flows path and drops its `isIntegrations` parameter, because `facebookapp` is its only caller. Named constants `integrationsForwardAttempts = 3`, `integrationsForwardBaseBackoff = 500 * time.Millisecond` and `integrationsWebhookPath`. `integrationsForwardTimeout = 10 * time.Second` is a named package-level variable, so the deadline test can shorten it. | It separates the authenticated, retried integrations forward from the Flows one, and removes the boolean flag parameter (Principle XV). |
| `handlers/facebookapp/facebookapp.go` (`GetChannel`, WAC branch) | modified: when `routing.toIntegrations \|\| routing.toFlows`, call `h.validateSignature(r)` first. On failure, skip both forwards and `handleMMLiteTermsSigned`, and log a `WARN` without a body. On success, call `ForwardToIntegrations` and `SendWebhooks`. When the payload has no channel address, return a forward error instead of the ignore sentinel, so Meta redelivers. | It closes the forgery path that the token would otherwise authenticate (Principle IX), and makes a failed forward recoverable (Principle XI). |
| `handlers/facebookapp/facebookapp.go` (`validateSignature`) | modified: return `invalid request signature` without the expected signature, the received signature or the body. | It removes the signature oracle and the message text from logs (Principles III and IX). Without this, the new check can be bypassed. |
| `handlers/webhook_test.go` | modified | Unit and seam tests for the forward: header, empty token, retry, no retry on 401, deadline. |
| `handlers/facebookapp/facebookapp_test.go` | modified | Flow tests from Meta webhook to forward, plus signature-response assertions on FBA, IG and WAC. |
| `WENI-CHANGELOG.md` | modified | Entry for the release, as a MINOR bump (new setting). |

### Flow

1. Meta POSTs to `/c/wac/receive`. `server.go` calls `GetChannel`.
2. `GetChannel` decodes `moPayload` and runs `routeWhatsAppChanges`.
3. When the payload carries integration or Flows fields:
   1. `validateSignature(r)` computes HMAC-SHA1 over the raw body with `WhatsappCloudApplicationSecret` and compares it with `hmac.Equal`. `ReadBody` restores the body, so later steps still read it.
   2. **Invalid signature**: nothing is forwarded, and `handleMMLiteTermsSigned` does not run. Courier logs `event=wac_webhook_forward_skipped reason=invalid_signature`. With no channel address, `GetChannel` returns `invalid request signature` and Meta gets a 400. With a channel address, it falls through to channel resolution, and `receiveEvent` keeps applying its own rules.
   3. **Valid signature**:
      1. `ForwardToIntegrations` runs when `routing.toIntegrations` is set:
         1. An empty token returns `errIntegrationsTokenNotConfigured`, with no HTTP call.
         2. Otherwise it re-encodes `moTemplatesPayload` as today and builds a `POST` with `Content-Type: application/json` and `Authorization: Bearer <token>`.
         3. The idempotency key is the hex SHA-256 of the forwarded body.
         4. It calls `utils.MakeHTTPRequestWithRetry` under `context.WithTimeout(ctx, integrationsForwardTimeout)`.
         5. The `RequestResponse` is used only for its status code. It is never logged or written to a channel log, because its `Request` trace contains the `Authorization` header.
      2. `handleMMLiteTermsSigned` runs as today.
      3. `SendWebhooks` runs for Flows as today.
4. Forward outcome:
   - **2xx**: with no channel address, the ignore sentinel gives Meta a 200, as today.
   - **Failure, no channel address**: `GetChannel` returns a generic `integrations webhook forward failed`, so Meta gets a non-200 and redelivers.
   - **Failure, with a channel address**: the failure is logged at `ERROR`, and message processing continues. See Implementation Decisions.

### Rejected Alternatives

- **Add the header inside `SendWebhooks` behind `isIntegrations`**: it is the smallest diff. But it keeps two forwards with different authentication, retry and failure rules behind one boolean parameter, and the retry would leak into the Flows forward, which this spec does not cover.
- **Forward unsigned payloads with the token and rely on integrations**: integrations has no Meta app secret and cannot tell a forgery from a real event. The token would then authenticate any payload sent to Courier.
- **Reject every unsigned WAC request in `GetChannel`**: it would also reject `X-Router-Token` message traffic, which skips the signature check by design today. That is a behaviour change outside this spec (Principle XVI). Only the forward is gated.
- **When the token is empty, forward without a header**: it would silently 401 once integrations ships SEC-07. Failing closed makes the misconfiguration visible and keeps events redeliverable.
- **Retry asynchronously through a queue or the spool**: it adds new state and a new mechanism. Meta redelivery already provides durable, bounded redelivery for the payloads where it is safe, and the synchronous retry handles short blips.

## Contracts

| Interface | Change | Version impact | Backward compatible |
|-----------|--------|----------------|---------------------|
| Outgoing `POST {WhatsappCloudWebhooksUrl}/api/v1/webhook/facebook/api/notification/` | modified: adds `Authorization: Bearer <token>` and `Idempotency-Key`. Retried on transient failures. Not sent when the token is unset. | minor | Yes. Integrations before SEC-07 ignores both headers. Integrations after SEC-07 requires the bearer header. |
| Configuration `COURIER_WHATSAPP_CLOUD_WEBHOOKS_TOKEN` | added | minor | Yes, as an addition. Without it the forward fails closed, so it is required before integrations enforces SEC-07. |
| Inbound `POST /c/wac/receive`, payload with only integration or Flows fields | modified: 400 on an invalid signature (today 200), and non-200 on a failed forward (today 200) | patch | Yes for Meta, which signs every delivery and redelivers on non-200. Unsigned callers were never entitled to a forward. |
| Inbound `/c/{fba,ig,wac}/receive`, invalid-signature response body | modified: the message is `invalid request signature`, without the expected signature, the received signature or the body | patch | Yes. The status (400) and the prefix are unchanged. The removed detail was a vulnerability, not a contract. |
| `handlers.SendWebhooks` (Go, internal) | modified: `isIntegrations` parameter removed | none (internal package API, single caller) | Not applicable. It is not a public interface under Principle IV. |

**Consumers affected**: weni-integrations-engine, which receives the header. Its owners are told through this spec and the release notes. Operators provision the new variable. Meta needs no change.

## Non-Functional Requirements

### Peak Load

- **Courier service-wide inbound**: peak of **242.9 req/s** (1-minute rate) at 2026-10-28 14:06 UTC, from Mimir `istio_requests_total{destination_workload="courier", reporter="destination"}` over 2026-10-07 to 2026-11-06. Istio has no path label, so the `/c/wac/receive` share cannot be isolated.
- **Forwarded to integrations**: peak of **11 req/min (about 0.18 req/s)** in the busiest hour (2026-10-30, about 17:00 UTC-3), and at most 130 req/h over 7 days. This is the integrations-side Istio measurement of this exact route, recorded in its spec. Courier's own calls to integrations leave the mesh, so Courier has no outbound series.
- **Added cost**: the signature check adds one HMAC-SHA1 per forwarded payload (at most 1 MB). It runs only on payloads with integration or Flows fields, not on the service peak. In the worst case, every forward makes 3 attempts, which is about 0.55 req/s to integrations. Each worst-case forward holds one request goroutine for at most 10 s.

### Observability

- Every forward outcome other than success logs one structured `logrus` entry:
  - `event`: `integrations_webhook_forward_failed` or `wac_webhook_forward_skipped`
  - `reason`: `token_not_configured`, `unauthorized`, `rejected`, `transient_exhausted` or `invalid_signature`
  - `status_code` (0 when no response arrived)
  - `attempts`
  - `idempotency_key`: the body SHA-256, which is also sent to integrations as `Idempotency-Key`
  - `waba_ids`: the `entry[].id` values, which are opaque WhatsApp Business Account IDs
- Log levels:
  - `token_not_configured`, `unauthorized` and `transient_exhausted` are `ERROR` and reach Sentry through `logrus_sentry`.
  - `rejected` (any other non-retriable status) is `ERROR`.
  - `invalid_signature` is `WARN`, because an attacker can trigger it at will.
- **Correlation identifier**: `idempotency_key`. No channel, channel log or message exists at this point (see Constitution Alignment, Principle XIII).
- Logs, channel logs and Sentry never contain the token, the `Authorization` header, the `RequestResponse.Request` trace, the request body, or the expected signature.

### Failure Handling

- **Timeout**: `integrationsForwardTimeout` (10 s) bounds the whole forward, retries included. A single attempt is also capped by the shared client's 60 s timeout, which the context deadline overrides.
- **Retriable** (`shouldRetryTransient`): connection error, timeout, 429, 502, 503 and 504. At most `integrationsForwardAttempts` (3) attempts, with jittered exponential backoff from `integrationsForwardBaseBackoff` (500 ms).
- **Not retriable**: 401 (a configuration error, as the integrations contract requires), other 4xx, and 500 (see Constitution Alignment).
- **Idempotency**: integrations dispatches `update_templates_by_webhook` and `update_account_info_by_webhook`, which apply Meta's latest state. Applying a delivery twice gives the same result. The `Idempotency-Key` is the body SHA-256, so retries and Meta redeliveries of the same body share one key.
- **Recoverability when retries run out**: for a payload with no channel address (the normal shape of template and account events), Courier answers non-200 and Meta redelivers. For a payload that also carries message events, the failure is logged at `ERROR` and the messages are processed (see Implementation Decisions).
- **Caller-visible failures**: Meta sees 400 with a generic message. No upstream status, header or token is echoed.

### Security and Authorization

- **Trust boundary**: the inbound body and `X-Hub-Signature` are untrusted until `validateSignature` passes. Courier forwards nothing unverified to integrations or Flows.
- **Outbound authentication**: Courier sends `Authorization: Bearer <WhatsappCloudWebhooksToken>`, and integrations compares it in constant time.
- **Signature oracle removed**: the error carries no expected value, so a valid signature cannot be derived from a response.
- **Secret**: `COURIER_WHATSAPP_CLOUD_WEBHOOKS_TOKEN` comes from the secrets manager, with no default and no value in `courier.toml`, tests or docs. Tests use a literal test-only value set on `NewTestServer`'s config.

## Test Strategy

| Success / acceptance criterion | How this repository proves it |
|--------------------------------|-------------------------------|
| AC-01 | `handlers/facebookapp/facebookapp_test.go`: a flow test posts a signed (`addValidSignatureWAC`) `message_template_status_update` webhook to `/c/wac/receive`. The server's `WhatsappCloudWebhooksUrl` points at an `httptest` server, and `WhatsappCloudWebhooksToken` is set. The test asserts one request on the notification path, the exact `Authorization` and `Content-Type`, the forwarded body, and a 200 to the caller. `handlers/webhook_test.go`: `ForwardToIntegrations` sends the header and the `Idempotency-Key`. |
| AC-02 | `handlers/webhook_test.go`: with an empty token, the `httptest` server records zero requests and the function returns `errIntegrationsTokenNotConfigured`. Flow test: the same webhook with an empty token gets a non-200. |
| AC-03 | Flow tests: an integration-only payload and a Flows-only payload, each with `addInvalidSignature` and with no signature, give 400, with zero requests on both the integrations and the Flows `httptest` servers. A mixed payload (template status plus `messages` with a channel's `phone_number_id`), with an invalid signature, makes zero forward requests and still yields today's `receiveEvent` result. |
| AC-04 | `handlers/facebookapp/facebookapp_test.go`: the existing `Receive Invalid Signature` cases for FBA, IG and WAC also assert that the response contains neither `expected` nor the test body. A unit test asserts that the `validateSignature` error equals `invalid request signature`. |
| AC-05 | `handlers/webhook_test.go`: the `httptest` server answers 503, then 200. The test asserts 2 attempts with identical `Authorization` and `Idempotency-Key`, and a nil error. An always-503 server gives exactly 3 attempts and an error. Flow test: 503 then 200 gives a 200 to the caller. |
| AC-06 | `handlers/webhook_test.go`: 401, 400 and 500 each give exactly 1 attempt and an error with the status. Flow test: 401 on an integration-only payload gives a non-200 to the caller. |
| AC-07 | `handlers/webhook_test.go`: a handler that blocks beyond the deadline makes `ForwardToIntegrations` return within `integrationsForwardTimeout` plus a small margin. The test shortens `integrationsForwardTimeout` and restores it afterwards. |
| AC-08 | `handlers/webhook_test.go` and the flow tests capture `logrus` output with `logrus/hooks/test` and assert that the token string is absent from every entry, on the 401, exhausted and empty-token paths. The flow test also asserts that the token is absent from the response body and the channel logs recorded by the `MockBackend`. |

**Flow coverage**: Meta webhook into `/c/wac/receive`, then signature check, then routing, then the authenticated forward (with retry), then the response to Meta. This runs through `RunChannelTestCases`, or `NewTestServer` with config overrides when per-test config is needed, as in `handlers/telephony/telephony_test.go:151`.

**Failure paths covered**: missing signature, invalid signature, empty token, 401, other 4xx, 500, transient errors that recover, transient errors that run out of retries, and the deadline being exceeded.

**Stubbed at the boundary**: integrations and Flows through `httptest.Server`, and the backend through `courier.MockBackend`. No test reaches a real provider or service.

## Rollout and Rollback

This change is step 2 of the integrations contract's binding rollout order.

1. The secrets team provisions one value as `FACEBOOK_WEBHOOK_TOKEN` (integrations) and `COURIER_WHATSAPP_CLOUD_WEBHOOKS_TOKEN` (Courier), in staging and production.
2. Deploy Courier to staging with the variable set. Integrations still ignores the header there. Confirm that template status webhooks still reach integrations, with no `integrations_webhook_forward_failed` log entries and no rise in `wac_webhook_forward_skipped`.
3. Deploy Courier to production, then confirm the same signals for 24 hours. A rise in `invalid_signature` from legitimate traffic would mean a Meta delivery path that is not signed with `WhatsappCloudApplicationSecret`, and blocks step 4.
4. Integrations ships SEC-07 to staging, then to production. Watch for `reason=unauthorized` in Courier.

**Rollback**: redeploy the previous Courier tag. Before step 4, this is fully safe. After step 4, the previous Courier sends no header, so integrations answers 401. The old code drops that silently and Meta gets a 200, so the events are lost until Courier is redeployed or integrations rolls back too. Rolling back Courier after step 4 therefore requires rolling back integrations' SEC-07 first, which its contract states is safe. This change persists nothing and needs no data step.

## Constitution Alignment

| Principle | How this spec complies | Exception + justification |
|-----------|------------------------|---------------------------|
| II. Secrets and Supply Chain | The token is injected through a `COURIER_*` variable from the secrets manager, with no default and no committed value. | none |
| III. Structured, Privacy-Safe Observability | `logrus` fields only. The body and message text are removed from the signature log. The token and the request trace are never logged. | none |
| IV. Versioned Contracts | Every changed interface is listed with its SemVer impact. The header ships before integrations enforces it. | The `/c/wac/receive` status change for unsigned or failed forwards and the shorter signature error ship as patch, with no deprecation window. Unsigned callers were never entitled to a forward, the removed error detail is a vulnerability, and Meta (the only legitimate caller) signs every delivery and redelivers on non-200. |
| V. Specification Traceability | Opens with `## Origin Without Product Spec` in the mandated format. | none |
| VIII. Release Changelog | `WENI-CHANGELOG.md` entry for the MINOR release. | none |
| IX. Never Trust the Client | The Meta signature is verified before the payload is forwarded. The oracle that defeated verification is removed. | `X-Router-Token` requests keep skipping signature verification for channel processing, as today. Only their forward is now gated. Changing router traffic is outside this spec (Principle XVI). |
| X. Fail Gracefully and Predictably | The shared client, an explicit 10 s deadline, and generic errors to Meta. | none |
| XI. Bounded Retry Over REST | `MakeHTTPRequestWithRetry` with named attempt and backoff constants, retries only on transient failures, the `Idempotency-Key`, and Meta redelivery on exhaustion. | HTTP 500 is not retried, because `shouldRetryTransient` excludes it (`TODO(RETRY_5XX)`), and changing it would alter WWC retries. A forward that fails on a payload that also carries message events is logged at `ERROR` but not made redeliverable. Answering non-200 would make Meta redeliver the messages too, and Courier would duplicate them. Meta sends template and account events in their own deliveries, so the mixed shape is rare. |
| XII. Stateless, Peak-Sized Services | No new state. Peak declared above. | Per-route peak is unavailable, since Istio has no path label. The service-wide peak and the integrations-side measurement of the forwarded route are used. |
| XIII. Diagnosable Errors | Sentry events carry `waba_ids`, `idempotency_key`, `status_code` and `reason`. | No project, channel UUID or contact UUID exists when the forward runs, because it happens in `GetChannel`, before any channel is resolved, and template events belong to a WABA, not a channel. The opaque WABA ID and the body hash take their place. |
| XIV. Tests Exercise Flows | Flow tests from the webhook to the forward cover success and every failure path listed above. | none |
| XV. Explicit Over Clever | Named constants for attempts, backoff, timeout and path. The boolean `isIntegrations` parameter is replaced by a dedicated function. | none |
| XVI. Contained Changes | Limited to the WAC forward path, its config, and `validateSignature`, whose leak defeats the new check. The Flows authentication, the router path and `shouldRetryTransient` are untouched. | `validateSignature` also serves FBA and IG. Fixing its message there is unavoidable, because it is one function, and it is the same vulnerability. |

## Implementation Decisions

- **Setting name `WhatsappCloudWebhooksToken`**: it pairs with the existing `WhatsappCloudWebhooksUrl` and `WhatsappCloudWebhooksTokenFlows`. `ezconf` maps it to `COURIER_WHATSAPP_CLOUD_WEBHOOKS_TOKEN`.
- **Fail closed when the token is empty**: no request is sent, the outcome is logged at `ERROR`, and integration-only payloads are made redeliverable. This mirrors `TelephonyResolveToken` and the integrations contract.
- **The signature check gates the forward, not channel processing**: mixed and router-token payloads keep today's `receiveEvent` behaviour. Only what leaves Courier toward integrations and Flows is gated.
- **Meta redelivery is the durable retry**: with no channel address, a failure answers non-200. It reuses `WriteError`'s 400 through `server.go`, with a generic message.
- **The `Idempotency-Key` is the SHA-256 of the forwarded body**: retries and Meta redeliveries of the same event share it. Integrations ignores it today, and it is harmless if integrations starts deduplicating later.
- **The retry budget is 3 attempts, 500 ms base backoff and a 10 s total deadline**: this matches the existing WWC usage, and it keeps Courier's response to Meta within a range Meta tolerates.
- **The forward error does not echo the upstream status to Meta**: the status goes to logs only.
