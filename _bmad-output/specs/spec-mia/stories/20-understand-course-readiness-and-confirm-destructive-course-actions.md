---
title: 'Understand Course Readiness and Confirm Destructive Course Actions'
type: 'feature'
created: '2026-09-14'
status: 'done'
review_loop_iteration: 0
followup_review_recommended: false
baseline_revision: '1cbb4c6e8762afb20fdac9e07db122d7c4b71459'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** Course responses do not explain authoritative readiness, students cannot safely read joined courses, and
course deletion, supervisor removal, and membership removal can apply after their reviewed consequences have changed.

**Approach:** Project course readiness from current owner data in one transaction, disclose blockers and actions according
to viewer scope, add reviewed assignment and membership detail, and require strong `If-Match` preconditions on all three
destructive actions.

## Boundaries & Constraints

**Always:** Recompute readiness from course fields, supervisor assignments, material readiness, and current session state;
keep activation and session-start eligibility independent from lifecycle state. Students see joined active courses and only
safe readiness/action fields; assigned supervisors receive stable preparation blockers and links. Strong validators cover
the actor's authority, blockers, and all cascade conditions relevant to the exact action. Destructive transactions recheck
authorization, guards, and validator before cleanup and audit. Missing/out-of-scope resources remain indistinguishable.

**Block If:** Implementation requires a new course lifecycle, prerequisite, authorization boundary, cascade effect, or
viewer-visible preparation detail not fixed by FR-37..43, FR-54, and Story 2.7.

**Never:** Persist readiness counters, expose material identities or configuration details to students, treat an ETag as
authorization, mutate without a current strong precondition, replay destructive operations, coordinate deletion with
workers/providers, or prevent existing sessions from resuming after readiness loss.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Course read | Authorized administrator, supervisor, or joined student | Current readiness and scoped actions from one snapshot | Hidden course is one safe `404` |
| Readiness change | Course field, assignment, material, lifecycle, or session changes | Next read and mutation representation recompute source state | Dependency failure returns safe unavailable error |
| Reviewed removal | Current assignment/membership/course ETag | Exact removal and documented cleanup commit | Guard is rechecked in the same transaction |
| Stale removal | Missing/malformed/stale ETag or changed blocker | No state or data changes | Stable `428` or `412`; hidden access remains `404` |

</intent-contract>

## Code Map

- `internal/course/course.go` -- course table owner; add coherent readiness/action projections, scoped student visibility,
  reviewed detail snapshots, effect-complete validators, and in-transaction precondition checks.
- `internal/course/handler.go` -- expose scoped course readiness and assignment/membership detail, set strong ETags, require
  `If-Match` on destructive routes, and return current equivalent representations from lifecycle mutations.
- `internal/material/material.go`, `internal/tutoring/service.go`, `cmd/mia/main.go` -- retain existing transaction-aware
  owner callbacks for qualifying material and active-session conditions; no foreign-table SQL in course.
- `internal/httpserver/errors.go` -- register safe course precondition and readiness-unavailable outcomes.
- `internal/course/{course,handler}_test.go` -- cover all readiness transitions, disclosure scopes, current/stale/missing
  preconditions, concurrent guard changes, exact cleanup, and existing-session preservation.
- `api-doc/openapi.yaml`, `api-doc/paths/courses.yaml`, `api-doc/schemas/resources/resources.yaml` -- document readiness
  states, blockers, links, action eligibility, detail reads, ETag/If-Match, stable errors, and no-store responses.
- `docs/api.md`, `docs/product-requirements.md` -- explain authoritative readiness and reviewed destructive-action
  reconciliation without duplicating field schemas.

## Tasks & Acceptance

**Execution:**

- [x] `internal/course/course.go` -- compute viewer-safe readiness and exact-action snapshots from source data in coherent
  transactions; bind destructive mutations to those snapshots.
- [x] `internal/course/handler.go`, `internal/httpserver/errors.go` -- serve readiness/detail representations and map
  required/failed preconditions through the central registry.
- [x] `internal/course/*_test.go` -- prove transitions, authorization disclosure, stale no-op behavior, concurrency guards,
  and unchanged active-session resumability.
- [x] `api-doc/**`, `docs/{api,product-requirements}.md` -- publish the complete browser contract.

**Acceptance Criteria:**

- [x] Given an authorized course viewer, when the course is read, then one committed snapshot distinguishes
  inactive-incomplete, inactive-activatable, active-accepting, and active-not-accepting and reports activation and
  session-start eligibility independently.
- [x] Given an assigned supervisor, when prerequisites are missing, then stable blockers for goals, instructions,
  language, supervisor presence, and qualifying material include only authorized preparation links; other viewers receive
  no out-of-scope details.
- [x] Given material readiness is lost or restored, when an active course is reread, then new-session eligibility changes
  without lifecycle transition and existing sessions remain untouched.
- [x] Given a course, supervisor assignment, or membership is reviewed, when detail is returned, then server-authored
  eligibility, viewer-safe consequences, and a strong effect-complete ETag describe that exact destructive action.
- [x] Given current `If-Match`, when an authorized destructive course action executes, then authorization, state, guards,
  precondition, cleanup, account preservation, triage, and audit effects are checked and committed atomically.
- [x] Given a missing/stale precondition, changed blocker, or lost access, when deletion/removal is attempted, then no
  mutation occurs and the stable precondition or existence-safe result is returned without automatic replay.
- [x] Given OpenAPI and integration tests, when every readiness and destructive race is exercised, then all state,
  blockers, links, eligibility, ETag/If-Match, error, no-store, and viewer-disclosure contracts are proven.

## Spec Change Log

## Review Triage Log

### 2026-09-14 — Reviewer-finding fix pass

- patch: 5 (high 2, medium 3, low 0)
- addressed_findings:
  - `[high]` Course deletion now hashes the canonical viewer-visible review and deterministic deletion-impact facts from
    every registered course-data owner plus course-owned membership, audit, and logo state.
  - `[high]` Membership removal now hashes deterministic owner-reported student-course deletion impact, including private
    material, jobs, tutoring, speech, mentoring, and audit facts.
  - `[medium]` Membership review now uses a dedicated OpenAPI response schema that requires the `remove` action.
  - `[medium]` HTTP route tests now cover review response headers and shape, authorization scope, missing and stale
    preconditions, and no-op side effects for all three destructive route families.
  - `[medium]` OpenAPI now defines action-specific stable blocker and consequence vocabularies, and the API narrative
    explains that validators cover relevant non-disclosed deterministic cascade-impact facts without exposing identities.

## Design Notes

Readiness is a projection, not stored metadata. The course response may contain complete preparation fields only for an
assigned supervisor or administrator already entitled to the existing course detail; joined students receive the safe
course shape and readiness eligibility without supervisor relationships or preparation blockers. Exact-action detail
validators hash canonical source facts, including hidden IDs where needed to detect changed cascade impact, but responses
publish only authorized consequences.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: no formatting differences.
- `./run-all-tests.sh` -- expected: Go test, vet, lint, race, duplication, vulnerability, OpenAPI, and Markdown gates pass;
  optional unavailable tools are reported by the script.

## Auto Run Result

Status: done

Implemented transaction-snapshot course readiness with four explicit states, independent activation and new-session
eligibility, supervisor-only preparation blockers and links, joined-student safe course views, and the cross-course active
session gate. Added reviewed supervisor-assignment and membership detail resources and strong ETags for all three
destructive course actions; missing or changed preconditions are no-ops.

Updated the central error registry, command wiring, OpenAPI schemas and operations, API rationale, and product requirements.
Tests cover readiness transitions, student disclosure, dependency unavailability, missing preconditions, and stale
supervisor, membership, and course operations.

Verification: targeted course tests, `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, pinned Redocly lint, and
the final `./run-all-tests.sh` passed. The full gate passed formatting, Go tests, vet, lint, race, duplication-marker and
clone checks, Trivy, Redocly, and Markdown lint. `govulncheck` was not installed and was skipped by the script.

Fix verification: targeted owner and course tests passed. The final `./run-all-tests.sh` passed all available gates after
the deletion-impact registry, canonical ETag coverage, dedicated schema, and route-test fixes.
