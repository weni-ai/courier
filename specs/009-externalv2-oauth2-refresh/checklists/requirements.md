# Engineering Spec Quality Checklist: ExternalV2 OAuth2 Access-Token Refresh

**Purpose**: Validate the engineering spec before planning or implementation
**Created**: 2026-11-26
**Feature**: [spec.md](../spec.md)

## Origin

- [x] Product Spec is `none`, and the origin section matches the format required by the constitution, field for field
- [x] Problem and acceptance criteria are concrete and observable
- [x] Every acceptance criterion maps to a test in this repository (AC-01 to AC-08, see Test Strategy)

## Technical Completeness

- [x] Scope in this repository is bounded, and what other repos own is named
- [x] Current state references real paths in this codebase
- [x] Technical approach names the components affected and the flow between them
- [x] Every changed public interface appears in Contracts with its SemVer impact
- [x] Backward compatibility or a deprecation path is stated for each breaking change (no breaking change; additive keys)
- [x] Data model and migration reversibility are covered, or the feature persists nothing
- [x] Peak load is declared as a peak, with its window (242.9 sends/s, 1-minute rate, window 2026-10-07 to 2026-11-06: the Courier service-wide figure used as a proxy, since no E2 or outbound series exists)
- [x] Observability states what is logged, with which context, and which correlation identifier
- [x] Failure handling states timeouts, retriable failures, bounds and idempotency
- [x] The trust boundary and the server-side authorization check are identified

## Verifiability

- [x] Every success or acceptance criterion maps to a test in this repository
- [x] Flow coverage covers complete use cases, not only isolated methods
- [x] Failure paths are covered, not only the success path
- [x] Rollout and rollback are described, and rollback is safe

## Constitution

- [x] Every principle this feature touches has a row in Constitution Alignment (II, III, IV, VIII, X, XI, XII, XIV, XV, XVI)
- [x] Every departure from a MUST is justified in the spec, not left to the code (XII resolved after Q1 by declaring the peak, no departure; III resolved after Q2 with no departure; XI justified after Q3; XIII justified: no contact UUID)
- [x] No obligation of the constitution is left as an unfilled placeholder (no `[NEEDS CLARIFICATION]` marker remains; the peak-load obligation is filled with the declared service-wide peak)

## Notes

- All items pass. Q1 (peak load) was first answered with option C (unknown, measurement gate) and revised on 2026-11-26: the spec declares the Courier service-wide peak (242.9 sends/s, window 2026-10-07 to 2026-11-06) as a proxy for E2 outbound, because no E2 or outbound series exists. The constitution is unchanged.
- Q2 (channel log redaction) was answered with option A: the E2 handler redacts the `Authorization` value in all its send logs, so Principle III has no remaining departure.
- Q3 (replay policy) was answered with option A: `status_codes` replays on its status, and `body_contains` replays only on a non-2xx response. The Principle XI departure is limited to the credential replay, with the residual risk documented.
- Differences between the design, the request and the code are listed under Implementation Decisions: the log redaction (Q2) and replay policy (Q3) that override the design, redaction applying to every E2 send log, 35s context versus the 60s client timeout, and two additions (`refreshedInThisSend`, sanitized token log).
