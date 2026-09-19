---
title: 'Reject duplicate pending MFA enrollment cleanly'
type: 'bugfix'
created: '2026-09-19'
status: 'done'
baseline_commit: '02164f5f57a29e65f54dab2046c137b871de565a'
review_loop_iteration: 0
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/.agents/rules/json-api.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Starting a second unexpired MFA enrollment for the same authenticated user violates the
`mfa_enrollments.user_id` unique index. The unhandled SQLite error becomes a 500 response, although the user can
correct the state by cancelling, verifying, or waiting for the existing enrollment to expire.

**Approach:** Treat the existing pending enrollment as a documented `409 Conflict` with a stable MFA-specific code.
Retain the database unique index as the concurrency control and translate only its expected conflict outcome.

## Boundaries & Constraints

**Always:** Preserve the existing enrollment, including its secret or SMS code, expiry, failures, and replacement
proof. Delete expired enrollment rows before a new attempt as today. Return the central JSON:API error document with
a stable `auth_mfa_enrollment_pending` code and no persistence detail. Ensure concurrent starts yield at most one
created enrollment and all losing requests receive the same conflict response. Keep the existing active-factor,
proof, SMS availability, and rate-limit outcomes unchanged.

**Ask First:** Halt if implementation requires changing the one-pending-enrollment product invariant, replacing a
pending enrollment implicitly, or exposing its identifier, method, expiry, or secret in the error response.

**Never:** Do not remove or weaken the unique index, use a check-then-insert preflight as the sole concurrency
control, treat the error as a provider failure or generic `auth_mfa_unavailable`, expose SQLite errors, or modify the
MFA login-session defect in this work.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| First start | Authenticated user has no live enrollment | Create a pending enrollment and return `201` | Existing validation and provider handling apply |
| Duplicate start | Authenticated user has an unexpired enrollment | Leave the existing row unchanged and return `409` | JSON:API `auth_mfa_enrollment_pending`; no SQL detail |
| Concurrent starts | Two starts for one user without a live enrollment | Exactly one request creates the row | Every losing request returns the same `409` conflict |
| Expired start | Existing enrollment has expired | Remove expired row, then create a new enrollment | Existing `201` behavior remains |

</frozen-after-approval>

## Code Map

- `internal/auth/auth.go` -- `startEnrollment` deletes expired rows at lines 871-874 and performs the
  race-arbitrating insert at lines 880-883; map only the enrollment user-ID uniqueness error to a local sentinel and
  then to the central error code.
- `migrations/000004_mfa.up.sql` -- `mfa_enrollments_user_idx` at line 27 enforces the one-pending-enrollment
  invariant; it remains unchanged.
- `internal/httpserver/errors.go` -- owns stable API codes and their status/title/detail mappings; add the MFA
  pending-enrollment conflict entry here.
- `internal/auth/auth_test.go` -- MFA integration tests begin at `TestTOTPEnrollmentLoginAndStepReplay`; reuse the
  response helpers and the concurrent-request pattern in `TestConcurrentResetProducesOneSuccess`.
- `api-doc/paths/auth.yaml` -- `mfaEnrollments.post` currently lists `201`, protocol errors, `401`, `403`, `422`,
  and `429`; declare its `409` response and stable-error meaning.
- `docs/api.md` -- MFA narrative and state-overview documentation; update only the directly affected behavior.
- `internal/auth/mfa.go` -- read-only evidence: MFA state treats a live pending enrollment as authoritative and
  disables a second enrollment action.

## Tasks & Acceptance

**Execution:**
- [x] `internal/httpserver/errors.go` -- register `auth_mfa_enrollment_pending` as a `409 Conflict` central error --
  make the recoverable state observable without exposing storage internals.
- [x] `internal/auth/auth.go` -- translate the specific unique-index conflict from pending-enrollment insertion into
  the new domain error -- preserve the index as the race-safe arbiter.
- [x] `internal/auth/auth_test.go` -- cover sequential duplicate starts, concurrent starts, and the retained single
  row -- prevent regression to an internal error or an implicit overwrite.
- [x] `api-doc/paths/auth.yaml` and `docs/api.md` -- document the conflict status and meaning -- keep the API
  contract aligned with the implementation.

**Acceptance Criteria:**
- Given an authenticated user with a live pending MFA enrollment, when they start another enrollment, then the API
  returns a JSON:API `409` error with code `auth_mfa_enrollment_pending` and does not disclose persistence details.
- Given two concurrent enrollment-start requests for an authenticated user with no pending enrollment, when both
  reach the unique constraint, then one returns `201`, every other request returns the documented `409`, and one
  enrollment row exists.
- Given only an expired enrollment, when the user starts enrollment, then the expired row is removed and a new
  enrollment is returned with `201`.

### Review Findings

- [x] [Review][Patch] Detect a live pending enrollment before SMS reservation, provider/mobile checks, or replacement
  proof validation so every valid duplicate returns the specified `409`; add SMS and active-factor regression coverage
  [`internal/auth/auth.go:836`](../../internal/auth/auth.go#L836)
- [x] [Review][Patch] Record the newly confirmed duplicate-enrollment behavior in the authoritative product requirements
  [`docs/product-requirements.md:659`](../../docs/product-requirements.md#L659)

## Design Notes

The unique index is the only reliable final arbiter between concurrent transactions. Translating its known constraint
failure at the handler boundary makes the public outcome deterministic without adding a racy preliminary state check.

## Verification

**Commands:**
- `go test ./internal/auth ./internal/httpserver` -- expected: MFA conflict and central error tests pass.
- `./run-all-tests.sh` -- expected: the authoritative project quality suite passes, including API and Markdown lint.

## Suggested Review Order

**Conflict Translation**

- Convert only the known SQLite uniqueness failure after transaction-safe expiry cleanup.
  [`auth.go:851`](../../internal/auth/auth.go#L851)

- Centralize the public conflict status, code, title, and safe detail.
  [`errors.go:48`](../../internal/httpserver/errors.go#L48)

**API Contract**

- Declare the conflict response on the enrollment-start operation.
  [`auth.yaml:317`](../../api-doc/paths/auth.yaml#L317)

- Explain that the existing pending enrollment remains unchanged.
  [`api.md:276`](../../docs/api.md#L276)

**Regression Coverage**

- Cover duplicate retention and expired enrollment replacement.
  [`auth_test.go:718`](../../internal/auth/auth_test.go#L718)

- Exercise concurrent enrollment starts and the unique-index conflict outcome.
  [`auth_test.go:772`](../../internal/auth/auth_test.go#L772)
