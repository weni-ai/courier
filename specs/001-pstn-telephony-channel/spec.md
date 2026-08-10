# Engineering Spec: PSTN Telephony Channel (Courier)

**Feature Branch**: `feat/telephony-channel`  
**Created**: 2026-07-21  
**Status**: Draft  
**Input**: Implement PSTN telephony channel in Courier per Product Spec 004-voice-mode-telephony.

## Inheritance from Product Spec

- **Product Spec**: Voice Mode for Telephony — `vtex-cx-engine-specs/specs/004-voice-mode-telephony/spec.md`
- **Pinned version**: `004-voice-mode-telephony` (branch)
- **Inherited binding decisions**: BD-001, BD-010 (PSTN as dedicated Courier channel type; `tel:` URN; DID→channel via channel config; Courier owns URN construction)
- **Scope of this spec**: Courier-only — new `TPH` channel handler, DID-based channel resolution, inbound transcript ingestion, outbound agent text delivery to gateway
- **Divergences**: none

## User Scenarios & Testing

### User Story 1 - Resolve channel and contact from an inbound voice turn (Priority: P1)

When the voice gateway forwards a committed caller transcript, Courier resolves the tenant channel from the dialed number (DID), builds a `tel:` contact URN from the caller ID, and records the inbound message in the RapidPro pipeline.

**Why this priority**: Without channel/contact attribution and message ingestion, the agent pipeline has no conversation context.

**Independent Test**: POST a valid receive payload with DID and caller ID; verify a `tel:` URN inbound message is queued for the matching channel.

**Acceptance Scenarios**:

1. **Given** a PSTN channel configured with DID `+15551234567`, **When** the gateway sends a text message for that DID and caller `+15559876543`, **Then** Courier resolves the channel and creates/updates a contact with URN `tel:+15559876543`
2. **Given** the message text is non-empty, **When** the receive request is processed, **Then** an inbound message is written to the backend exactly once
3. **Given** the message text is empty, **When** the receive request is processed, **Then** the request is rejected and no message is written
4. **Given** the DID does not match any channel, **When** the receive request is processed, **Then** the request fails with a channel-not-found error
5. **Given** the origin is not `pstn`, **When** the receive request is processed, **Then** the request is rejected

---

### User Story 2 - Deliver agent responses to the voice gateway (Priority: P1)

When Mailroom sends an outbound message on a PSTN channel, Courier forwards the agent text to the configured gateway URL so it can be synthesized and played to the caller.

**Why this priority**: Voice conversations require spoken agent replies routed back through the gateway.

**Independent Test**: Trigger `SendMsg` on a TPH channel with `base_url` configured; verify an HTTP POST to `{base_url}/send` with the caller URN and message text.

**Acceptance Scenarios**:

1. **Given** a channel with `base_url` configured, **When** an outbound text message is sent, **Then** Courier POSTs JSON to `{base_url}/send` and marks the message sent on success
2. **Given** `base_url` is missing, **When** an outbound message is sent, **Then** Courier returns an error without calling the gateway
3. **Given** the gateway returns a non-2xx response, **When** send is attempted, **Then** the message is marked errored with a channel log

---

### User Story 3 - Withheld caller identity (Priority: P2)

When caller ID is missing or withheld, Courier still attributes the call using a stable synthetic `tel:` identity derived from the call session ID.

**Why this priority**: Product spec requires handling withheld numbers without dropping the call.

**Independent Test**: Send receive payload with empty `caller_id` and a `call_id`; verify a stable `tel:` URN is used.

**Acceptance Scenarios**:

1. **Given** `caller_id` is empty and `call_id` is present, **When** the message is received, **Then** Courier uses a synthetic `tel:` URN scoped to the call session

---

### Edge Cases

- Invalid JSON or schema validation failure → 400, no message written
- Duplicate `message_id` external ID → handled by backend deduplication semantics
- Non-text inbound message types → ignored with 200 for forward compatibility
- Auth token configured on channel → gateway requests must include matching `Authorization` header on outbound (optional inbound validation deferred)

## Requirements

### Functional Requirements

- **FR-001**: Courier MUST register channel type `TPH` (Telephony PSTN) with address-based routing (no channel UUID in URL path)
- **FR-002**: Courier MUST resolve channels via `GetChannelByAddress` using the `did` field from inbound payloads
- **FR-003**: Courier MUST accept only `origin=pstn` for this handler in v1
- **FR-004**: Courier MUST construct contact URNs with scheme `tel:` from normalized caller ID
- **FR-005**: Courier MUST reject blank inbound text messages
- **FR-006**: Courier MUST forward outbound text to `{base_url}/send` using a JSON contract aligned with gateway expectations
- **FR-007**: Courier MUST include `call_id` in outbound payloads when present in message metadata
- **FR-008**: Courier MUST support withheld caller ID via synthetic `tel:` URNs tied to `call_id`

### Key Entities

- **PSTN Channel**: Courier channel instance with type `TPH`, address = DID, config `base_url` (+ optional `auth_token`)
- **Inbound voice turn**: JSON payload from gateway with `did`, `caller_id`, `call_id`, and committed transcript text
- **Outbound voice response**: Agent text routed to gateway for TTS playback

## Success Criteria

- **SC-001**: Valid inbound payloads create `tel:` URNs and inbound messages in 100% of happy-path tests
- **SC-002**: Outbound messages reach the configured gateway URL with correct recipient and text in 100% of happy-path tests
- **SC-003**: Unknown DID and invalid payloads never write messages to the backend
- **SC-004**: Handler test suite passes in CI (`go test ./handlers/telephony/...`)

## Assumptions

- Gateway (`weni-webchat-socket`) implements the complementary `/send` endpoint (out of scope for this repo)
- Flows channel type `TPH` will be added in a separate engineering spec
- Inbound auth from gateway uses existing Courier channel URL exposure; optional shared-secret validation can be added later
- Only text inbound messages are required for telephony voice v1
