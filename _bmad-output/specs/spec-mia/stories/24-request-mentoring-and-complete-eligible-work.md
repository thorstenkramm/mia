---
title: 'Request Mentoring and Complete Eligible Work'
type: 'feature'
created: '2026-09-15'
status: 'in-review'
review_loop_iteration: 0
followup_review_recommended: false
baseline_revision: '8f946eb76d6828d9172e13324c529ee16e3f569a'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** Mentoring creation and completion are currently exposed only as mutations. Clients must infer whether an
action is available, and mentor-removal DELETE operations do not bind confirmation to reviewed consequences.

**Approach:** Add authoritative request and completion eligibility projections, make mentoring-session reads carry
viewer-specific completion eligibility, and add strong preconditioned mentor-removal reviews and mutations. Recheck all
mutable facts atomically when creating, completing, or removing.

## Boundaries & Constraints

**Always:** Evaluate request eligibility from current active course membership, `mentoring_requests_allowed`, and at least
one student mentor assignment in one database snapshot. Return only allowed, neutrally disabled, access-lost, or
unavailable, without exposing the setting or mentor identity. Evaluate completion from current visibility, account and
assignment scope, open/scheduled lifecycle, scheduled instant, and server time; only the current explicitly assigned
mentor may receive allowed. Return a recheck instant only when schedule time is the sole blocker. Course deactivation
blocks creation but never existing-work handling. Completion and creation recheck in the mutating transaction. Review
mentor removal with a strong ETag derived from assignment and affected open-work facts; require matching `If-Match` and
recheck authorization/state in the removal transaction. Removal clears mentor, proposed/scheduled times, and meeting
details on open work while preserving topic and prior response/authorship. Use shared JSON:API, strict UTC instants,
existence hiding, central errors, CSRF, and `no-store`.

**Block If:** Implementation requires disclosing a hidden setting or mentor identity to students, changing existing-work
authorization, changing preserved removal history, allowing completion by another actor, or inventing client-clock or
automatic-replay authority.

**Never:** Treat checking or a prior eligibility response as durable authority; infer authorization from elapsed browser
time; let course deactivation block existing work; automatically retry an ambiguous creation/completion; merge direct
reassignment with removal; accept weak, wildcard, missing, or stale removal preconditions; or mutate on a failed review
precondition.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Request check | Owning joined student and current gates | Allowed or neutral disabled projection | Lost scope is hidden; dependency failure is unavailable |
| Request race | Allowed check followed by gate change | Transaction creates once or returns current race denial | No automatic replay |
| Completion check | Current assigned mentor and scheduled open work | Allowed at/after schedule; otherwise not allowed with optional recheck instant | Lost scope, terminal, and unavailable are explicit |
| Completion race | Assignment/lifecycle/time changes before mutation | Transaction commits once only if still eligible | Stale or concurrent denial reconciles through GET |
| Removal review | Assigned supervisor and existing assignment | Consequences plus strong ETag cover assignment/open work | Out-of-scope existence hidden |
| Removal mutation | Matching reviewed ETag | Assignment deletion and triage commit atomically | Missing/stale precondition performs no mutation |

</intent-contract>

## Code Map

- `internal/mentoring/types.go` -- add request/completion eligibility and removal-review domain projections and
  precondition sentinel errors.
- `internal/mentoring/service.go` -- `Available`, `Create`, `Get`, `Update`, `RemoveCourseMentor`, and
  `RemoveStudentMentor` hold the current gates; add snapshot eligibility reads, viewer-specific completion evaluation,
  strong removal review tokens, and transactional compare-before-remove behavior.
- `internal/mentoring/handler.go` -- register eligibility/review routes, include completion eligibility in session
  resources, split explicit completion from general PATCH if needed for fresh confirmation, and enforce `If-Match`.
- `internal/mentoring/{service,handler}_test.go` -- existing real-migration fixtures cover request gates, schedule
  boundaries, scope loss, reassignment, removal triage, and HTTP protocol; extend them across every state and race.
- `internal/httpserver/errors.go` -- register mentoring precondition and unavailable mappings in the single registry.
- `api-doc/openapi.yaml`, `api-doc/paths/mentoring.yaml`, `api-doc/schemas/resources/resources.yaml` and
  `api-doc/schemas/requests/requests.yaml` -- define concrete routes, states, timestamps, ETag/If-Match, errors,
  reconciliation, CSRF, and no-store contracts.
- `docs/api.md`, `docs/product-requirements.md`, `docs/database-layout.md` -- document authoritative eligibility and
  reviewed removal semantics without duplicating field-level OpenAPI details.

## Tasks & Acceptance

**Execution:**

- [x] `internal/mentoring/{types,service}.go` -- implement snapshot request eligibility, viewer-specific completion
  eligibility, atomic completion recheck, and deterministic reviewed-removal preconditions.
- [x] `internal/mentoring/handler.go`, `internal/httpserver/errors.go` -- expose authenticated JSON:API reads/actions,
  central errors, CSRF, no-store, equivalent success representation, and precondition enforcement.
- [x] `internal/mentoring/{service,handler}_test.go` -- prove gates, schedule boundary/server clock, role and assignment
  scope, terminal/access-loss states, concurrent stale no-ops, course deactivation, and removal review invalidation.
- [x] `api-doc/**`, `docs/{api,product-requirements,database-layout}.md` -- publish the complete transport and lifecycle
  contract, including reconciliation and privacy-safe state meanings.

**Acceptance Criteria:**

- [x] Given a joined student checks request eligibility, when current course, permission, and assignment facts are read,
  then the response is allowed, disabled, access-lost, or unavailable and reveals no hidden setting or mentor identity.
- [x] Given eligibility changed after a read, when creation is submitted, then all gates are atomically rechecked and
  either exactly one unassigned request is created or the documented race response is returned without replay.
- [x] Given an authorized viewer reads a mentoring session, when completion is evaluated, then allowed is returned only
  to its current assigned mentor at or after schedule and an early mentor gets the authoritative UTC recheck instant.
- [x] Given a fresh allowed check, when the mentor confirms completion, then assignment, lifecycle, schedule,
  authorization, and server time are rechecked atomically and definitive success returns equivalent current state.
- [x] Given stale, concurrent, ambiguous, terminal, or access-lost completion, when the session is reconciled, then its
  current representation is authoritative and no success or automatic replay is inferred.
- [x] Given an assigned supervisor reviews mentor removal, when the assignment or affected open work changes, then the
  strong ETag changes; DELETE requires the exact fresh value and otherwise performs no mutation.
- [x] Given a matching reviewed removal, when it commits, then assignment removal, open-work triage, field clearing,
  history preservation, and audits are atomic while direct reassignment semantics remain unchanged.
- [x] Given HTTP/OpenAPI and concurrency tests, when all states and actions are exercised, then strict instants,
  existence hiding, preconditions, CSRF, stable errors, reconciliation shapes, and `no-store` match the contract.

## Spec Change Log

## Review Triage Log

### 2026-09-15 — Review fixes

- patch: 2 (high 0, medium 2, low 0)
- addressed_findings:
  - `[medium]` `[patch]` Added both removal-route handler matrices for missing, stale, weak, wildcard, and multi-value
    preconditions, with database assertions proving assignments and open work remain unchanged.
  - `[medium]` `[patch]` Limited successful OpenAPI eligibility enums to states handlers can return in resource documents
    and documented access loss and dependency unavailability as error outcomes where applicable.

### 2026-09-15 — Round-two review fixes

- patch: 2 (high 0, medium 2, low 0)
- addressed_findings:
  - `[medium]` `[patch]` Wrapped completion-projection failures from mentoring-session lists in
    `ErrStateUnavailable` and added list-route coverage proving the stable `503 mentoring_state_unavailable` response.
  - `[medium]` `[patch]` Documented concrete `503` outcomes for mentoring-session list, GET, POST, and PATCH, including
    safe-read retry and ambiguous-mutation reconciliation without replay.

### 2026-09-15 — Round-three review fixes

- patch: 2 (high 0, medium 2, low 0)
- addressed_findings:
  - `[medium]` `[patch]` Added missing- and invalid-CSRF coverage for both preconditioned mentor-removal routes, proving
    `403 csrf_invalid` and unchanged assignments, open work, and audit rows with a current `If-Match` value.
  - `[medium]` `[patch]` Documented concrete DELETE `403` outcomes for CSRF rejection and restricted login stages on both
    course-mentor and student-mentor removal operations.

## Design Notes

Eligibility is a read-time domain projection, not persisted workflow state. A strong removal validator hashes a canonical
snapshot containing assignment identity and all open-work fields whose clearing/preservation consequences are reviewed;
the raw snapshot is never exposed. The mutation recomputes that validator inside its transaction before deleting.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: no formatting differences.
- `./run-all-tests.sh` -- expected: formatting, Go test/vet/lint/race, duplication, vulnerability, OpenAPI, and Markdown
  gates pass; unavailable optional tooling is reported by the script.

## Auto Run Result

Status: implemented

Added atomic student request-eligibility reads with allowed, neutral disabled, access-lost, and dependency-unavailable
outcomes. Mentoring-session reads now include viewer-specific completion eligibility, and a focused fresh-check endpoint
returns server-time schedule guidance only to the current assigned mentor. Creation and completion retain transactional
rechecks; course deactivation does not block completion, and concurrent completion commits exactly once.

Added course- and student-mentor removal review reads. Their strong ETags cover current assignment and affected open-work
facts, and DELETE now requires the exact fresh `If-Match` value before atomically applying existing triage, clearing,
history-preservation, and audit behavior. Missing or stale preconditions leave all rows unchanged.

Updated central errors, authenticated routes, OpenAPI resource and operation contracts, product/API/database documents,
and service/HTTP tests. The supervisor-owned sprint status file was preserved without modification by this run.

Verification passed: targeted mentoring and HTTP tests, project-wide vet and lint, the full test and race suites,
duplication checking with zero clones, Trivy with no findings, Redocly OpenAPI lint, and Markdown lint. No independent
review agents were run because the supervisor requested direct execution without nested agents. Residual risks: none
identified.

Review fixes additionally reject every non-exact `If-Match` form before service comparison and prove the course-mentor and
student-mentor DELETE routes leave assignment and open-work rows unchanged for every rejected precondition. OpenAPI now
advertises only `allowed`, `disabled`, and `access-lost` for successful request-eligibility resources, and only `allowed`,
`not-allowed`, and `terminal` for successful completion-eligibility resources; completion access loss remains `404` and
dependency unavailability remains `503`. The final full project suite passed after these fixes.

Round-two fixes align list projection failures with detail projection behavior and make every mentoring-session route's
unavailable and reconciliation contract explicit. The final full project suite passed after these changes.

Round-three fixes prove CSRF rejection precedes both precondition handling and domain auditing for each removal scope, and
the OpenAPI DELETE contracts now name both `csrf_invalid` and `auth_password_change_required`. The final full project
suite passed after these changes.
