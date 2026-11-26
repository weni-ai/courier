# Engineering Spec Quality Checklist: WAC Handover Synthetic Inbound Message

**Purpose**: Validate the engineering spec before planning or implementation
**Created**: 2026-11-26
**Feature**: [spec.md](../spec.md)

## Origin

- [x] Product Spec is `none`, and the origin section matches the format required by the constitution, field for field
- [x] Problem and acceptance criteria are concrete and observable
- [x] Every acceptance criterion maps to a test in this repository

## Technical Completeness

- [x] Scope in this repository is bounded, and what other repos own is named
- [x] Current state references real paths in this codebase
- [x] Technical approach names the components affected and the flow between them
- [x] Every changed public interface appears in Contracts with its SemVer impact
- [x] Backward compatibility or a deprecation path is stated for each breaking change
- [x] Data model and migration reversibility are covered, or the feature persists nothing
- [x] Peak load is declared as a peak, with its window
- [x] Observability states what is logged, with which context, and which correlation identifier
- [x] Failure handling states timeouts, retriable failures, bounds and idempotency
- [x] The trust boundary and the server-side authorization check are identified

## Verifiability

- [x] Every success or acceptance criterion maps to a test in this repository
- [x] Flow coverage covers complete use cases, not only isolated methods
- [x] Failure paths are covered, not only the success path
- [x] Rollout and rollback are described, and rollback is safe

## Constitution

- [x] Every principle this feature touches has a row in Constitution Alignment
- [x] Every departure from a MUST is justified in the spec, not left to the code
- [x] No obligation of the constitution is left as an unfilled placeholder

## Notes

- Cycle: SDD lite. Go straight to implement; no plan/tasks.
- Survey findings the request did not mention (recorded in the spec): `NewIncomingMsg` runs `checkMsgSeen`, so two empty synthetic messages for the same contact within 2–4 s collapse into one write (accepted, context stays in the pending row); with no timestamp, `ReceivedOn` falls back to now instead of epoch 0.
- No feature flag (team decision): the behavior is the default for WAC `control_passed`. Rollback is a redeploy.
- Review follow-up applied: Mailroom paths that bypass the brain (open ticket, trigger, active session, `BrainOn` off) recorded as an accepted effect plus Mailroom follow-ups; AC-08 uses a `wacHandoverClaimSeen` seam (closing the Redis pool fails earlier in `applyWACThreadOwner`); `MockBackend` pending-row state defined.
- Items marked incomplete require spec updates before `/speckit-implement`
