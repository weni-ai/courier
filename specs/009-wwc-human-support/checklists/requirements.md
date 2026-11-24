# Engineering Spec Quality Checklist: WWC human-support forwarding

**Purpose**: Validate the engineering spec before planning or implementation
**Created**: 2026-11-23
**Feature**: [spec.md](../spec.md)

## Inheritance

- [x] Exactly one Product Spec is referenced
- [x] Pinned version is an immutable commit or tag, not a bare URL or ID
- [x] The inheritance section matches the format required by the constitution, field for field
- [x] Architecture doc is either `none` or pinned by commit/tag
- [x] Inherited binding decisions are listed
- [x] The spec does not redefine problem, scope rationale, user journeys or success criteria
- [x] Divergences is `none`, or links to an amendment in the product repository

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

Validation passed on 2026-11-23. One pass, no failing items. The same day, `message_status` HTTP 404 was changed to three in-request attempts (500ms backoff with jitter) before `MsgErrored`. 403 stays on the mailroom schedule. Failure handling, tests, Principle XI, and Implementation Decisions match that rule.

The product spec is pinned to commit `59dabf0b030431fae835756a43aa7efd8d58f953` (clean `origin/main`). That commit has no git tag, and the product document status is still Draft. Principle V records this as a departure: the SHA is an immutable pin, and a tag is still required before this spec should be treated as mergeable against a ratified product spec.

SC-001 is mapped to the Courier half only (forwarded times and stored `message_id`). The widget comparison belongs to webchat-react and the socket.

Rollback is safe when Engine stops emitting the new `action_type` values before the Courier image is rolled back. That order is in the spec.
