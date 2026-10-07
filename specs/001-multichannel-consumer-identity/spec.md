# Engineering Spec: Multichannel consumer identity

**Feature Branch**: `feat/multichannel-consumer-identity`  
**Created**: 2026-10-07  
**Status**: Draft  
**Product spec**: `vtex-cx-engine-specs` / `specs/009-multichannel-customer-identity/spec.md`

This is the Courier engineering spec. Product requirements and binding decisions live in the product spec and MUST be followed. This document records the HOW inside Courier.

## Inheritance from Product Spec

- Product Spec: Multichannel Consumer Identity — `vtex-cx-engine-specs/specs/009-multichannel-customer-identity/spec.md`
- Pinned version: `c8d007a120bd6cccb67c2eba92afaab773434fc1`
- Architecture doc: `vtex-cx-engine-specs/specs/009-multichannel-customer-identity/architecture.md` @ `c8d007a120bd6cccb67c2eba92afaab773434fc1`
- Inherited binding decisions: BD-001–BD-022, applied only to the Courier slice
- Scope of this spec: inbound ingest binds each new channel message to one protocol before it is stored, and keeps accepting the message when protocol resolution fails
- Divergences: none

Out of this repository: who the person is and attach (`flows`); protocol lifecycle, timers, and follow-up rules (`mailroom`); the agent attach command (`weni-cli`). Courier does not issue the Web Chat URN.

Coordination plan: `docs/plans/multichannel-consumer-identity-plano.md` in the workspace.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Inbound messages are stored with a protocol (Priority: P1)

After the contact is resolved from the URN, Courier asks Mailroom for the protocol and stores that id on the message and on the event Mailroom will consume. Attach is not called on this path. WhatsApp, Web Chat, Instagram, email, and SMS share this step.

**Why this priority**: A channel message cannot exist without a unit of service, and Courier is the writer on the hot path.

**Independent Test**: Receive one WhatsApp message for a new URN. Assert the stored message has a non-null `protocol_id`, the contact URN is unchanged aside from normal creation, and no attach request was made.

**Acceptance Scenarios**:

1. **Given** a new inbound message, **When** Courier accepts it, **Then** the stored row and the downstream event both carry the `protocol_id` returned by Mailroom.
2. **Given** a URN that already has an open protocol, **When** another message arrives, **Then** Courier stores the id Mailroom returned for that open protocol.
3. **Given** the inbound path, **When** the message is accepted, **Then** Courier does not call attach and does not wait on commerce.

### User Story 2 - A failed resolve still accepts the message (Priority: P1)

If the Mailroom resolve call times out or errors, Courier inserts a new open protocol in the same database and stores the message on it. The message is not dropped and not duplicated.

**Why this priority**: Identity and protocol resolution must not stop channel ingest.

**Independent Test**: Force the Mailroom client to fail, receive one message, and assert exactly one new message and one new open protocol.

**Acceptance Scenarios**:

1. **Given** Mailroom resolve returns an error or exceeds its budget, **When** an inbound message arrives, **Then** Courier stores the message on a newly opened protocol.
2. **Given** that fail-safe insert, **When** the same external message is received again, **Then** Courier does not create a second message.
3. **Given** the identity graph is unavailable, **When** an inbound message arrives, **Then** Courier still stores it on the channel URN it already resolved.

### Edge Cases

- A Web Chat URN invented only by the browser is not minted here. Courier persists the URN the channel already presented.
- Logs include org, channel, and `protocol_id`. They do not include anchors or personal data.
- Outbound Courier send carries `protocol_id` when the queued message has one. Refusal of a closed protocol happens in Mailroom before the message is queued.
- Stream messages written by Flows are outside this path and may have a null `protocol_id`.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Courier MUST call Mailroom protocol resolve after `GetContact` and before `writeMsg` on inbound channel messages.
- **FR-002**: Courier MUST persist the returned `protocol_id` on `msgs_msg` and on the handler event.
- **FR-003**: Courier MUST, when resolve fails, insert one open `msgs_protocol` row and store the message on it.
- **FR-004**: Courier MUST NOT call attach, detach, or a commerce connector during ingest.
- **FR-005**: Courier MUST apply this path to WhatsApp, Weni Web Chat, Instagram, email, and SMS inbound handlers.
- **FR-006**: Courier MUST NOT decide who the person is and MUST NOT close a protocol.

### Key Entities

- **Inbound message**: existing courier message plus `protocol_id` before it is written.
- **Fail-safe protocol**: an `open` protocol row created by Courier only when Mailroom resolve does not return.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of scripted inbound messages that receive a resolve response are stored with that `protocol_id`.
- **SC-002**: With the resolve client failing, 100% of scripted inbound messages are still stored, each on exactly one new open protocol.
- **SC-003**: No scripted ingest test issues an attach call.
- **SC-004**: A repeated external id does not create a second stored message in the fail-safe path.

## Assumptions

- Flows has already added nullable `msgs_msg.protocol_id` and table `msgs_protocol` before this service writes them.
- The resolve request and response fields are those in the Mailroom engineering spec: `project_id`, `urn_id`, `contact_id`, `channel_id`, external message id, optional `protocol_id` in; `protocol_id`, `created`, `predecessor_id` out.
- The HTTP path is an internal Mailroom route. The exact URL is fixed in the Courier plan, and the fields do not change if the module is later extracted.
- Courier keeps using `contactForURN` for the provisional contact. It does not implement the identity graph.
