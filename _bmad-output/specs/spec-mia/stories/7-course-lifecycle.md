---
title: 'Course lifecycle'
type: 'feature'
created: '2026-09-01'
status: 'done'
baseline_revision: '9b4d6d4f7e2011fc672c73c35616539bdcd08862'
review_loop_iteration: 0
followup_review_recommended: true
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** MIA has no course-owned persistence or API, so administrators cannot create courses and supervisors cannot
prepare or control their lifecycle. Later feature packages also lack the deletion inversion required by AD-14.

**Approach:** Add the `course` vertical slice, including course and supervisor-assignment persistence, preparation,
activation/deactivation, logos, deletion, and a kernel lifecycle registry. Inject owner APIs for material readiness and
active-session checks so later stories complete those gates without reversing the canonical import direction.

## Boundaries & Constraints

**Always:** Enforce FR-36..44, NFR-1..3, NFR-13..14, AD-3, AD-4, AD-6, AD-11, AD-12, and AD-14. Creation and all initial
supervisor assignments are one transaction. New courses are inactive. Every assignee already has the supervisor role,
and every course retains at least one supervisor. Only assigned supervisors prepare, activate, or deactivate; an
administrator needs an assignment for supervisor actions. Activation checks goals, instructions, language, and an
approved ready file-backed material through an injected owner API. Only administrators delete inactive courses with no
active tutoring session. Course deletion invokes every registered course deleter in the deleting transaction. Logos use
the shared normalized-image and atomic-publication contract.

**Block If:** Implementation requires an authorization boundary, lifecycle ordering, or cross-feature readiness result
not fixed by the PRD and AD-14.

**Never:** Let administrator status imply supervisor scope; expose inactive courses to students; query a future feature's
tables from `course`; activate before story 9 supplies qualifying material; delete an active course or coordinate with
workers/providers; add student membership behavior assigned to story 8.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Creation | Administrator plus valid supervisor IDs | Inactive course and assignments commit together | Duplicate name or any invalid assignee rolls everything back |
| Lifecycle | Assigned supervisor and prepared course | Activation checks all gates; deactivation blocks new work | Missing material keeps activation unavailable until story 9 |
| Assignment | Administrator adds/removes supervisors | Assignment changes atomically and course keeps one | Last-supervisor removal is rejected |
| Deletion | Administrator, inactive course, no active session | Registered deleters and course deletion share one transaction | Active course/session rejects without deletion |
| Logo | Administrator or assigned supervisor, bounded image | Only normalized PNG is stored and safely served | Invalid, unauthorized, or missing logo fails safely |

</intent-contract>

## Code Map

- `internal/course/` -- new vertical slice owning course records, supervisor assignments, scoped fetches, lifecycle, and
  logo HTTP operations.
- `internal/lifecycle/` -- new AD-14 registry for transaction-aware course/account deletion callbacks.
- `migrations/000009_courses.up.sql` -- course and supervisor-assignment tables with normalized uniqueness and FKs.
- `internal/identity/identity.go` -- reuse `NameKey`, text, and BCP-47 validation.
- `internal/imagefile/`, `internal/filepublish/` -- reuse story 6's bounded normalizer and reversible atomic replacement.
- `internal/httpserver/errors.go`, `internal/audit/audit.go` -- extend central registries with stable course outcomes/actions.
- `cmd/mia/main.go` -- construct the lifecycle registry and wire the course service and authenticated routes.
- `docs/api.md`, `docs/database-layout.md`, `docs/data-dir.md` -- align concrete route fields and course-logo storage.

## Tasks & Acceptance

**Execution:**
- `migrations/000009_courses.up.sql`, `internal/course/*` -- implement course persistence, scoped HTTP APIs, preparation,
  supervisor assignment, lifecycle gates, deletion, logos, audits, and integration tests.
- `internal/lifecycle/*` -- implement deterministic transaction-aware registration/invocation with tests.
- `internal/httpserver/errors.go`, `internal/audit/audit.go`, `cmd/mia/main.go` -- register and wire the slice.
- `docs/api.md`, `docs/database-layout.md`, `docs/data-dir.md` -- document concrete fields and lifecycle integration.

**Acceptance Criteria:**
- Given an administrator and existing supervisors, when a valid course is created, then it is inactive and every initial
  assignment exists in the same committed transaction; any invalid assignee leaves neither record nor assignments.
- Given an assigned supervisor, when activation is requested, then all FR-39 gates are checked and no activation can
  succeed until the material owner reports approved, ready, file-backed material.
- Given an administrator, when an inactive course without active sessions is deleted, then all lifecycle callbacks and
  the course deletion commit atomically while accounts survive.

## Spec Change Log

## Review Triage Log

### 2026-09-01 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 4: (high 2, medium 2, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[high]` `[patch]` Replaced direct reads of user-owned role tables with the exported user owner API.
  - `[high]` `[patch]` Added bounded course-list pagination and closed the list rows before relationship queries.
  - `[medium]` `[patch]` Kept active courses from losing required non-material activation fields.
  - `[medium]` `[patch]` Added content-free audits for denied course mutations.

## Design Notes

Material readiness and active-session checks are injected transaction-aware functions. Their safe defaults report no
qualifying material and no active session respectively: the former intentionally blocks activation before story 9, while
the latter is valid before tutoring-session persistence exists. Later owners are wired in `cmd/mia` without `course`
importing them.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: all changed Go files are formatted.
- `go test ./...` -- expected: all tests pass without external providers.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.

## Auto Run Result

Status: done

Implemented the course vertical slice with normalized globally unique course names, transactional initial supervisor
assignment, scoped preparation and visibility, activation/deactivation gates, supervisor assignment invariants, bounded
logos, deletion gates, content-free auditing, and the AD-14 lifecycle registry. Material readiness defaults to false, so
activation remains blocked until story 9 wires the material owner's readiness function.

Changed files include the course and lifecycle packages and tests, migration 000009, command wiring, central audit and
error registries, SQLite migration-state test, API/database documentation, and this story record.

Review findings: 4 patches applied (2 high, 2 medium), 0 deferred, 0 rejected. Follow-up score is 6
(`3 × 2 medium + 0 low`); follow-up review is recommended because the pass contained high-severity patches.

Verification passed: `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, and `go test -race ./...`.

Residual dependency: activation intentionally cannot succeed until story 9 supplies approved, ready, file-backed
material readiness; active-session deletion checking is injected for story 10 before tutoring-session rows exist.
