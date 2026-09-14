---
title: 'Discover and Resume the Active Tutoring Session'
type: 'feature'
created: '2026-09-14'
status: 'done'
review_loop_iteration: 0
followup_review_recommended: true
baseline_revision: 'eedefacd24a7b81bf1dbb496f190750993425cc0'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Course-scoped lists and ID-addressed reads cannot locate the student's globally unique active tutoring
session after reload, device change, creation conflict, or an ambiguous creation response.

**Approach:** Add one authenticated, non-refreshing current-account discovery read backed by a single coherent database
snapshot. Return either the owned active session with minimal course navigation context or an explicit JSON:API null.

## Boundaries & Constraints

**Always:** Authorize the caller as a student and scope the query to session ownership. Read across all courses without
pagination, preserve the database one-active-session invariant, and return a session ID plus its course ID and name.
Return deactivated-course sessions while they remain active. Keep normal absence distinct from authentication,
authorization, and dependency failures. Apply shared negotiation, unchanged session deadlines, and `no-store` policy.

**Block If:** Implementation requires a new session lifecycle, course visibility rule, or different creation-conflict
semantics not fixed by FR-54..56 and Story 2.8.

**Never:** Enumerate courses, trust a retained client session ID, expose another student's session or course context,
refresh browser idle expiry, cache discovery, replay session creation, or treat course deactivation as session completion.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Active | Student owns one active session | One minimal session resource with course ID and name | No error expected |
| None | Student owns no active session | `200` JSON:API document with `data: null` | Absence is not an error |
| Deactivated | Active session's course is inactive | Same active session remains discoverable | No replacement is inferred |
| Lost scope | Caller is authenticated without student role | No session or course data | Stable `403` |
| Dependency failure | Atomic lookup cannot complete | No partial or stale result | Safe central `500` |

</intent-contract>

## Code Map

- `internal/tutoring/{types,service,store}.go` -- tutoring owns the active-session row and global partial unique index;
  add a minimal course-context projection and transaction-snapshot discovery API without loading transcript or material.
- `internal/tutoring/handler.go` -- register the current-account GET and encode a nullable concrete JSON:API document.
- `internal/tutoring/{service,handler}_test.go` -- existing two-course fixture covers ownership, deactivation, completion,
  conflict reconciliation, no-session null, authorization, and non-refreshing response behavior.
- `internal/httpserver/errors.go` -- add a stable tutoring authorization error mapped only by the central registry.
- `api-doc/{openapi.yaml,paths/tutoring.yaml,schemas/resources/resources.yaml}` -- define the path, concrete nullable
  response, security, errors, no-store, and non-refreshing reconciliation semantics.
- `docs/{api,tutoring-sessions,product-requirements}.md` -- document authoritative cross-course discovery and safe
  reconciliation without duplicating field-level transport schemas.

## Tasks & Acceptance

**Execution:**

- [x] `internal/tutoring/{types,service,store,handler}.go` -- implement student-only atomic active-session discovery and
  nullable HTTP representation.
- [x] `internal/httpserver/errors.go`, `internal/tutoring/*_test.go` -- map and prove authorization, absence, concurrency,
  deactivation, completion, conflict, protocol, deadline, and disclosure behavior.
- [x] `api-doc/**`, `docs/{api,tutoring-sessions,product-requirements}.md` -- publish the browser contract and
  reconciliation guidance.

**Acceptance Criteria:**

- [x] Given an authenticated student owns one active session in any course, when discovery runs, then one atomic response
  returns its session ID and authorized course ID/name regardless of browser course context.
- [x] Given no owned active session, when discovery runs, then it returns `200` with `data: null`, distinct from `401`,
  `403`, and safe dependency failure.
- [x] Given an active session's course is deactivated, when discovery runs, then the same session remains resumable and no
  replacement is offered.
- [x] Given conflict or ambiguous creation, when discovery runs before another create, then it returns the committed
  active session or authoritative null without course enumeration or replay.
- [x] Given concurrent create, complete, delete, and discovery operations, when transactions race, then each read exposes
  zero or one committed owned active session and never another student's context.
- [x] Given integration and OpenAPI checks, when the route is exercised after sign-in expiry, scope loss, reload, device
  change, deactivation, completion, and conflict, then response shape, errors, no-store, and unchanged authentication
  deadlines match the documented contract.

## Spec Change Log

## Review Triage Log

### 2026-09-14 — Product-owner resolution fix

- patch: 1 (high 1, medium 0, low 0)
- addressed_findings:
  - `[high]` Active-session discovery database lookup failures now use the I/O matrix's shared safe HTTP `500`
    `internal_error`; OpenAPI, human-readable contracts, and an HTTP regression test enforce that mapping.

### 2026-09-14 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 0
- defer: 0
- reject: 0
- addressed_findings:
  - none

## Design Notes

The discovery resource remains a `tutoring-sessions` resource but uses a purpose-specific sparse response schema. It
contains only active-session identity, state, start/activity instants, and course identity/name. Existing ID-addressed
retrieval remains authoritative for the full session representation.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: no formatting differences.
- `./run-all-tests.sh` -- expected: formatting, Go test/vet/lint/race, duplication, vulnerability, OpenAPI, and Markdown
  gates pass; unavailable optional tools are reported by the script.

## Auto Run Result

Status: done

Implemented `GET /api/v1/users/me/active-tutoring-session` as a student-authorized, non-refreshing cross-course read.
The transaction-snapshot service returns either one minimal owned session with course identity and name or explicit
`data: null`; deactivated courses remain discoverable, while role loss and dependency failure have distinct safe errors.
Database lookup failures map to the matrix-required shared safe HTTP `500 internal_error`.

Updated the central error registry, tutoring service and handler tests, OpenAPI path and concrete nullable schema, and
the API, product, and tutoring-session narratives. Tests cover absence, active and deactivated sessions, conflict
reconciliation, other-student isolation, role loss, HTTP-level database dependency failure, and concurrent
start/completion reads.

Review findings: one high-severity mapping patch, with no deferred or rejected findings. Follow-up review recommendation:
true (1 high, 0 medium, 0 low patches).

Verification: targeted tutoring and HTTP tests passed after correcting the non-refreshing cookie assertion. Pinned
Redocly lint passed after correcting YAML scalar quoting. The final `./run-all-tests.sh` passed formatting, Go tests,
vet, golangci-lint, race tests, duplication marker/clone checks, Trivy, Redocly, and Markdown lint.

Residual risks: none identified.
