---
title: 'Multi-factor authentication'
type: 'feature'
created: '2026-08-31'
status: 'done'
baseline_commit: 'e10cec0'
review_loop_iteration: 1
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
  - '{project-root}/_bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/prd.md'
  - '{project-root}/_bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/addendum.md'
  - '{project-root}/_bmad-output/planning-artifacts/architecture/architecture-mia-2026-08-29/ARCHITECTURE-SPINE.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Accounts have no second authentication factor; passwordless attackers who compromise or guess
credentials have unrestricted access.

**Approach:** Add optional MFA with TOTP or SMS (one active factor per account), staged login verification, single-use
recovery codes, factor management with mfa-management proofs, and lost-factor reset paths for students (supervisor) and
staff (different administrator plus `reset-admin-mfa` command for sole admin).

## Boundaries & Constraints

**Always:** Apply FR-26..32, NFR-2..4, and AD-3, AD-7, AD-10, AD-12. TOTP: SHA-1, 6 digits, 30-second steps, ±1 step
skew, 20-byte secret; a time-step succeeds only once per factor. SMS: 6 uniformly random decimal digits, 30-minute
single-use expiry, resends reuse the same code (60-second cooldown, 5/hour, 10/day per account + destination). Pending
enrollment expires after 30 minutes or 5 failed verifications. Login MFA challenges expire after 30 non-refreshing
minutes and are invalidated by 5 failures. Recovery codes: 10 codes, 16 characters each (Crockford Base32), shown once,
stored as non-reversible digests only, invalidated on factor replacement. mfa-management proof: SHA-256 digest-only
storage, 5-minute expiry, single-use, consumed atomically with the MFA mutation. Any password, MFA, ban-state, or
account-state change invalidates all MFA challenges and proofs. Lost-factor resets increment security generation and
force password replacement. Do not log or audit MFA secrets, codes, or recovery-code values.

**Ask First:** Halt for ClickSend SMS provider integration (story 1-4 implements the SMS MFA logic but may stub the
provider if ClickSend is deferred), changes to password-change flow, or invitation acceptance MFA scope.

**Deferred:** HTTP MFA reset routes (`POST /api/v1/users/{id}/mfa-resets`) require course membership and scoped
supervisor authorization that do not exist until story 1-8. This story implements core MFA (enrollment, login
challenges, proofs, recovery codes, `reset-admin-mfa` command). The HTTP reset routes will be added in story 1-8 when
the authorization context exists.

**Never:** Do not store TOTP secrets in reversible form after enrollment succeeds. Do not reveal factor existence or
account state through MFA error responses beyond the documented codes. Do not allow self-MFA-reset for staff. Do not
create or expose recovery-code regeneration without factor replacement.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| TOTP enrollment start | Authenticated user, no pending enrollment | Create pending TOTP, return provisioning URI and secret | Reject if active factor exists without management proof |
| SMS enrollment start | Authenticated user, verified mobile, ClickSend configured | Create pending SMS, send code to profile mobile | 422 if no verified mobile or ClickSend unavailable |
| Enrollment verification (TOTP) | Pending TOTP enrollment, valid 6-digit code | Activate factor, return 10 recovery codes, delete pending | 422 after 5 failures (deletes enrollment) |
| Enrollment verification (SMS) | Pending SMS enrollment, correct code within expiry | Activate factor, return 10 recovery codes, delete pending | 422 for wrong/expired code |
| Enrollment resend (SMS) | Pending SMS enrollment, cooldown passed | Resend same code, no expiry extension | 429 if cooldown active or send limits exceeded |
| Login with MFA | Correct password, active factor | Return `mfa` stage cookie + challenge ID | Challenge expires after 30 mins |
| Login MFA verification (TOTP) | `mfa` cookie, matching challenge, correct code, unused step | Transition to `password-change` or `authenticated` | 422 for wrong code; 403 if step already used |
| Login MFA verification (SMS) | `mfa` cookie, matching challenge, correct code | Transition cookie stage | 5 failures invalidate challenge |
| Login MFA recovery-code | `mfa` cookie, matching challenge, valid unused code | Consume code, transition cookie stage | 422 for invalid/used code |
| Factor disable | Authenticated, active factor, valid mfa-management proof | Remove factor, invalidate recovery codes | 422 without valid proof |
| Factor replacement start | Authenticated, active factor, mfa-management proof | Create pending new factor (old stays active) | Activation atomically swaps and consumes proof |
| Sensitive-action proof | Authenticated, current-factor code or recovery code, current password | Return opaque 5-minute single-use proof | 422 on invalid code |
| Sole admin MFA reset | `reset-admin-mfa` command, server stopped | Remove factor, invalidate codes, increment security gen, force password change | Reject if multiple admins or server running |

Note: Student and staff HTTP MFA reset routes are deferred to story 1-8 when course membership authorization exists.

</frozen-after-approval>

## Code Map

- `internal/auth/auth.go` -- extend login handler for MFA stage, add challenge/verification/resend handlers for login
  and enrollment, add mfa-management proof lifecycle, add factor disable/replace routes.
- `internal/auth/mfa.go` -- TOTP verification (SHA-1, 6-digit, ±1 step, step-replay prevention), recovery-code
  generation/verification, factor lifecycle helpers.
- `internal/auth/mfa_test.go` -- unit tests for TOTP algorithm, step-replay, recovery-code digest comparison.
- `internal/auth/auth_test.go` -- integration tests for MFA enrollment, login challenges, proof lifecycle, resets.
- `internal/user/user.go` -- add MFA-related columns (active_mfa_method, totp_secret, sms_factor_destination,
  security_generation manipulation), recovery-code storage/verification, pending-enrollment management.
- `migrations/000004_mfa.up.sql` -- mfa_enrollments, mfa_challenges, mfa_management_proofs, recovery_codes tables;
  user columns for active factor.
- `internal/httpserver/errors.go` -- register MFA-related error codes (auth_mfa_required, auth_invalid_mfa_code,
  auth_mfa_step_used, auth_invalid_recovery_code, auth_mfa_challenge_expired, auth_mfa_proof_required, etc.).
- `internal/httpserver/limit.go` -- add MFA verification limits (10 per account, 30 per IP per 15 minutes per FR-28).
- `internal/audit/audit.go` -- register MFA audit actions (mfa.enrolled, mfa.disabled, mfa.replaced, mfa.challenge.*,
  mfa.recovery_code.used, mfa.reset).
- `cmd/mia/main.go` -- wire MFA handlers.
- `cmd/mia/reset_admin_mfa.go` -- implement `reset-admin-mfa` subcommand (server stopped, interactive, exact-username
  confirmation, sole-admin only).
- `docs/api.md` -- document MFA enrollment, challenge, verification, proof, and reset routes with request/response
  schemas.
- `internal/provider/sms/` -- SMS provider interface; stub implementation for this story if ClickSend deferred.

## Tasks & Acceptance

**Execution:**

- [x] `migrations/000004_mfa.up.sql` -- add MFA schema: pending enrollments, login challenges, management proofs,
  recovery codes (digest-only), user MFA columns.
- [x] `internal/auth/mfa.go` -- implement TOTP verification (RFC 6238), recovery-code generation (Crockford Base32),
  step-replay tracking, digest comparison.
- [x] `internal/auth/auth.go` -- extend login for MFA stage creation, add enrollment routes (POST, verification,
  resend, DELETE), add login challenge routes (verification, resend, recovery-code), add sensitive-action proof route,
  add factor disable/replace logic.
- [x] `internal/user/user.go` -- add MFA account-reset support and security-generation
  increment for resets.
- [x] `internal/httpserver/errors.go` and `internal/audit/audit.go` -- register MFA error codes and audit actions.
- [x] `internal/httpserver/limit.go` -- add MFA-specific rate limits.
- [x] `cmd/mia/main.go` -- implement offline `reset-admin-mfa` command with lock, sole-admin check,
  interactive confirmation.
- [x] `cmd/mia/main.go` -- wire MFA routes and dependencies.
- [x] `docs/api.md` -- document MFA routes, stages, and error responses.
- [x] `internal/auth/mfa_test.go` and `internal/auth/auth_test.go` -- tests for TOTP algorithm, enrollment
  lifecycle, login challenge flow, proof consumption, recovery codes, reset paths, rate limits, step-replay.
- [x] SMS provider stub or interface -- define the SMS interface; full ClickSend implementation may be deferred to a
  later story.

**Acceptance Criteria:**

- Given an authenticated user with no active factor, when they complete TOTP enrollment, then the factor activates and
  10 recovery codes are returned exactly once.
- Given a user with an active TOTP factor, when they log in with correct password, then login returns an `mfa` stage
  cookie and challenge; correct TOTP verification transitions to the next stage.
- Given a login MFA challenge, when the same TOTP time-step is submitted twice, then the second attempt fails with
  `auth_mfa_step_used`.
- Given an active factor, when the user provides current password and valid factor code, then they receive a 5-minute
  mfa-management proof usable exactly once for disable or replacement.
- Given exactly one administrator with active MFA, when `reset-admin-mfa` runs with correct confirmation while the
  server is stopped, then the factor is removed and password replacement is required.
- Given any MFA operation, when it completes, then no MFA secrets, codes, or recovery-code values appear in logs or
  audit records.

Note: Student/staff HTTP MFA reset acceptance criteria are deferred to story 1-8.

## Review Findings

- [x] #3 MFA-stage verification remains reachable when password replacement is required and transitions to that stage.
- [x] #4 Failed challenge, enrollment, and recovery-code submissions persist their failure state before returning.
- [x] #5 Management-proof TOTP verification atomically advances the accepted factor step.
- [x] #6 TOTP enrollment stores the accepted step as the active factor replay boundary.
- [x] #7 SMS enrollment, login challenges, resends, immutable destinations, and durable send reservations are implemented.
- [x] #8 Management proofs accept active-factor verification or atomically consumed recovery codes.
- [x] #9 MFA disable invalidates every remaining management proof and challenge in its mutation transaction.
- [x] #10 MFA failure, replay, and throttle outcomes write content-free audit events.
- [x] #11 Regression coverage includes password-gated MFA transition, failure invalidation, and replay boundaries.
- [x] #12 MFA throttles preserve `Retry-After` in the response.
- [x] #13 Starting enrollment clears expired pending state before the unique enrollment insert.
- [x] #14 Account-record reads in MFA flows use transaction-aware `user` package APIs.
