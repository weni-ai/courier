<!--
Sync Impact Report:
- Version change: none (unfilled template) → 1.0.0
- Modified principles: n/a (first generation)
- Added principles:
  I. Reviewed Changes on a Protected Main Branch
  II. Secrets and Supply Chain
  III. Structured, Privacy-Safe Observability
  IV. Versioned Contracts
  V. Specification Traceability
  VI. No Silent Divergence
  VII. Conventional, Atomic Commits
  VIII. Release Changelog
  IX. Never Trust the Client
  X. Fail Gracefully and Predictably
  XI. Bounded Retry Over REST
  XII. Stateless, Peak-Sized Services
  XIII. Diagnosable Errors
  XIV. Tests Exercise Flows
  XV. Explicit Over Clever
  XVI. Contained Changes
- Added sections: Technology Stack & Constraints; Development Workflow & Quality Gates; Governance
- Removed sections: none
- Templates requiring updates:
  ✅ .specify/templates/plan-template.md — generic "Constitution Check" gate already defers to this file; no edit required
  ✅ .specify/templates/spec-template.md — no edit required; engineering specs MUST add the inheritance section from Principle V
  ✅ .specify/templates/tasks-template.md — no edit required
- Follow-up TODOs:
  TODO(PRODUCT_SPEC_REPO): the product-spec repository that engineering specs inherit from is not
    referenced anywhere in this repo; record it here once known.
  TODO(PEAK_LOAD): no declared peak load exists for Courier (inbound webhooks/s, outbound sends/s);
    the next engineering spec touching throughput MUST declare it (Principle XII).
  TODO(BRANCH_PROTECTION): confirm that `main` on weni-ai/courier enforces required review and a
    green CI run at the platform level (Principle I).
  TODO(RETRY_5XX): `utils.shouldRetryTransient` retries 429/502/503/504 and transient network
    errors but not HTTP 500; evaluate against Principle XI in a dedicated change.
  TODO(SPOOL_EXCEPTION): the local-disk spool is accepted as a documented exception to Principle XII;
    revisit when a shared durable fallback is available.

Provenance:
- Source: weni-ai/vtex-cx-engineering-constitutions (main)
- Bases: base-constitution.md, backend/base-constitution.md
- Domains: backend
- Precedence: root > backend > project adaptation
-->

# Courier (Weni) Constitution

Courier is Weni's fork of `nyaruka/courier`: a Go messaging gateway that receives
channel webhooks, normalizes them into messages, statuses and events, and sends
outgoing messages to 70+ channel providers (WhatsApp Cloud/on-premises, Facebook,
Instagram, Telegram, Weni Webchat, SMS aggregators, and others). Every principle
below applies to all code in this repository.

## Core Principles

### I. Reviewed Changes on a Protected Main Branch

- All code MUST enter `main` through a pull request on `weni-ai/courier`.
- A merge MUST require at least one approved review and a green CI run
  (`.github/workflows/ci.yml`).
- Direct pushes to `main` MUST be blocked via GitHub branch protection, not by
  convention.

**Rationale:** the policy is only real when enforced by the platform, not by
trust. Peer review and a protected main branch keep history auditable and prevent
unreviewed changes from reaching production.

### II. Secrets and Supply Chain

- Secrets (channel tokens, app secrets, `COURIER_DB`/`COURIER_REDIS` credentials,
  AWS keys, Sentry DSN) MUST never be committed to the repository, including
  local helper scripts, test fixtures, and git remote URLs shared in docs.
- Secrets MUST be provided by an external secrets manager and injected at
  runtime through `COURIER_*` environment variables.
- Access MUST follow least privilege by default.
- Dependencies MUST come only from trusted sources declared in `go.mod`/`go.sum`
  and MUST be checked for known vulnerabilities (e.g. `govulncheck`).

**Rationale:** leaked credentials and untrusted dependencies are among the most
common and most damaging breaches; prevention is far cheaper than remediation.

### III. Structured, Privacy-Safe Observability

- Logs MUST be structured (logrus fields, not interpolated strings) and MUST
  never contain secrets or sensitive personal data. URN paths (phone numbers,
  usernames, e-mails) and message text MUST NOT be written to application logs.
- Channel logs MUST redact provider credentials before persistence.
- Errors MUST be traceable across components through correlation identifiers
  (channel UUID, channel-log UUID, message UUID / external ID).

**Rationale:** structured, privacy-safe telemetry is what makes incidents
diagnosable without creating new data-exposure risks.

### IV. Versioned Contracts

- Any change to a public interface MUST be versioned following SemVer. Public
  interfaces include: inbound webhook routes (`/c/<type>/<uuid>/...`), outgoing
  provider payloads, the message/status/event payloads written to Postgres,
  Redis/Valkey queues and RabbitMQ consumed by Mailroom and other Weni services,
  and the `COURIER_*` configuration surface.
- Changes MUST be backward compatible or ship with an announced deprecation path.
- Silent breaking changes MUST NOT be introduced.

**Rationale:** consumers depend on stable contracts; explicit versioning and
deprecation give them a predictable path to adapt without outages.

### V. Specification Traceability

- Every engineering spec in `specs/` MUST derive from exactly one approved product
  spec and MUST reference it through an immutable, pinned version (commit or tag).
  A mutable URL or ID alone MUST NOT be used.
- The product spec MUST exist and be tagged before its engineering spec is created.
- An engineering spec MUST NOT redefine the "what" it inherits: problem, scope,
  success criteria, and binding decisions belong to the product spec.
- A technical architecture document SHOULD be produced for non-trivial features;
  when it exists it MUST be linked from the engineering spec, pinned by commit/tag.
  Its absence MUST NOT block the engineering spec.
- Every engineering spec MUST open with an inheritance section in exactly this
  format:

```
## Inheritance from Product Spec
- Product Spec: <title> — <URL>
- Pinned version: <commit/tag>
- Architecture doc: <none | URL + commit/tag>
- Inherited binding decisions: <short list>
- Scope of this spec: <slice implemented by this repo>
- Divergences: <none | link to amendment>
```

**Rationale:** traceability from product intent to technical execution keeps
decisions auditable. Pinning the version guarantees every team (Courier, Mailroom,
Flows, etc.) implements the same version of a feature. A mandatory product spec
prevents engineering work without an agreed problem; an optional architecture doc
avoids blocking trivial designs; a single inheritance format keeps the link
machine-checkable across repositories.

### VI. No Silent Divergence

- When a technical need contradicts something inherited from the product spec —
  scope, success criteria, or a binding decision — the divergence MUST NOT be
  implemented silently in code.
- It MUST be raised as an amendment in the product repository and recorded in the
  `Divergences` field of the engineering spec, linking to that amendment.
- Once the amendment is approved and tagged, the engineering spec's
  `Pinned version` MUST be updated to it.
- A technical difference that contradicts nothing inherited is an implementation
  decision, not a divergence, and MUST be recorded in the engineering spec.

**Rationale:** with the product spec as single source of truth, a silent code
deviation makes intent and implementation drift apart with no audit trail.

### VII. Conventional, Atomic Commits

- Commits MUST follow Conventional Commits: `<type>: <description>`, with type in
  `feat`, `fix`, `docs`, `refactor`, `test`, `chore`.
- The description MUST be imperative, specific, and at most 50 characters.
- Commits MUST be atomic: one logical change per commit.

**Rationale:** conventional commits enable automated changelog generation and
semantic versioning; atomic commits simplify bisecting, reverting, and reviewing.

### VIII. Release Changelog

- Courier is a deployable service, not a public library; the root requirement of
  Keep a Changelog format therefore does not bind it. Release notes still MUST be
  maintained.
- Every Weni release MUST have an entry in `WENI-CHANGELOG.md` whose version
  matches the git tag (`X.Y.Z`, pre-releases `X.Y.Z-<suffix>`), and every
  user-facing change MUST be listed under that entry.
- Version bumps MUST follow SemVer. `CHANGELOG.md` is the upstream (nyaruka)
  history and MUST NOT be edited for Weni releases.

**Rationale:** a maintained changelog communicates impact to operators and
downstream services and serves as release documentation; SemVer alignment sets
predictable upgrade expectations.

### IX. Never Trust the Client

- Everything reaching Courier from outside — provider webhooks, Weni Webchat
  clients, internal API callers — MUST be treated as potentially malicious,
  incomplete, or incorrect until validated.
- Every external input MUST be validated for type, format, range, and business
  rules at the handler boundary (`handlers/<channel>/`) before it is written as a
  message, status, or event.
- When a provider supports request signing (e.g. `X-Hub-Signature` for Meta
  channels), the handler MUST verify the signature before processing the payload.
- Authorization MUST be enforced server-side on every request, regardless of
  checks performed by the caller.

**Rationale:** clients run outside the server's control and can be inspected,
modified, or bypassed. Server-side validation prevents injection, data
corruption, and privilege escalation that client-side checks can never stop.

### X. Fail Gracefully and Predictably

- Calls to external dependencies (channel providers, Postgres, Redis/Valkey,
  RabbitMQ, S3, Weni services) MUST have explicit timeouts and MUST NOT block
  indefinitely. Outbound HTTP MUST use the shared clients in `utils/http.go`
  (`GetHTTPClient`/`MakeHTTPRequest*`) or a client with an explicit timeout;
  `http.DefaultClient` MUST NOT be used.
- Failures MUST be handled explicitly and surfaced as consistent responses
  (`handlers/responses.go`, channel-log errors, `MsgStatus` errored/failed) —
  never as unhandled panics or leaked internal details to webhook callers.

**Rationale:** failure is a certainty, not an edge case. Handling it explicitly
keeps partial outages contained and observable instead of letting one failing
provider take down the gateway or expose internals.

### XI. Bounded Retry Over REST

- When data is propagated between services over REST, a failure MUST be retried
  rather than dropped.
- A retry MUST be attempted only for failures that could plausibly succeed on
  another attempt — connection error, timeout, HTTP 5xx, HTTP 429 — and MUST NOT
  be attempted on a 4xx that reflects a defect in the request.
- A retry MUST only be applied to an idempotent operation or one protected by a
  deduplication key; otherwise the operation MUST be made idempotent.
  `utils.MakeHTTPRequestWithRetry` (attempt cap, exponential backoff with jitter,
  `Idempotency-Key` header) is the default mechanism.
- Every retry policy MUST declare a maximum number of attempts and a backoff
  strategy as named constants; unbounded retry MUST NOT be used.
- When attempts are exhausted, the failure MUST be logged and MUST remain
  recoverable (errored status for Mailroom retry, channel log, or spool) — it
  MUST NOT be silently discarded.

**Rationale:** propagation fails for transient reasons far more often than for
permanent ones, so retry keeps services converging. Retrying rejected or
non-idempotent requests multiplies load or duplicates effects; bounds stop the
mechanism from becoming the outage; recoverable exhaustion prevents data from
vanishing between two services that each believe they succeeded.

### XII. Stateless, Peak-Sized Services

- Courier MUST be stateless so it can scale horizontally: state that outlives a
  single request MUST live in a shared external store (Postgres, Redis/Valkey,
  S3), not in process memory or on local disk.
- In-process caches (e.g. `go-cache`) MAY be used only as non-authoritative,
  TTL-bounded caches whose loss changes latency, never correctness.
- **Documented exception — spool:** the local spool (`COURIER_SPOOL_DIR`,
  `spool.go`) MAY persist messages and statuses to local disk only as a
  write-ahead fallback while the database is unavailable, and MUST be flushed
  back to the database. New features MUST NOT add other local-disk state.
- The peak load a feature is expected to sustain MUST be declared in its
  engineering spec, stated as peak and not as average.

**Rationale:** capacity is a design input, not an incident discovery; sizing for
average traffic fails exactly at seasonal peaks. Statelessness is what makes
adding instances a valid answer to load. The spool exception is accepted because
it is the inherited mechanism that prevents message loss during database
outages; it holds no state shared across requests and drains automatically.

### XIII. Diagnosable Errors

- Every error reported to Sentry MUST carry enough context to be located and
  filtered without reproducing it: at minimum the project/org identifier, the
  account identifier (channel UUID), the user identifier (contact UUID), and the
  correlation identifier (channel-log UUID or message UUID).
- Those identifiers MUST be opaque. Sensitive personal data — names, e-mail
  addresses, phone numbers or other URN paths, government identifiers, message
  bodies — MUST NOT be attached to an error report under any circumstance.

**Rationale:** an error without identifying context can be counted but not
investigated. Opaque identifiers give the filtering an investigation needs while
keeping reports free of personal data, as Principle III requires.

### XIV. Tests Exercise Flows

- Every flow MUST have at least one test covering the complete use case from
  input to resulting effect: inbound webhook → written msg/status/event via
  `RunChannelTestCases`, and outgoing send → provider request and resulting
  status via `RunChannelSendTestCases`.
- Isolated unit tests SHOULD be used for edge cases and input variations, but
  MUST NOT be the only coverage a flow has.
- Every flow MUST cover its success path and its failure paths (invalid payload,
  bad signature, provider 4xx/5xx, timeout). An error path no test exercises MUST
  NOT be considered covered.

**Rationale:** isolated tests can be green while their composition is broken.
Flow tests prove the pieces work together, and failure paths are the least
exercised in development and the most expensive in production.

### XV. Explicit Over Clever

- What code does MUST be evident where it happens; hidden side effects and
  implicit control flow MUST NOT be introduced to save lines.
- Any literal that carries meaning — threshold, limit, timeout, retry count,
  provider API version, payload size cap — MUST be a named constant. Literals
  with no meaning beyond their value (index 0, increment 1) are exempt.
- Comments MUST explain why: the constraint, trade-off, or non-obvious provider
  behaviour. A comment that restates the code signals the code SHOULD be
  rewritten.

**Rationale:** code is read far more often than written, usually without the
context that made the clever version feel obvious. An unexplained literal is a
decision nobody can review; why-comments preserve what code cannot carry
without going stale.

### XVI. Contained Changes

- A change MUST be limited to the context it was asked to address. Refactoring,
  renaming, reformatting, or behaviour adjustments outside that context —
  including in inherited upstream code and in unrelated channel handlers — MUST
  NOT ride along; each belongs to its own change.
- This governs the scope of a change as a whole; Principle VII governs how it is
  split into commits, and an in-scope change MAY span several commits.

**Rationale:** a change beyond its stated scope is a change nobody reviewed on
purpose. It hides the intended fix, makes the diff expensive to read, and turns
a revert into a choice between losing the fix and keeping an unrelated
regression. In a fork, out-of-scope edits to upstream code also make future
upstream merges harder.

## Technology Stack & Constraints

- **Language/runtime:** Go 1.24 (`go.mod`, toolchain `go1.24.x`); module path
  `github.com/nyaruka/courier` is kept for upstream compatibility.
- **HTTP:** `go-chi/chi` router; channel handlers live in `handlers/<channel>/`
  and register through `handlers/base.go`.
- **Persistence/queues:** Postgres via `sqlx` (`backends/rapidpro/`, schema in
  `backends/rapidpro/schema.sql`), Redis/Valkey via `redigo`, RabbitMQ, S3 for
  attachments.
- **Observability:** `logrus` structured logging, Sentry via `logrus_sentry`,
  channel logs persisted per request, Librato metrics.
- **Configuration:** `COURIER_*` environment variables (via `ezconf`); no
  secrets in `courier.toml` or committed scripts.
- **Upstream:** `nyaruka/courier` is tracked as `upstream`; Weni-specific
  behaviour MUST be isolated so upstream merges remain feasible.

## Development Workflow & Quality Gates

- CI (`.github/workflows/ci.yml`) MUST pass: `go test -p=1 ./...` against
  Postgres 12 and 13, Redis, and RabbitMQ. Tests MUST NOT depend on network
  access to real providers; use `httptest` servers and `handlers/testdata`.
- Pull requests MUST state the engineering spec they implement (Principle V)
  and MUST include flow tests for new or changed handlers (Principle XIV).
- Releases are cut by tagging `X.Y.Z` on `main` after updating
  `WENI-CHANGELOG.md` (Principle VIII); pre-release tags use a `-<suffix>`.
- Plans generated with `/speckit.plan` MUST include a Constitution Check against
  every principle above and justify any violation in Complexity Tracking.

## Governance

- This constitution supersedes conflicting practices, READMEs, and local
  conventions in this repository. In conflict, the VTEX CX root engineering
  constitution prevails over the backend constitution, which prevails over
  project adaptations in this file.
- Amendments MUST be made by pull request to `.specify/memory/constitution.md`,
  with an updated Sync Impact Report, and SHOULD be regenerated from the
  canonical bases (`weni-ai/vtex-cx-engineering-constitutions`) when those
  change.
- Versioning follows SemVer: MAJOR for removing or redefining a principle,
  MINOR for adding a principle or section or materially expanding guidance,
  PATCH for clarifications and wording.
- Compliance: every PR review MUST verify adherence; `/speckit.analyze` MUST
  treat any conflict with a `MUST` as CRITICAL. Exceptions MUST be documented in
  this file with rationale (as with the spool exception) — never granted ad hoc.

**Version**: 1.0.0 | **Ratified**: 2026-10-01 | **Last Amended**: 2026-10-01
