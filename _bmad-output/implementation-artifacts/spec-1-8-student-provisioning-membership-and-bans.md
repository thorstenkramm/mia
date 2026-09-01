---
title: 'Student provisioning, membership, and bans'
type: 'feature'
created: '2026-09-01'
status: 'done'
baseline_commit: '1628a359a45c57f27bc2db304be193a05033be52'
review_loop_iteration: 1
followup_review_recommended: false
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** Assigned supervisors cannot provision student accounts, manage active-course student membership, recover
student passwords, or ban student-only accounts, leaving CAP-3, CAP-13, and FR-19 unavailable.

**Approach:** Add supervisor-scoped membership operations through `course`, account-security mutations through `user`,
and the required transactional audit, lifecycle, HTTP, and migration support.

## Boundaries & Constraints

**Always:** Provisioning requires an assigned supervisor, an active course, complete required account fields, and a
policy-compliant temporary password. Existing enrollment uses complete username only, never grants a role, and hides
unknown and non-student accounts identically. Temporary-password recovery, ban, and unban require one shared assigned
course and a student-only target. Security generation changes only for temporary-password setting, not ban changes.
Every mutation and its content-free audit record is atomic. Removal preserves the account and other-course data while
deleting the membership and all registered student-course data, and is rejected for an active tutoring session.

**Block If:** Implementation requires choosing a default language, country, or time zone for provisioned students;
requires a new authorization principal; or cannot preserve future feature-owned student-course deletion through the
lifecycle registry.

**Never:** Add public registration, student acceptance, global search/autocomplete, implicit role grants during
existing enrollment, staff bans, password values in logs/audit/responses, or security-generation changes for bans.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Provision | Assigned supervisor, active course, valid identity fields and temporary password | Student-only account and membership are created atomically with password-change required | Duplicate username conflicts; invalid input is rejected |
| Add existing | Complete username of student-role account | Membership is created, or existing membership is returned unchanged | Unknown and non-student targets share one safe not-found response |
| Remove | Assigned supervisor and member without active course session | Registered course data and membership are deleted; account survives | Active session conflicts; out-of-scope and unknown are indistinguishable |
| Recover | Supervisor shares a course with student-only target | Password hash changes, password gate is set, security generation increments, MFA challenges/proofs are invalidated | Staff and out-of-scope targets share safe not-found |
| Ban state | Supervisor shares a course with student-only target | Ban/unban applies on next login/request without incrementing security generation | Staff and out-of-scope targets share safe not-found |

</intent-contract>

## Code Map

- `internal/course/course.go` -- owns membership table access, supervisor-scoped fetches, active-course gates, removal
  transaction, and cross-owner calls into `user`.
- `internal/course/handler.go` -- existing authenticated JSON:API course route family; add students and user-security
  endpoints using its strict decoder and central error mapping.
- `internal/user/user.go` -- sole owner of account creation, password/security generation, and ban columns; extend its
  exported transaction-aware API rather than issuing user SQL from `course`.
- `internal/auth/auth.go` and `internal/auth/mfa.go` -- auth owns MFA challenges/proofs; expose the existing artifact
  invalidation operation for password and ban state changes.
- `internal/lifecycle/lifecycle.go` -- add the student-within-course deletion inversion needed by FR-42 and future
  feature packages.
- `internal/audit/audit.go` -- register content-free actions and replace removed student's course audit history.
- `internal/httpserver/errors.go` -- central stable mappings for membership/security operation outcomes.
- `migrations/000011_course_students.up.sql` -- create the composite-key membership table and indexes.
- `cmd/mia/main.go` -- wire course service security callbacks; no feature SQL in command wiring.
- `internal/course/course_test.go`, `internal/course/handler_test.go` -- real-migration tests for atomicity, scope,
  existence hiding, idempotence, deletion, cookie invalidation, and ban behavior.
- `docs/api.md`, `docs/database-layout.md` -- update implemented route/schema contracts without changing product rules.

## Tasks & Acceptance

**Execution:**

- [x] `migrations/000011_course_students.up.sql` -- add constrained course membership persistence.
- [x] `internal/lifecycle/lifecycle.go` -- register and invoke student-course deleters transactionally.
- [x] `internal/user/user.go` -- support temporary-gated creation, temporary-password mutation, and student-only ban state.
- [x] `internal/auth/auth.go` -- export transaction-aware MFA challenge/proof invalidation.
- [x] `internal/course/course.go` -- implement list, provision, add, remove, recover, ban, and unban domain operations.
- [x] `internal/course/handler.go`, `internal/httpserver/errors.go` -- expose strict authenticated JSON:API routes and
  central errors.
- [x] `internal/audit/audit.go` -- add atomic content-free security and membership actions.
- [x] `cmd/mia/main.go` -- wire dependencies.
- [x] `internal/course/*_test.go` -- cover the matrix and authorization boundaries through services and Echo.
- [x] `docs/api.md`, `docs/database-layout.md` -- document concrete implemented contracts.

**Acceptance Criteria:**

- Given an assigned supervisor and active course, when valid student provisioning is submitted, then one student-only
  account and membership are committed and first login is restricted to password replacement.
- Given an existing student username, when membership is added repeatedly, then the same membership is returned without
  changing roles, profile, password, or restoring deleted course data.
- Given an unknown, staff-only, or out-of-scope target, when a supervisor attempts a target-sensitive operation, then
  the endpoint returns the same safe not-found shape.
- Given a shared student-only account with existing cookies, when a temporary password is set, then all old cookies fail
  on their next request and a fresh login requires password replacement.
- Given a shared student-only account, when it is banned or unbanned, then database-reloaded ban state takes effect on
  the next login/request while security generation remains unchanged.
- Given a member with no active course session, when an assigned supervisor removes them, then all registered
  student-course data is deleted atomically while the account and other memberships remain.

## Spec Change Log

## Review Triage Log

### 2026-09-01 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 1: (high 0, medium 1, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[medium]` `[patch]` Made concurrent existing-student enrollment converge on the durable membership instead of
    surfacing a uniqueness failure.

## Design Notes

`course` initiates cross-feature transactions because course assignment is the authorization scope. It calls exported
`user` and `auth` owner APIs with the shared querier and invokes lifecycle student-course deleters before deleting the
membership. This preserves AD-3 and the AD-14 import direction.

## Verification

**Commands:**

- `gofmt` on changed Go files -- expected: no formatting diff.
- `go test ./...` -- expected: all packages pass.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.

## Auto Run Result

Status: done

Summary: Implemented assigned-supervisor student provisioning and membership management, temporary-password recovery
with cookie invalidation, and student-only ban/unban with per-request enforcement. Added transactional lifecycle and
content-free audit support plus documented JSON:API contracts.

Files changed: migration 000011; course, user, auth, audit, lifecycle, HTTP error, and command wiring Go files; course
service/handler tests; SQLite migration-version test; API and database-layout documentation; this story spec.

Review findings breakdown: one medium patch applied for concurrent enrollment idempotence; no deferred or rejected
findings. Follow-up review recommendation: false (high 0, medium 1, low 0; score 3).

Verification performed: `gofmt` completed; `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, and
`go test -race ./...` passed. Markdown lint passed for both changed documentation files.

Residual risks: tutoring-owned active-session and downstream student-course deleters are intentionally injected through
the new owner callbacks/registry and must be registered when those feature slices arrive.
