---
title: 'Resolve and Grant Mentor Targets Safely'
type: 'feature'
created: '2026-09-15'
status: 'done'
review_loop_iteration: 0
followup_review_recommended: false
baseline_revision: '3bfa19ebf189efe6a0f5c8422d30373870f4cc02'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** Supervisors can grant mentor role by opaque ID but cannot safely review the exact eligible staff target,
and unrestricted probes could become an account directory or existence oracle.

**Approach:** Add one focused, exact-ID, CSRF-protected preflight with layered bounded throttling and a minimal target
projection. Bind supervisor mentor grants to its strong validator and current eligibility in the grant transaction.

## Boundaries & Constraints

**Always:** Accept one canonical `u_`-prefixed UUID v4 only; authorize the actor as a current supervisor before target
lookup; expose only target ID, username, nullable display name, and `available` or `already_granted` mentor action state.
Reject invalid ID resource values before lookup or throttling. Use a fixed unavailable response for well-formed hidden,
ineligible, and throttled targets without `Retry-After` or target data. Keep limiter state bounded, do not create a
durable resolution record, recheck all grant state atomically, audit role mutations and throttling without the submitted
target, and preserve API-wide CSRF, no-store, and JSON:API behavior.

**Block If:** Implementation requires exposing another field, granting a different role, changing permanent-role
lifecycle, or weakening the fixed hidden-target outcome.

**Never:** Add browse, autocomplete, partial, prefix, fuzzy, batch, suggestion, pagination, or result-count behavior;
reuse administrator account-directory visibility for supervisors; retain rejected IDs; return target-specific timing or
retry metadata; or automatically replay a grant after an ambiguous outcome.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Eligible target | Exact staff ID with verified email | Minimal identity, grant state, strong ETag | `200` |
| Existing mentor | Exact eligible target already mentor | `already_granted`; grant remains idempotent | `200` / `204` |
| Invalid resource value | Missing, null, non-string, or noncanonical ID | No lookup or limiter consumption | `422 auth_invalid_request` |
| Hidden target | Well-formed unknown, student-only, unverified, banned, or deleted ID | No lookup detail or retained resolution | Fixed `404 user_mentor_target_unavailable` |
| Throttled probe | Actor or source-IP window exhausted | Same hidden-target response and headers | No `Retry-After` |
| Reviewed grant | Mentor role plus current preflight ETag | Atomic recheck and grant | `204` |
| Unsafe grant | Missing or stale ETag, changed eligibility | No role mutation | `428` or `412` |

</intent-contract>

## Code Map

- `internal/user/administration.go` -- add user-table-owner exact mentor-target projection and effect-complete ETag;
  reuse role eligibility and the existing idempotent `GrantRole` mutation.
- `internal/user/administration_handler.go` -- serve focused preflight without broadening administrator account reads.
- `internal/invitation/roles.go` -- require the mentor preflight validator inside the existing grant transaction while
  preserving administrator/supervisor grant review and same-transaction audit.
- `internal/httpserver/{httpserver,limit,errors}.go` -- own named actor/IP rolling limits and the fixed unavailable error.
- `internal/audit/audit.go` -- register content-free mentor-target throttling without submitted target identity.
- `internal/user/administration_test.go`, `internal/invitation/invitation_test.go`, `internal/httpserver/limit_test.go` --
  cover exact matching, disclosure, hidden/throttled equivalence, limiter bounds, stale races, and idempotency.
- `api-doc/{openapi.yaml,paths/users.yaml,schemas/resources/resources.yaml,schemas/requests/requests.yaml}` -- document the
  focused request/response, CSRF, limits, ETag/If-Match, reconciliation, errors, and no-store contract.
- `docs/api.md`, `docs/product-requirements.md` -- record the direct mentor-target workflow and privacy rationale.

## Tasks & Acceptance

**Execution:**
- [x] `internal/user/administration*.go` -- implement exact eligible mentor-target preflight and validator generation.
- [x] `internal/httpserver/{httpserver,limit,errors}.go`, `internal/audit/audit.go` -- add layered bounded throttling and
  one disclosure-safe unavailable outcome.
- [x] `internal/invitation/roles.go` -- bind only supervisor mentor grants to the reviewed snapshot.
- [x] `internal/{user,invitation,httpserver}/*_test.go` -- prove exact lookup, disclosure, authorization, throttling,
  stale no-op, and idempotent grant behavior.
- [x] `api-doc/*`, `docs/{api,product-requirements}.md` -- publish the complete browser/API contract.

**Acceptance Criteria:**
- Given a supervisor submits one exact bounded user ID, when preflight runs, then no directory-like operation occurs and
  no durable resolution state is created.
- Given an eligible verified staff target, when resolved, then only ID, username, nullable display name, grant state,
  and a strong eligibility-complete ETag are returned.
- Given a missing, null, non-string, or noncanonical target ID, when preflight validates the resource, then it returns
  `422 auth_invalid_request` before target lookup or dedicated throttling.
- Given any well-formed hidden, ineligible, deleted, or throttled target condition, when resolved, then the same fixed
  status, body, headers, and non-target-specific timing behavior result.
- Given the reviewed `If-Match`, when mentor grant runs, then authority, exact target, class, state, verified email,
  mentor role, and validator are atomically rechecked and only mentor is idempotently granted.
- Given a missing or stale validator, when grant runs, then no role changes and a fresh preflight is required.
- Given resolution is unavailable, when unrelated account or mentoring operations run, then they remain unaffected.

## Spec Change Log

### 2026-09-15 — Product-owner clarification

The required `data.attributes.user_id` member must contain a canonical string MIA user ID. Missing, null, non-string,
and noncanonical values return `422 auth_invalid_request` before target lookup or dedicated throttling; well-formed
unknown, hidden, or ineligible IDs retain the fixed `404 user_mentor_target_unavailable` outcome.

### 2026-09-15 — Resource-value classification correction

The intent contract and acceptance criteria now reflect the corrected resource-value classification consistently.

## Review Triage Log

### 2026-09-15 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 1 (high 0, medium 1, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[medium]` `[patch]` Rejected explicitly empty CSRF cookie/header pairs instead of treating equal empty values as a
    valid double-submit token.

## Design Notes

The preflight uses fixed one-hour windows of 10 attempts per authenticated supervisor account and 30 per trusted source
IP, matching the established exact-token anti-enumeration budget. It creates no target-derived limiter key, preventing a
submitted hidden identity from becoming a durable or timing-visible dimension. Invalid, missing, null, non-string, and
noncanonical `data.attributes.user_id` values return `422 auth_invalid_request` before target lookup or dedicated
throttling. Only well-formed unavailable IDs receive the fixed unavailable behavior shared with throttling and target
ineligibility.

## Verification

**Commands:**
- `gofmt` on changed Go files -- passed.
- `go test ./internal/user ./internal/invitation ./internal/httpserver` -- passed.
- `npx @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml` -- passed.
- `./run-all-tests.sh` -- passed Go formatting, tests, vet, lint, race, duplication, Trivy, Redocly, and Markdown gates.
  The optional `govulncheck` tool was not installed and was skipped.

## Auto Run Result

Status: done

Summary: Added an exact-ID supervisor mentor-target preflight with minimal identity, strong validators, layered bounded
throttling, and a fixed hidden-target outcome. Mentor grants now require the reviewed validator, recheck eligibility in
the grant transaction, remain idempotent, and audit only a durable role change.

Files changed: user administration and role-grant code and tests; HTTP limiter, error registry, and CSRF validation;
audit action registry; OpenAPI schemas and paths; API/product contracts; JSON:API exception rule; this story record.

Review findings breakdown: one medium patch applied; no items deferred or rejected. Follow-up review recommendation:
false (high 0, medium 1, low 0; score 3).

Verification: the complete project gate passed. The optional `govulncheck` component was unavailable and skipped.

Residual risks: none identified.

### Fix verification

The product-owner request-shape clarification and review findings were applied. Throttle audit failures now reach the
central error handler, explicit empty CSRF values are rejected, and real-route tests prove the shared 30-attempt source-IP
limit across supervisor sessions. `./run-all-tests.sh` passed after these fixes; optional `govulncheck` remained
unavailable.
