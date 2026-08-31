---
title: 'Invitations and direct role grants'
type: 'feature'
created: '2026-08-31'
status: 'blocked'
baseline_revision: '799f3b54288d9ba6fdd1a42eaf61de8fe7dff668'
review_loop_iteration: 0
followup_review_recommended: false
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
  - '{project-root}/_bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/prd.md'
  - '{project-root}/_bmad-output/planning-artifacts/architecture/architecture-mia-2026-08-29/ARCHITECTURE-SPINE.md'
warnings: []
deferred: []
---

<intent-contract>

## Intent

**Problem:** MIA has no invitation vertical slice, so staff cannot register through the required invitation-only flow
or manage invitation delivery lifecycle. Direct role grants exist as a persistence function but have no authorized
HTTP operation or audit trail.

**Approach:** Add the `invitation` package, its owned migration and routes, and reuse `user.Create` and
`user.GrantRole` inside invitation-owned transactions. Add invitation-specific SMTP delivery, public token flows,
management authorization, lifecycle auditing, rate limiting, and role-grant route registration.

## Boundaries & Constraints

**Always:** Persist only SHA-256 invitation-token digests; generate canonical lowercase UUID v4 tokens; link using
the configured HTTPS public URL and a fragment. A pending invitation is the only public-valid state. Keep
revoked/faulty/accepted/unknown public tokens indistinguishable. Create and accept invitations, rotate on resend,
and make definite SMTP rejection terminally faulty atomically with audit records; SMTP timeout/ambiguous outcome
leaves it pending and is logged/audited without automatic retry. An accepted supervisor gets supervisor and student
roles atomically. Use the single `user.Create` and `user.GrantRole` APIs, and never issue user or role SQL from the
invitation package.

**Block If:** A required invitation response field, authorization boundary, or data lifecycle behavior is not
defined by FR-6..14, the current API/database drafts, or the architecture spine.

**Never:** Add public sign-up, course/student scope to invitations, plaintext token storage or logs/audits,
invitation expiry, retry after delivery ambiguity, account creation outside `user.Create`, or a direct first staff
role for student-only accounts. Do not expose invitation email, inviter identity, lifecycle state, or failure code
on public preview/acceptance.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Create staff invitation | Authorized actor, unused valid email, intended role | Pending invitation is stored, audited, and a fragment link is emailed | Reject already registered email and unauthorized role/actor |
| Public preview | Pending token | JSON:API resource says only MIA invitation and invited role | Non-pending, unknown, malformed, or rate-limited token returns generic invalid-invitation response |
| Accept invitation | Pending token and valid account profile | Atomically create verified staff account, consume invitation, and audit | Email conflict leaves invitation pending; all other non-pending cases are generic invalid |
| Delivery outcomes | SMTP rejection versus timeout/ambiguous error | Rejection faults and invalidates; timeout/ambiguous retains usable pending token | Record only safe lifecycle/failure metadata; never retry automatically |
| Resend / delete | Authorized pending or faulty invitation | Resend atomically rotates token; pending delete revokes; faulty delete removes row | Accepted/revoked/faulty resend and accepted/revoked delete reject without mutation |
| Direct grant | Authorized actor grants permitted role to verified staff account | Idempotently grants role and audits; supervisor also gains student | Reject banned, unverified, student-only, unknown, and unauthorized targets without existence disclosure |

</intent-contract>

## Code Map

- `internal/user/user.go` -- owns the only account creation and idempotent role-grant APIs; extend only where
  invitation-route actor/target state cannot be read without bypassing its ownership.
- `internal/auth/auth.go` -- provides strict JSON:API decoding and resource helpers; keep invitation parsing local
  unless a safe shared exported helper is required.
- `internal/auth/delivery.go` -- bounded asynchronous SMTP delivery pattern and terminal failure handling to mirror
  for invitations without conflating recovery challenges.
- `internal/provider/smtp/smtp.go` -- add an English plain-text invitation message while preserving SMTP error
  classification and sanitized headers.
- `internal/httpserver/httpserver.go` -- authenticated routes currently support POST/DELETE only; add only the
  registrar capabilities required by documented invitation GET routes and normal protected grant POST routes.
- `internal/httpserver/limit.go` -- already owns invitation IP/token limits; expose a combined public check and keep
  these public routes out of generic semantics only if the generic rate-limit response would reveal state.
- `internal/httpserver/errors.go` -- add registered invitation and role-grant codes, including one generic public
  invalid-invitation code, mapped exclusively by the central error handler.
- `internal/audit/audit.go` -- register content-free invitation lifecycle and direct role-grant actions.
- `migrations/000005_invitations.up.sql` -- create the invitation-owned table using the documented columns,
  constraints, pending-token uniqueness, and foreign-key lifecycle.
- `migrations/migrations.go` -- embed the new forward-only migration.
- `cmd/mia/main.go` -- wire the invitation package and its delivery manager beside auth and SMTP without feature SQL.
- `docs/api.md`, `docs/database-layout.md` -- read-only route/schema behavior evidence; update only if the concrete
  slice expands an affected human-readable contract.

## Tasks & Acceptance

**Execution:**
- `migrations/000005_invitations.up.sql`, `migrations/migrations.go` -- add the invitation persistence schema and
  embed it -- enforce digest-only pending tokens and lifecycle states durably.
- `internal/invitation/*.go` -- implement invitation creation, scoped management reads/actions, public preview and
  acceptance, delivery manager, resend rotation, and lifecycle transitions -- keep transactions and authorization in
  the owning vertical slice.
- `internal/provider/smtp/smtp.go` -- send English plain-text invitation mail containing only the role and fragment
  link -- reuse classified SMTP errors.
- `internal/httpserver/{httpserver,limit,errors}.go` -- add required authenticated registration methods, invitation
  public limit checks, and central error codes -- preserve centralized session, limiter, and error behavior.
- `internal/audit/audit.go`, `internal/user/user.go` -- register lifecycle/grant audit actions and expose only
  ownership-preserving account state needed by the new routes.
- `cmd/mia/main.go` -- construct and close invitation delivery work and register all invitation/direct-grant routes.
- `internal/invitation/*_test.go`, `internal/user/user_test.go`, `internal/provider/smtp/smtp_test.go` -- test the
  matrix, authorization scope, atomic consumption/rotation/faulting, digest-only storage, and message safety.

**Acceptance Criteria:**
- Given an authorized administrator or supervisor, when they create an allowed invitation for an unused email, then
  MIA stores a pending digest-only invitation, audits it, and delivers a fragment-bearing link without exposing a
  plaintext token in storage, logs, or audit records.
- Given a valid pending token, when a public caller previews it, then the response identifies MIA and only the invited
  role; when the token is malformed, unknown, accepted, revoked, or faulty, then every response is the same generic
  invalid-invitation result.
- Given a pending invitation and valid acceptance profile, when acceptance succeeds, then one transaction creates a
  verified account with exactly the invited role (plus student for supervisor), consumes the invitation, and audits
  the transition; an email conflict does not consume it.
- Given classified SMTP delivery failures, when the server receives a definite rejection, then the invitation becomes
  faulty and tokenless; when it receives timeout or ambiguity, then it stays pending with no automatic retry.
- Given an authorized manager, when they resend a pending invitation, then the token rotates atomically; when they
  delete pending or faulty state, then it respectively revokes or removes it, while accepted/revoked deletion fails
  unchanged.
- Given a verified registered staff target, when an administrator grants administrator/supervisor or a supervisor
  grants mentor, then the role applies immediately and idempotently, supervisor includes student, and the action is
  audited; banned, unverified, student-only, unknown, and unauthorized requests are rejected.

## Design Notes

Invitation delivery must be asynchronous like password recovery so request admission is bounded and SMTP ambiguity
does not fabricate a result. The delivery worker owns only the post-send state transition; the original creation or
resend transaction persists the pending invitation and token digest before it is admitted.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: all changed Go source is formatted.
- `go test ./...` -- expected: all package and integration tests pass.
- `go vet ./...` -- expected: no vet findings.
- `golangci-lint run ./...` -- expected: no lint findings.
- `go test -race ./...` -- expected: delivery-manager concurrency checks pass without races.

## Auto Run Result

Status: blocked

Blocking condition: no subagents
