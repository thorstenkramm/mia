---
title: 'Inspect MFA State and Recover Lost Factors'
type: 'feature'
created: '2026-09-14'
status: 'done'
review_loop_iteration: 2
followup_review_recommended: false
baseline_commit: 'a5d63e565eca0b4bf010ac38cf1079adb239a8b8'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** Account holders cannot reconstruct active and pending MFA state after reload, and authorized supervisors
and administrators have no HTTP operation for lost-factor recovery.

**Approach:** Add a secret-free authoritative current-account MFA projection and one scoped reset command. Keep MFA
table ownership in `auth`, call course and user owner APIs for authorization and account effects, and commit cleanup and
audit atomically.

## Boundaries & Constraints

**Always:** Return active and pending opaque IDs, methods, pending expiry, replacement relation, and separate action
eligibility without destinations or secrets. Student-only reset requires a shared assigned course, invalidates every
cookie through security generation, and forces fresh-login password replacement. Staff reset requires a different
administrator and has the same generation-increment, cookie-invalidation, and fresh-login password gate. Both paths
remove all MFA artifacts and audit in one transaction.

**Block If:** A reset would require broadening student scope beyond a shared assigned course, permitting staff self-reset,
or exposing sole-administrator recovery through HTTP.

**Never:** Return TOTP provisioning data after setup, SMS destinations, codes, recovery codes, proof values or digests;
infer authorization from role alone; automatically retry an ambiguous reset; or make the overview refresh session idle
expiry.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| MFA overview | No factor, active factor, pending setup, or replacement | Explicit current state and action eligibility | Expired pending state is unavailable |
| Student reset | Shared-course supervisor and student-only target with active MFA | Remove all MFA state, bump generation, set password gate, audit | Hidden target returns one safe not-found |
| Staff reset | Different administrator and staff target with active MFA | Remove MFA state, bump generation, set password gate, audit | Self or non-admin target returns one safe denial |
| Concurrent reset | Factor removed after authorization or by another request | Exactly one mutation commits | Stale request returns reset-unavailable |

</intent-contract>

## Code Map

- `migrations/000018_mfa_factor_ids.up.sql` -- give active factors opaque mutation IDs without changing user ownership.
- `internal/auth/mfa.go` -- own MFA projection, reset transaction, complete artifact cleanup, and exact account effects.
- `internal/auth/auth.go` -- register and serialize the overview and reset routes; require active-factor IDs for disable.
- `internal/course/course.go` -- export transaction-aware shared-course student reset authorization.
- `internal/user/user.go` -- export staff-reset authorization and the shared reset generation/password-gate effect.
- `internal/audit/audit.go`, `internal/httpserver/errors.go` -- register content-free reset audit and stable safe errors.
- `cmd/mia/main.go` -- wire course authorization into auth and use the shared reset effect for local recovery.
- `internal/auth/auth_test.go`, `internal/course/course_test.go`, `internal/sqlite/sqlite_test.go` -- lifecycle,
  authorization, atomicity, migration, cookie, concurrency, and redaction coverage using real migrations.
- `api-doc/openapi.yaml`, `api-doc/paths/auth.yaml`, `api-doc/schemas/resources/resources.yaml` -- complete read and reset
  transport contracts.
- `internal/provider/sms/sms.go` -- project durable resend eligibility without consuming delivery capacity.
- `docs/api.md`, `docs/architecture.md`, `docs/database-layout.md` -- align implemented discovery and recovery.

## Tasks & Acceptance

**Execution:**

- [x] Add opaque active-factor persistence and use the ID in factor creation and disablement.
- [x] Implement current-account MFA state with explicit active, pending, replacement, and action eligibility.
- [x] Implement one reset route with shared-course student and different-administrator staff authorization.
- [x] Apply cookie invalidation to every reset class with atomic cleanup and audit.
- [x] Cover transitions, redaction, scope, self-targeting, stale concurrency, and session effects.
- [x] Publish complete OpenAPI and human-readable contracts.

**Acceptance Criteria:**

- Given any supported active and pending MFA combination, when the account holder reads MFA state, then the response
  returns mutation IDs, methods, pending expiry, replacement relation, and separate action eligibility without secrets.
- Given MFA state changes or invalidation, when state is reread, then it reflects only committed live state and requires
  no retained setup response.
- Given an authorized supervisor resets a shared student-only target, when the transaction commits, then all MFA
  artifacts are removed, all cookies are invalidated, password replacement is required after login, and audit is safe.
- Given a different administrator resets staff MFA, when the transaction commits, then all MFA artifacts are removed,
  all existing cookies are invalidated, password replacement follows a fresh login, and audit is safe.
- Given an unrelated supervisor, staff self-reset, missing target, absent factor, or concurrent stale reset, when reset
  is evaluated, then no unintended mutation occurs and the documented existence-safe error is returned.

## Spec Change Log

- 2026-09-14: Implemented and verified the authoritative MFA state projection and scoped lost-factor reset.
- 2026-09-14: Adopted the AD-8 generation-increment correction for all reset classes and added layered reset limiting.

## Review Triage Log

- Iteration 1 resolved snapshot consistency in the MFA projection, made SMS resend eligibility
  provider- and quota-aware, removed pending replacement state during disable, enforced and tested required factor IDs,
  and added direct cookie-effect coverage for student and staff reset paths. No follow-up review is recommended.
- Iteration 2 product-owner correction: staff and local administrator reset now increment security generation and
  invalidate every existing cookie per AD-8. The reset endpoint also applies layered source-IP and actor-account
  limiting before target evaluation and documents the central `429` response. No follow-up review is recommended.

## Design Notes

AD-8 is authoritative for every MFA reset class. Student, staff, and local sole-administrator reset increment security
generation in the same transaction as factor cleanup and the password gate, so every existing cookie is invalid and the
account must sign in again before replacing the password.

## Verification

**Commands:**

- `gofmt` on changed Go files -- passed.
- `go test ./internal/auth ./internal/httpserver ./internal/user ./cmd/mia` -- passed.
- `./run-all-tests.sh` -- passed Go tests, vet, golangci-lint, race tests, duplication, Trivy, Redocly, and Markdown.
  `govulncheck` was not installed, so the script skipped that optional check.

## Suggested Review Order

- Start with the secret-free state projection and atomic reset boundary in `internal/auth/mfa.go`.
- Confirm shared-course and different-administrator authorization in `internal/course/course.go` and
  `internal/user/user.go`.
- Trace the generation increment through `RequirePasswordChangeAfterMFAReset` and both HTTP and local reset callers.
- Review route serialization and stable errors in `internal/auth/auth.go` and `internal/httpserver/errors.go`.
- Verify factor-ID backfill in `migrations/000018_mfa_factor_ids.up.sql` and the API contract in
  `api-doc/paths/auth.yaml`.
