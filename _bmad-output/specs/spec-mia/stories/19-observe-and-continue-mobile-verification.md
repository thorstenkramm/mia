---
title: 'Observe and Continue Mobile Verification'
type: 'feature'
created: '2026-09-14'
status: 'done'
review_loop_iteration: 3
followup_review_recommended: false
baseline_revision: 'bd45c04ebf26068098f266d0a989440a6faf8701'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** Staff cannot recover an authoritative mobile-change challenge after reload, distinguish resend cooldown from
durable quota exhaustion, or reconcile create, resend, and verification outcomes without relying on browser timing.

**Approach:** Add a current-account mobile-verification state projection and return that same secret-free projection from
challenge mutations. Extend the shared SMS gate to report exact safe eligibility timing while preserving the existing
code, expiry, failure count, and single provider-attempt semantics.

## Boundaries & Constraints

**Always:** Represent active, expired, invalidated, completed, absent, and provider-unavailable states from committed
state. Active state carries the server expiry and resend eligibility; cooldown carries `next_resend_at`; hourly/daily
exhaustion is distinct and returns `Retry-After` without naming the limiting dimension or threshold. Reads are
non-mutating and account-scoped. Resend reserves durable capacity before one provider call and preserves code, expiry,
and verification failures. Every instant uses shared UTC formatting and every API response remains `no-store`.

**Block If:** Implementation would require exposing a destination, code, failure count, limiter key/dimension, or defining
a new mobile-verification lifecycle beyond FR-35 and Story 2.6.

**Never:** Add cross-account challenge lookup, refresh expiry on reads or resends, reset failed submissions, automatically
retry ClickSend, infer provider success after failure, or use browser receipt time as a lifecycle deadline.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Reload | Latest challenge in any terminal or live state, or no row | One explicit secret-free lifecycle projection | Provider absence is explicit without deleting state |
| Cooldown | Live challenge sent less than 60 seconds ago | Resend unavailable with exact next instant | Resend returns stable `429` and `Retry-After`; no send |
| Quota exhausted | Account or destination hourly/daily capacity exhausted | Distinct rate-limited eligibility | Resend returns generic `429` and exact safe `Retry-After` |
| Eligible resend | Live challenge outside cooldown and quota | Existing code sent once; state remains active | Provider failure is safe `503`; state remains reconcilable |
| Concurrent transition | Read/resend/verify/replacement/invalidation races | Responses and reread reflect committed serialization | No expiry/failure reset, code replay, or duplicate resend send |

</intent-contract>

## Code Map

- `internal/user/profile.go` -- owns mobile challenge rows and transitions; add lifecycle projection, clock-controlled
  evaluation, serialized mutation/provider boundary, and equivalent mutation results.
- `internal/user/handler.go` -- register the authenticated current-state read, serialize projection attributes, return
  state from create/resend/verify, and map cooldown/quota timing through central errors.
- `internal/provider/sms/sms.go` -- replace boolean eligibility with cooldown-versus-quota projection and exact next
  permitted time while retaining one durable reservation source of truth.
- `internal/httpserver/errors.go` -- register the stable mobile cooldown code; retain generic rate-limited disclosure for
  hourly/daily capacity.
- `internal/user/profile_test.go`, `internal/user/handler_test.go`, `internal/provider/sms/sms_test.go` -- cover all
  lifecycle states, boundaries, redaction, preserved resend state, provider failures, and concurrency.
- `api-doc/openapi.yaml`, `api-doc/paths/users.yaml`, `api-doc/schemas/resources/resources.yaml` -- document the GET and
  equivalent mutation representations, exact attributes, security, errors, and `Retry-After` behavior.
- `docs/api.md`, `docs/database-layout.md` -- record browser reconciliation and authoritative resend-state semantics.

## Tasks & Acceptance

**Execution:**

- [x] `internal/provider/sms/sms.go` -- expose safe cooldown/quota eligibility and exact retry timing shared by reads and
  reservations.
- [x] `internal/user/profile.go` -- implement account-scoped lifecycle discovery and serialize mobile operations against
  concurrent transitions.
- [x] `internal/user/handler.go`, `internal/httpserver/errors.go` -- return equivalent lifecycle resources from create,
  resend, and verification and map stable cooldown/quota timing.
- [x] `internal/user/*_test.go`, `internal/provider/sms/sms_test.go` -- add deterministic lifecycle, concurrency,
  redaction, provider-failure, and durable-limit tests using local senders only.
- [x] `api-doc/**`, `docs/api.md`, `docs/database-layout.md` -- publish complete transport and lifecycle contracts.

**Acceptance Criteria:**

- [x] Given any latest mobile challenge or no challenge, when staff read current verification state, then active, expired,
  invalidated, completed, absent, or unavailable is explicit and no sensitive field is returned.
- [x] Given create, resend, or successful verification, when its admitted response is returned, then it has the same
  lifecycle representation as an immediate read and uses the original server-authored expiry.
- [x] Given cooldown or durable quota exhaustion, when state is read or resend is attempted, then the safe reason and
  authoritative retry timing are returned and no provider request occurs.
- [x] Given eligible resend, when it commits, then exactly one attempt is reserved and one existing-code send occurs while
  expiry and accumulated failures remain unchanged.
- [x] Given concurrent lifecycle operations, when they race, then committed state remains single-use and no race extends
  expiry, resets failures, restores stale state, or duplicates a resend provider call.
- [x] Given protocol, authorization, CSRF, rate-limit, provider, and redaction tests, when run, then all responses conform
  to the documented JSON:API and `no-store` contracts without cross-account disclosure.

## Spec Change Log

- 2026-09-14: Preserved mutation/read equivalence across the expiry boundary by requiring post-provider lifecycle
  evaluation to use a fresh clock while keeping the persisted expiry unchanged.
- 2026-09-14: Aligned create and resend success documentation with the post-delivery projection, including the permitted
  expired outcome and unchanged persisted expiry.

## Review Triage Log

### 2026-09-14 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 1: (high 0, medium 1, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[medium]` `[patch]` Documented the cooldown stable code and safe `Retry-After` behavior on challenge creation as
    well as resend, matching the implemented shared SMS gate.

### 2026-09-14 — Fix review pass

- intent_gap: 0
- bad_spec: 0
- patch: 1: (high 0, medium 1, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[medium]` `[patch]` Re-evaluated lifecycle with a fresh clock after SMS delivery and added a boundary test proving
    resend and immediate discovery both return expired when provider time crosses the original expiry.

### 2026-09-14 — Documentation fix review pass

- intent_gap: 0
- bad_spec: 0
- patch: 1: (high 0, medium 1, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[medium]` `[patch]` Revised create and resend success descriptions to promise the authoritative post-delivery
    projection, which may be expired without changing persisted expiry.

## Design Notes

The discovery resource is a per-account singleton projection identified by the authenticated account ID. Its nullable
`challenge_id` names the current persisted challenge for authorized follow-up commands without using a secret as
identity. The latest challenge row determines terminal history; an otherwise active row projects `unavailable` when SMS
is not configured. Resend eligibility is separate from lifecycle so terminal and provider-unavailable states cannot be
mistaken for quota denial.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: no formatting differences.
- `./run-all-tests.sh` -- expected: Go test, vet, lint, race, duplication, vulnerability, OpenAPI, and Markdown gates pass;
  optional unavailable tools are reported by the script.

## Auto Run Result

Status: done

Implemented a no-store current-account mobile-verification projection with explicit lifecycle and resend states. Create,
successful resend, and successful verification now return that projection. The shared durable SMS gate distinguishes
cooldown from hourly/daily capacity and supplies an exact safe retry instant without exposing limiter dimensions.
Serialized mobile mutations preserve the original code, expiry, and failure count and prevent duplicate concurrent
resend provider calls.

Changed `internal/user`, `internal/provider/sms`, and the central error registry; expanded service, handler, boundary,
redaction, provider-failure, and race coverage; and aligned OpenAPI plus browser/database lifecycle documentation.

Review findings: one medium documentation patch applied; no deferred or rejected findings. Follow-up review recommendation:
false (high 0, medium 1, low 0; score 3).

Verification: targeted Go tests passed. The final `./run-all-tests.sh` passed formatting, all Go tests, vet,
golangci-lint, race tests, duplication and marker checks, Trivy, Redocly, and Markdown lint. `govulncheck` was not
installed and was not run by the script.

Residual risks: none identified within Story 2.6 scope.

Fix verification: `go test ./internal/user` and `./run-all-tests.sh` passed after the post-provider clock correction.
The complete suite passed again after aligning the OpenAPI success descriptions with that behavior.
