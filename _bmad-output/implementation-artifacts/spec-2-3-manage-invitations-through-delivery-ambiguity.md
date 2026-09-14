---
title: 'Manage Invitations Through Delivery Ambiguity'
type: 'feature'
created: '2026-09-14'
status: 'done'
review_loop_iteration: 0
baseline_commit: '4d9ef2cb4a8cf64346da961d40dec12b431fc829'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Authorized invitation managers cannot distinguish a queued, delivered, or ambiguous latest SMTP attempt,
and state-dependent DELETE can revoke or physically delete a different state from the one they reviewed.

**Approach:** Persist and expose the latest delivery outcome and attempt time, return a strong version validator on
authorized invitation representations, and require that validator for DELETE so authorization, state, and reviewed
intent are verified atomically. Return a resource identifying the committed DELETE effect.

## Boundaries & Constraints

**Always:** Keep timed-out or otherwise ambiguous delivery pending and usable with a sanitized stable code; make definite
failure faulty and invalidate its token; rotate the token before every resend; authorize reads and mutations through
scoped SQL; audit each committed transition in its transaction; apply CSRF, no-store, and central JSON:API errors.

**Ask First:** Any automatic SMTP retry, change to invitation-manager scope, new invitation lifecycle state, or
relaxation of strong `If-Match` binding.

**Never:** Persist or expose bearer tokens or digests; reveal whether missing and out-of-scope invitations differ;
allow a stale pending review to delete a newly faulty invitation or a stale faulty review to revoke another state; or
claim SMTP delivery when the outcome is uncertain.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Delivery timeout | Current token generation | Pending, usable, ambiguous code and attempt time | No retry |
| Definite failure | Current token generation | Faulty, token cleared, sanitized code and attempt time | Public token stays generic |
| Reviewed DELETE | Current strong ETag | Revoke pending or delete faulty and identify that effect | Atomic audit |
| Missing precondition | No `If-Match` | No mutation | `428 invitation_precondition_required` |
| Changed review | Stale or malformed `If-Match` | No mutation | `412 invitation_precondition_failed` |
| Hidden target | Missing or out of scope | Identical response | `404 invitation_not_found` |

</frozen-after-approval>

## Code Map

- `migrations/000017_invitation_delivery_state.up.sql` -- add checked latest-delivery state/code/attempt fields.
- `internal/invitation/invitation.go` -- invitation projection, scoped reads, guarded delivery commits, strong ETag, and
  atomic precondition-bound DELETE result.
- `internal/invitation/delivery.go` -- persist current-generation delivery outcomes without retries or stale writes.
- `internal/invitation/handler.go` -- expose delivery fields and ETags, parse `If-Match`, and return committed effect.
- `internal/httpserver/errors.go` -- register the two invitation precondition errors.
- `internal/invitation/invitation_test.go` -- HTTP, authorization, concurrency, delivery, audit, and token regressions.
- `api-doc/paths/invitations.yaml`, `api-doc/schemas/resources/resources.yaml` -- publish ETag, If-Match, delivery fields,
  DELETE result, and stable errors.
- `docs/api.md`, `docs/database-layout.md`, `docs/product-requirements.md` -- align browser and persistence contracts.

## Tasks & Acceptance

**Execution:**
- [x] Add persisted latest-attempt state and generation-guarded outcome commits for create and resend delivery.
- [x] Bind DELETE to the reviewed strong ETag and return the exact retained-revoked or physically-deleted effect.
- [x] Cover delivery ambiguity, token rotation, stale concurrent transitions, authorization hiding, audit, and transport.
- [x] Publish the complete OpenAPI and human-readable invitation contract.

**Acceptance Criteria:**
- Given a current delivery attempt times out, when an authorized manager rereads it, then it remains pending and usable
  with an ambiguous delivery code and authoritative UTC attempt time, without automatic retry.
- Given a definite current-generation failure, when committed, then the invitation is faulty, its token is unusable, and
  authorized reads expose only sanitized failure and timing.
- Given a manager reviewed pending or faulty state, when DELETE carries its current ETag, then exactly the reviewed revoke
  or physical-delete effect commits atomically and the response identifies it.
- Given absent, malformed, or stale `If-Match`, when DELETE is evaluated, then no state changes and the documented 428 or
  412 error is returned without weakening existence hiding.

## Spec Change Log

## Design Notes

The strong validator derives from all persisted invitation representation and effect-driving fields, including token
generation and latest delivery state. DELETE checks the scoped row and validator inside one SQLite write transaction;
the reviewed ETag therefore binds both authorization-visible state and whether DELETE means revoke or physical delete.

## Verification

**Commands:**
- `gofmt` on changed Go files -- passed.
- `go test ./internal/invitation ./internal/httpserver ./internal/sqlite` -- passed.
- `./run-all-tests.sh` -- passed Go tests, vet, golangci-lint, race tests, duplication, Trivy, Redocly, and Markdown.
  `govulncheck` was not installed, so the script skipped that optional check.

## Suggested Review Order

**Reviewed mutation boundary**

- Start with atomic authorization, state, validator, and effect selection.
  [`invitation.go:299`](../../internal/invitation/invitation.go#L299)

- See transport-level precondition errors, denied audits, and committed-effect responses.
  [`handler.go:123`](../../internal/invitation/handler.go#L123)

- Confirm strong validators cover every versioned invitation mutation.
  [`invitation.go:709`](../../internal/invitation/invitation.go#L709)

**Delivery reconciliation**

- Follow SMTP outcomes into generation-guarded authoritative state commits.
  [`delivery.go:102`](../../internal/invitation/delivery.go#L102)

- Review ambiguity persistence without token invalidation or stale writes.
  [`invitation.go:559`](../../internal/invitation/invitation.go#L559)

- Verify schema backfill and terminal delivery-state immutability.
  [`000017_invitation_delivery_state.up.sql:1`](../../migrations/000017_invitation_delivery_state.up.sql#L1)

**Public contract and regressions**

- Review ETag, If-Match, delivery reconciliation, and DELETE response documentation.
  [`invitations.yaml:91`](../../api-doc/paths/invitations.yaml#L91)

- Confirm timeout reconciliation and token usability through the authorized API.
  [`invitation_test.go:1673`](../../internal/invitation/invitation_test.go#L1673)

- Confirm missing, malformed, stale, audited, and successful precondition behavior.
  [`invitation_test.go:1725`](../../internal/invitation/invitation_test.go#L1725)

- Confirm stale pending review cannot become a faulty physical deletion.
  [`invitation_test.go:1779`](../../internal/invitation/invitation_test.go#L1779)
