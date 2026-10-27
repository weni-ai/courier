# Implementation Plan: PSTN Telephony Channel (Courier)

**Branch**: `feat/telephony-channel` | **Date**: 2026-07-21 | **Spec**: [spec.md](./spec.md)

## Summary

Add a new Courier channel handler `handlers/telephony` for PSTN voice mode. The handler resolves channels by DID (channel address), ingests committed transcripts from the voice gateway as inbound `tel:` messages, and delivers outbound agent text to the gateway `base_url` for TTS playback.

## Technical Context

**Language/Version**: Go (module `github.com/nyaruka/courier`)  
**Primary Dependencies**: chi router, gocommon/urns, testify, existing `handlers` package utilities  
**Storage**: PostgreSQL via `backends/rapidpro` (unchanged)  
**Testing**: `go test ./handlers/telephony/...` with `RunChannelTestCases`  
**Target Platform**: Linux containers (Courier service)  
**Project Type**: Channel handler package  
**Performance Goals**: Standard Courier handler latency; no blocking external calls on receive path  
**Constraints**: Must align channel type `TPH` with upcoming Flows channel type; DID stored as channel address  
**Scale/Scope**: New handler only; no backend schema changes in Courier

## Constitution Check

| Principle | Status | Notes |
| --------- | ------ | ----- |
| I. Clear Go Channel Handlers | PASS | New package under `handlers/telephony/` following BaseHandler patterns |
| II. Channel Contract & URN Discipline | PASS | `tel:` URNs, DID-based lookup, `TPH` channel type |
| III. Secrets & Security | PASS | Optional auth token via channel config; no secrets in logs |
| IV. Test-First Quality Gates | PASS | Handler receive + send tests included |
| V. Observability | PASS | Uses standard channel logging on send errors |
| VI. Fidelity to Product Spec | PASS | Implements BD-010 Courier slice |
| VII. Release Alignment | PASS | Additive channel type; coordinate with Flows `TPH` release |

## Project Structure

### Documentation (this feature)

```text
specs/001-pstn-telephony-channel/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── gateway-api.md
└── checklists/
    └── requirements.md
```

### Source Code

```text
handlers/telephony/
├── telephony.go
└── telephony_test.go
```

**Structure Decision**: Single handler package mirroring `handlers/weniwebchat` and `handlers/thinq`, with address-based routing like `handlers/facebookapp`.

## Complexity Tracking

No constitution violations requiring justification.
