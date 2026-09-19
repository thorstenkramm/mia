---
title: 'Keep MFA-completed browser sessions usable'
type: 'bugfix'
created: '2026-09-19'
status: 'done'
baseline_commit: 'f0e6055da37ced4cc0be72e490b19ecf6e53ef6b'
review_loop_iteration: 0
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/.agents/rules/echo.md'
  - '{project-root}/.agents/rules/json-api.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Successful MFA challenge completion returns an authenticated or password-change session document, but the
serialized session cookie retains the consumed MFA challenge ID. The next protected request rejects that internally
inconsistent cookie as unauthenticated, preventing MFA users from continuing.

**Approach:** Ensure every authentication-stage transition serializes only state valid for the destination stage.
Remove inherited MFA challenge state when transitioning out of the MFA stage while preserving strict cookie validation
and browser-generation binding.

## Boundaries & Constraints

**Always:** Keep `mfa_challenge_id` present and non-empty only for the `mfa` stage. Preserve the existing validated
account ID, security generation, browser generation, CSRF rotation, idle deadline, absolute deadline, and destination
stage. Cover both TOTP/SMS verification and recovery-code completion because they share the transition path. Cover
transitions to both `authenticated` and `password-change`, including successful password replacement afterward.

**Ask First:** Halt if the fix requires weakening stage/challenge validation, changing session lifetimes, rotating the
browser marker to a new generation, adding server-side session records, or changing API representations.

**Never:** Do not accept a non-empty MFA challenge in an authenticated or password-change cookie, special-case the
protected endpoint, alter MFA challenge consumption, expose cookie contents, or modify the separate enrollment-conflict
behavior.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| TOTP completion | Valid MFA-stage cookie and fresh factor code | Authenticated cookie works on the next protected request | Existing invalid/replay errors remain unchanged |
| Recovery completion | Valid MFA-stage cookie and unused recovery code | Authenticated cookie works and code remains single-use | Existing invalid-code handling remains unchanged |
| MFA before password change | Account requires password replacement | Completion cookie works on password-change route | Full routes remain forbidden until replacement |
| Password replacement | Valid post-MFA password-change cookie | Final authenticated cookie works on a protected request | Existing password validation remains unchanged |

</frozen-after-approval>

## Code Map

- `internal/httpserver/httpserver.go` -- `saveSession` at lines 402-436 calls `CookieStore.New`, which decodes the
  request cookie and retains old values; clear inherited `mfa_challenge_id` before conditionally writing a destination
  challenge. Keep `loadSession` validation at lines 470-481 strict and unchanged.
- `internal/auth/auth.go` -- `verifyChallenge` and `consumeChallengeRecoveryCode` both call `TransitionSession`; their
  response documents use request-local destination state and currently hide the serialized-cookie inconsistency.
- `internal/auth/auth_test.go` -- existing tests cover replay, failed recovery consumption, and transition selection but
  do not adopt successful completion cookies for a subsequent routed request. Add full HTTP lifecycle coverage here.
- `internal/httpserver/httpserver_test.go` -- existing cookie-jar test covers initial issuance only; read-only evidence
  that browser/session pairing must remain strict.
- `docs/product-requirements.md`, `docs/api.md`, `api-doc/paths/auth.yaml` -- read-only contracts already require the
  successful transition; no transport or product behavior change is needed.
- `/Users/thorsten/projects/thorsten/mia-e2e-tests/defects/02-mfa-completion-session-rejected.md` -- external QA evidence;
  do not modify the separate test repository in this backend change.

## Tasks & Acceptance

**Execution:**
- [x] `internal/httpserver/httpserver.go` -- remove inherited MFA challenge state before serializing a destination
  session -- restore the stage/challenge invariant without weakening validation.
- [x] `internal/auth/auth_test.go` -- exercise successful TOTP and recovery-code completion through real handlers and
  reuse each returned cookie pair on the next allowed route -- prove the browser-visible lifecycle works.

**Acceptance Criteria:**
- Given a valid MFA challenge, when TOTP/SMS verification or recovery-code consumption succeeds, then the returned
  cookie has no stale MFA challenge state outside the MFA stage and authenticates its next stage-allowed request.
- Given MFA precedes mandatory password replacement, when MFA succeeds and the password is replaced, then each returned
  cookie works on the next allowed route and the final session is authenticated.
- Given malformed or inconsistent cookie state, when session validation runs, then it remains uniformly rejected rather
  than being tolerated for compatibility.

## Design Notes

Gorilla `CookieStore.New` decodes an existing request cookie despite its name. Session serialization must therefore
delete stage-specific values before setting the destination state. The strict loader is a security boundary and is not
the defect.

## Verification

**Commands:**
- `go test ./internal/auth ./internal/httpserver` -- expected: all completion paths and cookie invariants pass.
- `./run-all-tests.sh` -- expected: the authoritative project gate passes, including race and security checks.

## Suggested Review Order

**Session serialization**

- Clears request-inherited MFA state before writing the destination-stage cookie.
  [`httpserver.go:413`](../../internal/httpserver/httpserver.go#L413)

**Browser lifecycle coverage**

- Exercises TOTP, SMS, recovery, and password-change sessions through their next allowed routes.
  [`auth_test.go:1009`](../../internal/auth/auth_test.go#L1009)

### Review Findings

- [x] [Review][Patch] Cover stale MFA-cookie replacement during non-MFA login [internal/auth/auth_test.go:1009]
- [x] [Review][Patch] Make `assertNoMFAChallenge` reject missing or undecodable session cookies [internal/auth/auth_test.go:1910]
- [x] [Review][Defer] Reset all inherited session values instead of the current stage-specific key [internal/httpserver/httpserver.go:413] — deferred, pre-existing design hardening concern
- [x] [Review][Defer] Validate the `stage` and `challengeID` pairing at the session API boundary [internal/httpserver/httpserver.go:402] — deferred, pre-existing caller-contract concern
