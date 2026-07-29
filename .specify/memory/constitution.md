<!--
  SYNC IMPACT REPORT
  ==================
  Version change: 0.0.0 → 1.0.0 (Initial adoption)
  Modified principles:
    - Added I. Clear, Idiomatic Go Channel Handlers
    - Added II. Channel Contract & URN Discipline
    - Added III. Secrets, Security & Least Privilege
    - Added IV. Test-First Quality Gates
    - Added V. Observability & Operational Resilience
    - Added VI. Fidelity to the Product Spec
    - Added VII. Release & Infrastructure Alignment
  Added sections:
    - Engineering Standards
    - Delivery Workflow
    - Governance
  Removed sections: None
  Templates requiring updates:
    - .specify/templates/plan-template.md ✅ verified (Constitution Check present)
    - .specify/templates/spec-template.md ✅ verified
    - .specify/templates/tasks-template.md ✅ verified
  Follow-up TODOs: None
-->

# Courier Constitution

## Core Principles

### I. Clear, Idiomatic Go Channel Handlers

Production code MUST be readable without extensive commentary. Each channel
handler MUST follow the existing `handlers.BaseHandler` pattern, register
routes in `Initialize`, and delegate non-trivial logic to testable helpers.
Rationale: Courier is a high-throughput message router; unclear handler
code causes production incidents that are hard to trace across channels.

- Go code MUST favor explicit control flow, descriptive names, and small
  composable functions.
- New channel handlers MUST live under `handlers/<channel>/` and register
  via `courier.RegisterHandler` in `init()`.
- Exported types and functions MUST include GoDoc comments describing
  behavior and constraints.
- Debug code, dead branches, and commented-out implementations MUST not
  be committed.
- Files exceeding roughly 500 lines MUST be justified in the plan or
  refactored.

### II. Channel Contract & URN Discipline

Channel behavior MUST be deterministic from the incoming payload, channel
configuration, and approved external contracts. Contact identity MUST use
the correct URN scheme for the channel type. Rationale: incorrect URNs or
ambiguous payloads break downstream Flows/Mailroom routing and contact
resolution.

- Handlers MUST validate or normalize incoming messages at the boundary
  before dispatching to internal logic.
- Contact URNs MUST use `github.com/nyaruka/gocommon/urns` and follow the
  scheme defined in the Engineering Spec (e.g., `tel:` for PSTN telephony).
- Channel types MUST be registered as `courier.ChannelType` constants and
  stay consistent with the matching Flows channel type.
- Configuration MUST come from channel config or environment variables,
  never from hardcoded tenant or routing values.
- HTTP handlers MUST set explicit timeouts on outbound calls to Flows and
  external providers.

### III. Secrets, Security & Least Privilege

Sensitive data MUST be protected across code, logs, and infrastructure
interactions. Rationale: Courier handles channel credentials, message
payloads, and contact identifiers.

- Secrets MUST never be hardcoded, committed, or written to logs.
- Credential access MUST flow through channel configuration or dedicated
  configuration modules.
- External responses MUST be handled defensively, with actionable errors
  that do not leak sensitive payloads or internal state.
- New dependencies affecting authentication, transport, or cryptography
  MUST be introduced deliberately and documented in the plan.

### IV. Test-First Quality Gates

Every behavior change MUST be backed by automated tests before review.
Rationale: channel handlers are integration boundaries; regressions affect
all tenants on that channel type.

- New behavior MUST include tests that fail before implementation and pass
  afterward.
- Handler logic MUST have unit tests using `testify` and table-driven
  patterns consistent with existing handlers (e.g., `handlers/whatsapp/`).
- Changed modules SHOULD maintain coverage consistent with surrounding
  handlers unless the plan records an approved exception.
- `go test ./...` MUST pass locally before code review and in CI before
  merge.
- Bug fixes MUST include a regression test whenever technically feasible.

### V. Observability & Operational Resilience

Production behavior MUST be diagnosable from logs, metrics, and explicit
failure paths. Rationale: Courier processes high message volume; weak
telemetry makes channel-specific failures hard to isolate.

- Logs MUST include enough context to trace the operation (channel UUID,
  message type, handler name) but MUST exclude secrets and personal data.
- Error handling MUST distinguish retriable upstream failures from
  permanent validation or contract errors.
- Metrics and health checks MUST be preserved when adding new handlers.
- Silent exception swallowing is forbidden unless explicit recovery
  behavior and logging are present.

### VI. Fidelity to the Product Spec

Engineering work MUST inherit and MUST NOT contradict the ratified Product
Spec from `vtex-cx-engine-specs`. Rationale: Courier implements channel
attribution and messaging contracts defined at the product layer.

- Every Engineering Spec MUST open with an **Inheritance from Product Spec**
  section: URL, pinned commit/tag, inherited binding decisions, scope slice,
  and divergences (or none).
- Binding decisions from the Product Spec MUST be implemented verbatim.
- Any need to diverge MUST be raised as an amendment in
  `vtex-cx-engine-specs` before code encodes the change.
- The Product Spec defines *what*; this repository owns *how* to build it.

### VII. Release & Infrastructure Alignment

Application changes MUST ship in a way that preserves semantic versioning
and downstream deployment automation. Rationale: Courier is versioned with
the Flows/Mailroom release train.

- Release-impacting changes MUST document whether they require a new image
  tag or coordinated rollout with Flows/Mailroom.
- Breaking handler contracts or channel configuration schemas MUST trigger
  a MAJOR version discussion before merge.
- Required follow-up in deployment configuration MUST be captured in the
  plan or pull request notes.

## Engineering Standards

- Runtime code MUST remain compatible with the Go version declared in
  `go.mod`.
- Dependencies MUST be minimal, justified, and added through `go mod`.
- New channel handlers MUST mirror the structure of existing handlers:
  `handler.go`, `handler_test.go`, and registration in `init()`.
- Backend integration with RapidPro/Flows MUST use existing
  `backends/rapidpro` patterns.
- Formatting MUST follow `gofmt`; tests MUST use patterns from neighboring
  handlers in the same package.

## Delivery Workflow

- Specs MUST capture user scenarios, edge cases, functional requirements,
  non-functional requirements, and measurable success criteria before
  planning.
- Plans MUST include a Constitution Check covering handler structure, URN
  discipline, security, tests, observability, Product Spec fidelity, and
  release impact.
- Tasks MUST include mandatory test work and any configuration follow-up.
- Pull requests MUST explain runtime impact and rollback considerations.

## Governance

This constitution is the authoritative engineering policy for the Courier
repository. All specifications, plans, tasks, and code reviews MUST
enforce it.

**Amendment Process**:
1. Propose changes in a pull request that updates
   `.specify/memory/constitution.md`.
2. Record the semantic version bump rationale in the Sync Impact Report.
3. Obtain approval from Courier maintainers before merge.

**Versioning Policy**:
- MAJOR: Remove or materially redefine a principle or governance rule.
- MINOR: Add a principle or section, or expand requirements.
- PATCH: Clarify wording or non-semantic guidance.

**Compliance Review**:
- Every plan MUST pass the Constitution Check before and after design.
- Every pull request MUST show how tests and Product Spec fidelity were
  addressed.
- Reviewers MUST reject changes that bypass required tests or binding
  decisions.

**Version**: 1.0.0 | **Ratified**: 2026-07-21 | **Last Amended**: 2026-07-21
