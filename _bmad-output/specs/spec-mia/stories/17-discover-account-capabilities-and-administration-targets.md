---
title: 'Discover Account Capabilities and Administration Targets'
type: 'feature'
created: '2026-09-14'
status: 'done'
review_loop_iteration: 0
followup_review_recommended: true
baseline_revision: '66e7035b407c9b656bb1f12e2ac680e5f5b8e058'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Browser clients cannot discover the current account's scoped workflows, and administrators have no safe
global account collection or authoritative target detail before granting permanent roles or deleting an account.

**Approach:** Add a non-refreshing capability projection assembled through owner APIs, plus bounded administrator-only
account list/detail reads. Bind administrator role grants and deletion to the strong validator returned by detail.

## Boundaries & Constraints

**Always:** Return an explicit release-one capability catalog with global, course, student, and own-resource scope
shapes, including unavailable actions and empty scope arrays. Reload roles and assignments on each request, preserve
scope unions without widening, keep account reads administrator-only and minimally disclosed, and atomically recheck
authorization, target state, lifecycle guards, and `If-Match` for reviewed mutations. Use central pagination,
JSON:API errors, CSRF, no-store, scoped owner APIs, transaction-bound audit, deterministic username-key/ID ordering,
and unchanged authentication deadlines.

**Block If:** A new authorization boundary, account state, action family, disclosed personal field, or lifecycle
consequence is needed beyond the binding PRD and Story 2.4 contract.

**Never:** Expose email, profile, password, MFA, security-generation, course-content, tutoring, or mentoring content in
administrator account reads; derive authorization from a client capability response; perform fuzzy, partial, broad text,
or relationship-content search; mutate on missing or stale preconditions; or reveal missing and inaccessible targets
differently.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Capability read | Single or multiple roles and assignments | Stable actions with explicit unioned scopes | No idle refresh |
| Empty capability scope | Role without matching assignment or membership | Action remains explicit with empty scope | No denial probing |
| Account collection | Valid page and exact allowlisted filters | Minimal rows ordered by username key and ID | Empty page is `data: []` |
| Invalid collection | Unknown, duplicate, malformed, or excessive query | No broad query executes | `422 user_account_query_invalid` |
| Account detail | Administrator and opaque user ID | Minimal state, roles, action eligibility, consequences, strong ETag | Hidden target is `404 user_not_found` |
| Reviewed mutation | Current strong ETag | Exact idempotent grant or deletion effect commits | Audit in transaction |
| Missing/stale review | Missing, malformed, or changed ETag | No mutation | `428` or `412` stable error |

</intent-contract>

## Code Map

- `internal/capability/*` -- new projection slice; assembles owner-provided user/course/mentoring scopes and serves the
  authenticated current-account capability resource without refreshing session deadlines.
- `internal/user/administration.go`, `internal/user/administration_handler.go` -- user-owned bounded account discovery,
  minimal target projection, strong validator, eligibility, and precondition-bound administrator mutations.
- `internal/user/user.go`, `internal/course/course.go`, `internal/mentoring/service.go` -- narrow table-owner APIs for role,
  course, membership, shared-student, and mentor assignment scope; no foreign-table SQL in the projection package.
- `internal/user/deletion.go`, `internal/user/deletion_handler.go`, `internal/invitation/roles.go` -- preserve lifecycle
  cleanup and FR-13 role semantics while moving reviewed administrator transport to the user-owned boundary.
- `internal/httpserver/{errors,links,response}.go` -- stable account query/precondition errors and filter-preserving
  deterministic collection navigation through the shared protocol owner.
- `cmd/mia/main.go` -- wire capability and administration routes after their owner services exist.
- `api-doc/{openapi.yaml,paths/users.yaml,schemas/resources/resources.yaml,schemas/requests/requests.yaml}` -- document
  paths, action/scope vocabulary, filters, fields, ETag/If-Match, errors, CSRF, and no-store behavior.
- `docs/api.md`, `docs/product-requirements.md` -- record capability and administrator discovery rationale without
  duplicating field-level OpenAPI schemas.

## Tasks & Acceptance

**Execution:**
- [x] `internal/{capability,user,course,mentoring}/*.go` -- add owner-scoped capability inputs and a current-account
  capability endpoint with explicit empty scopes.
- [x] `internal/user/administration*.go`, `internal/httpserver/{errors,links,response}.go` -- add bounded administrator
  account collection/detail with exact filters, deterministic pagination, minimal fields, target eligibility,
  consequence identifiers, and strong ETags.
- [x] `internal/user/{administration,deletion}*.go`, `internal/invitation/roles.go` -- bind administrator role grants and
  account deletion to current `If-Match` inside their mutating transactions while preserving role coupling, deletion
  cleanup, idempotency, auditing, and existence hiding.
- [x] `internal/{capability,user,httpserver}/*_test.go` -- cover authorization, disclosure, scope union, filtering,
  pagination, stale concurrency, and sign-in expiry.
- [x] `api-doc/*`, `docs/{api,product-requirements}.md`, `cmd/mia/main.go` -- publish the complete contract and wire it.

**Acceptance Criteria:**
- Given any supported role and assignment combination, when capabilities are read, then stable actions contain only the
  valid union of explicit global, course, student, and own-resource scopes and absent scopes remain explicit.
- Given roles, assignments, membership, ban, or account state changes, when capabilities are reread, then committed state
  appears immediately without idle refresh and every operation still authorizes independently.
- Given an administrator lists accounts with valid exact filters, when the query runs, then minimal metadata is returned
  in normalized-username/ID order with deterministic bounded navigation, including a valid empty collection.
- Given an administrator reads an account by opaque ID, when it exists, then only ID, username, class/state, permanent
  roles, action eligibility, and consequence summaries are returned with a strong effect-complete ETag.
- Given a current ETag on grant or deletion, when the administrator confirms, then authorization, state, lifecycle
  guards, precondition, role coupling, cleanup, and audit commit atomically; absent or stale validators are no-ops.
- Given access expires or is lost, when any account administration request runs, then no protected account data or
  mutation is returned and authorized empty collections remain distinguishable from sign-in expiry.

## Spec Change Log

## Review Triage Log

### 2026-09-14 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 4 (high 0, medium 4, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[medium]` `[patch]` Kept mentor scopes as course-and-student pairs rather than widening two independent lists.
  - `[medium]` `[patch]` Made capability and administration projections use coherent SQLite transaction snapshots.
  - `[medium]` `[patch]` Required the course-owned sole-supervisor reader for registered administration/deletion routes.
  - `[medium]` `[patch]` Split global course-record and supervisor-assignment actions to avoid implying supervisor powers.

## Design Notes

The capability resource is navigation guidance, never an authorization token. Coarse release-one action identifiers
describe existing shell workflows; owner APIs supply only currently valid IDs. Account-detail ETags cover actor authority,
target roles and state, administrator count, verified-staff grant eligibility, and sole-supervisor responsibility so no
reviewed grant or deletion silently changes meaning before commit.

## Verification

**Commands:**
- `gofmt` on changed Go files -- passed.
- `go test ./...` -- passed.
- `go vet ./...` -- passed.
- `golangci-lint run ./...` -- passed with zero issues.
- `npx @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml` -- passed.
- `./run-all-tests.sh` -- passed formatting, tests, vet, lint, race, duplication, Trivy, Redocly, and Markdown gates.
  `govulncheck` was not installed, so the optional gate was skipped.

## Auto Run Result

Status: done

Summary: Added authoritative current-account capabilities, minimal administrator account collection/detail reads, and
strong-validator binding for administrator role grants and account deletion.

Files changed: capability, user, course, mentoring, and HTTP kernel Go sources and tests; command wiring; OpenAPI user
paths and schemas; API and product-requirement documentation; this story record.

Review findings breakdown: four medium patches applied; no items deferred or rejected. Follow-up review recommendation:
true (high 0, medium 4, low 0; score 12).

Verification: the complete project gate passed. The optional `govulncheck` component was unavailable and skipped.

Residual risks: none identified.
