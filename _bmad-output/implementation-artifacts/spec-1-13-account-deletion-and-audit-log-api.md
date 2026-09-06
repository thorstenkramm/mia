---
title: 'Account deletion and audit log API'
type: 'feature'
created: '2026-09-06'
status: 'done'
baseline_revision: 'eecab0a86ae8af89e7286dd3c76bf23a0ddca27e'
review_loop_iteration: 0
followup_review_recommended: false
context:
  - 'AGENTS.md'
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** CAP-14 account deletion and the CAP-15 administrator audit API are the remaining lifecycle and
oversight surfaces. Account deletion must not leave operational data, break course supervision, or leave direct
identity references in retained audit history.

**Approach:** Add an administrator-only user deletion operation owned by `user`, invoke every account lifecycle
owner in one transaction, and retain only random-fingerprint references in audit history. Add a paginated,
administrator-only audit-event collection with a concrete JSON:API/OpenAPI contract.

## Boundaries & Constraints

**Always:** Enforce student-only deletion by any administrator and staff deletion only by a different
administrator. Reject the last administrator and any supervisor who is the sole supervisor of a course. Triage
open mentoring work, remove assignments and student-owned data, clear historical actor references, and write the
de-identified deletion event in the same transaction. Run post-commit file cleanup without waiting for workers.

**Block If:** A discovered lifecycle owner cannot delete, triage, or de-identify its account data transactionally,
or a contract conflict would require retaining identifiable deleted-account data.

**Never:** Add self-service deletion, delete courses with staff accounts, coordinate with in-flight providers or
workers, expose audit secrets/content, audit ordinary denied reads, or modify unrelated lifecycle behavior.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Delete student | Administrator targets student-only account | All account data cascades; `204` | Atomic rollback on failure |
| Delete staff | Different administrator targets eligible staff | Assignments removed and work triaged; `204` | Atomic rollback on failure |
| Protected staff | Last administrator or sole course supervisor | No state changes | Stable `409` conflict |
| Invalid actor/target | Non-admin, self-targeted staff, or missing target | No state changes | Stable `403` or hidden `404` |
| Audit list | Administrator requests valid page | Newest-first JSON:API collection | Bounded deterministic pagination |
| Audit denial | Non-administrator requests audit list | No records exposed or denial event written | Stable `403` |

</intent-contract>

## Code Map

- `internal/user/user.go` and new deletion-focused files -- account ownership, authorization, transaction, handler.
- `internal/lifecycle/lifecycle.go` -- registered account deletion and post-commit cleanup inversion.
- `internal/course/course.go` -- course-owned sole-supervisor guard and assignment removal.
- `internal/mentoring/service.go` -- existing account deletion already triages open mentor work and clears authors.
- `internal/material/material.go`, `internal/tutoring/service.go`, `internal/speech/service.go` -- existing account deleters.
- `internal/audit/audit.go` -- action registry, de-identification, paginated administrator query, and HTTP route.
- `migrations/000016_account_deletion_audit.up.sql` -- generalized audit fingerprint storage and query indexes.
- `cmd/mia/main.go` -- register lifecycle owners and both new routes after dependency construction.
- `api-doc/openapi.yaml`, `api-doc/paths/users.yaml`, `api-doc/paths/audit.yaml`, and resource schemas -- transport contract.
- `docs/api.md` -- replace the deferred audit decision with implemented behavior.

## Tasks & Acceptance

**Execution:**
- Implement and test atomic account deletion, safeguards, lifecycle invocation, audit de-identification, and cleanup.
- Implement and test administrator-only paginated audit retrieval with bounded content-free output.
- Wire course account lifecycle and routes; update migration and OpenAPI/docs contracts.

**Acceptance Criteria:**
- Given an administrator and a student-only target, when the account is deleted, then all registered account data
  is removed atomically and retained audit references use one random deletion fingerprint.
- Given a different administrator and eligible staff target, when deletion succeeds, then assignments are removed,
  open mentoring work is triaged, historical actor references are cleared, and the account is gone.
- Given the last administrator or a sole course supervisor, when deletion is attempted, then the API returns a
  stable conflict and no mutation commits.
- Given an administrator, when audit events are listed, then bounded newest-first pages contain only allowlisted
  content-free fields and strict UTC instants; non-administrators receive no data.

## Spec Change Log

## Review Triage Log

### 2026-09-06 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 0
- defer: 0
- reject: 0
- addressed_findings:
  - none

## Design Notes

The deletion fingerprint is generated once by `audit` for an account deletion and applied to actor, subject, and
typed metadata references before the user row is removed. Foreign-key `SET NULL` then removes live identity links
without losing the opaque correlation needed within retained audit history.

## Verification

**Commands:**
- `gofmt` on changed Go files -- expected: clean formatting.
- `go test ./...` -- expected: all tests pass.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.
- `npx @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml` -- expected: API lint passes.

## Auto Run Result

Summary: Implemented atomic administrator account deletion, lifecycle-owner cascades, protected-account
safeguards, de-identified audit history, and the administrator audit-event collection.

Files changed: account deletion and audit services/handlers/tests, course lifecycle wiring, migration 16,
central errors/actions, command wiring, OpenAPI resources/paths, and API narrative.

Review findings breakdown: 0 patches applied, 0 items deferred, 0 items rejected.

Follow-up review recommendation: false (0 high, 0 medium, 0 low patches; score 0).

Verification: `gofmt`, `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...`,
Redocly API lint, and Markdown lint passed. The optional repository-wide zero-threshold JSCPD scan still reports
74 pre-existing clones; the final scan reports no clone involving a story-added file.

Residual risks: none identified for the implemented scope.
