---
title: 'Staff password recovery over SMTP'
type: 'feature'
created: '2026-08-31'
status: 'done'
baseline_commit: 'd63f22195f1a9070bdded73dc6da8f3bfff57ffd'
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

**Problem:** Staff cannot recover a forgotten password. The recovery flow needs a dependable SMTP delivery adapter
without exposing whether an account exists or leaking credentials, tokens, or personal data.

**Approach:** Add the SMTP provider adapter and the auth-owned challenge lifecycle, then expose recovery request and
reset routes with the specified JSON:API contracts and document those contracts.

## Boundaries & Constraints

**Always:** Apply FR-14 and FR-20..21, NFR-2, NFR-4, and AD-3, AD-7, AD-10, and AD-12. Store only SHA-256 reset
token digests. Use canonical lowercase UUID v4 bearer tokens, a 30-minute expiry, and configured HTTPS `public_url`
links with tokens in fragments. Send English-only UTF-8 plain-text email with sanitized headers. A successful reset
atomically changes the password, consumes its challenge, invalidates every other challenge for the staff account, and
writes a content-free audit event. Do not revoke staff cookies. Request delivery uses fixed SMTP deadlines and never
automatically retries an ambiguous failure.

**Ask First:** Halt for invitation delivery, student temporary-password recovery, MFA recovery, or any change to
provider configuration semantics beyond the existing SMTP configuration.

**Never:** Do not expose reset tokens or passwords in logs, audit records, provider errors, URLs, database rows, or
responses. Do not create a browser session after reset. Do not allow recovery for student-only, banned, or deleted
accounts, and do not reveal any of those states. Do not alter supervisor-managed sprint status bookkeeping.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Recovery request | Existing non-banned staff username | Persist one usable digest-only challenge and send one plain-text link | Return `204` even if SMTP times out; log only sanitized failure data |
| Hidden request | Unknown, banned, student-only, malformed username, or rate-limited request | No challenge or email | Return the identical `204` response, without `Retry-After` |
| Definite delivery failure | SMTP rejects a newly created message definitively | Newly created challenge becomes unusable | Return `204`; retain no usable new token |
| Reset | Valid unexpired challenge plus compliant matching password | Change password and invalidate all account challenges atomically | Return `204`; no session created |
| Invalid reset | Unknown, expired, consumed, malformed, banned, or deleted token | No password change | Return `422 auth_invalid_reset_token` uniformly |

</frozen-after-approval>

## Code Map

- `internal/auth/auth.go` -- owns password login and changes, shared strict JSON:API decoder, auth route wiring, and
  recovery handlers.
- `internal/auth/auth_test.go` -- SQLite + Echo integration helpers provide the existing route-test conventions.
- `internal/user/user.go` -- owns user records; needs a scoped staff account lookup and password-update support only
  through its exported owner API.
- `migrations/000002_auth_and_users.up.sql` -- existing user/audit schema; add the auth-owned reset-challenge table
  as a forward migration.
- `internal/httpserver/limit.go` -- already defines recovery and reset IP/identifier/token limits; recovery throttling
  must retain the route's hidden `204` response.
- `internal/httpserver/errors.go` -- central stable code registry needs the invalid-reset-token code.
- `internal/audit/audit.go` -- owns registered same-transaction content-free actions.
- `internal/config/config.go` and `mia.example.toml` -- existing SMTP and HTTPS public URL configuration are the sole
  adapter inputs.
- `cmd/mia/main.go` -- composes auth and provider dependencies for `serve`.
- `docs/api.md` -- recovery routes are listed but still deferred; document the approved two-route request, response,
  and error contract.

## Tasks & Acceptance

**Execution:**
- [x] `migrations/000003_password_reset_challenges.up.sql` -- add auth-owned, digest-only reset challenges and indexes.
- [x] `internal/provider/smtp/` -- implement configured SMTP delivery with sanitized message construction,
  fixed deadlines, STARTTLS/implicit-TLS/plaintext transport handling, and deterministic timeout versus definite
  failure classification.
- [x] `internal/auth/` -- add challenge creation, atomic reset consumption/invalidation, recovery handlers, limits,
  audit effects, and provider wiring.
- [x] `internal/user/` -- provide owner APIs needed to locate eligible staff by complete username and update a reset
  password without altering cookie security generation.
- [x] `internal/httpserver/errors.go` and `internal/audit/audit.go` -- register the safe reset-token error and recovery
  audit actions.
- [x] `cmd/mia/main.go` -- construct the SMTP adapter using validated server configuration.
- [x] `docs/api.md` -- replace the recovery-route deferral with the approved request, `204`, and error contracts.
- [x] `internal/provider/smtp/` and `internal/auth/` tests -- use local SMTP fakes and real migrated SQLite to cover
  delivery, timeouts, hidden states, rate limits, token lifecycle, password policy, and audit redaction.

**Acceptance Criteria:**
- Given a valid staff username, when recovery is requested, then a single-use 30-minute fragment link is sent to its
  verified email while SQLite retains only a SHA-256 token digest.
- Given unknown, student-only, banned, malformed, or rate-limited recovery input, when a request is made, then it
  returns exactly the same `204` response and never sends a message.
- Given one valid reset token, when reset succeeds, then all reset challenges for that account become unusable without
  revoking existing browser cookies or creating a new one.
- Given any invalid reset token state, when reset is submitted, then the response is `422 auth_invalid_reset_token`
  without revealing the token's lifecycle state.

### Review Findings

- [x] \[Review]\[Patch] DeliveryManager.Admit silently drops the job when the queue is full yet returns true
  [internal/auth/delivery.go:57]
- [x] \[Review]\[Patch] Transient 4xx SMTP replies classified as definite rejection, invalidating a deliverable
  challenge [internal/provider/smtp/smtp.go:147]
- [x] \[Review]\[Patch] Spec-mandated tests missing: throttling, hidden states, expiry, password precedence,
  delivery outcomes, digest-only storage, audit rows, concurrent reset, SMTP failure modes
  [internal/auth/auth_test.go, internal/provider/smtp/smtp_test.go]
- [x] \[Review]\[Patch] Admit refusal during shutdown returns 500, breaking the uniform 204 recovery contract
  [internal/auth/auth.go:253]
- [x] \[Review]\[Patch] Reset throttling audits every denied request — unbounded audit growth
  [internal/auth/auth.go:278]
- [x] \[Review]\[Patch] beforeExpiry swallows persisted expires_at parse errors as invalid token
  [internal/auth/auth.go:359]
- [x] \[Review]\[Patch] SMTP deferred connection close has a dead no-op conditional body
  [internal/provider/smtp/smtp.go:43]
- [x] \[Review]\[Patch] Delivery-outcome persistence failure logged without the error value
  [internal/auth/delivery.go:97]
- [x] \[Review]\[Patch] Recovery email lacks RFC 5322 Date and Message-ID headers
  [internal/provider/smtp/smtp.go:109]
- [x] \[Review]\[Patch] Argon2 hashing runs before the trivial confirmation-equality check
  [internal/auth/auth.go:306]
- [x] \[Review]\[Patch] Limiter.record discards Transitioned on the BlockAtLimit early return
  [internal/httpserver/limit.go:128]
- [x] \[Review]\[Patch] Dead exported Server.CheckRecovery is never called
  [internal/httpserver/httpserver.go:90]
- [x] \[Review]\[Patch] Stale "Register attaches" doc comment dangles above recoveryMailer
  [internal/auth/auth.go:30]
- [x] \[Review]\[Patch] Docs gaps: cleartext AUTH PLAIN over plaintext transport undocumented; api.md "malformed"
  wording ambiguous; token-remains-usable-after-invalid-password and reset-throttle response undocumented
  [docs/server-configuration.md, docs/api.md]
- [x] \[Review]\[Defer] Expired/consumed password_reset_challenges rows are never pruned [migrations/000003] —
  deferred, retention of operational data is the documented product baseline; pruning needs a product decision

## Design Notes

SMTP delivery occurs after creating the challenge so a timeout can leave that challenge usable as required. A definite
SMTP failure must invalidate only the newly created challenge. The adapter returns a small classified error surface;
raw SMTP exchanges, addresses beyond the allowed recipient, and message content never escape it.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: all changed Go files are formatted.
- `go test ./...` -- expected: unit and SQLite integration tests pass without external delivery.
- `go vet ./...` -- expected: no vet findings.
- `golangci-lint run ./...` -- expected: no lint findings.
