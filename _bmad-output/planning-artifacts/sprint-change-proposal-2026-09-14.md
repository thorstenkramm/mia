---
title: "Sprint Change Proposal: Defer Late Authentication-Transition Logout Race"
status: approved
created: 2026-09-14
affected_epic: epic-2
affected_story: 2-2-discover-and-extend-browser-sessions-safely
---

# Sprint Change Proposal: Defer Late Authentication-Transition Logout Race

## Issue Summary

Story 2.2 review found that a browser can apply a delayed pre-logout login, MFA-completion, or password-change
response after logout. That response reissues both the stateless authentication cookie and the matching signed
browser-generation marker, so it restores usable authentication in that browser.

The current requirement that logout defeats every older in-flight response is not achievable with a stateless cookie
and no server-side per-browser revocation record. Browser cookie application has no ordering primitive that lets a
logout response reject a later-applied matching cookie pair.

The product owner chose to preserve the stateless-session MVP and defer this race rather than add a durable
per-browser revocation system.

## Impact Analysis

### Epic and Story Impact

- Epic 2 remains viable. Its browser-ready workflows continue to require authoritative discovery, explicit idle
  extension, non-refreshing reads and SSE, CSRF rotation, and scoped session handling.
- Story 2.2 must be rescaled. It keeps protection against delayed ordinary authenticated and Continue working
  responses, which do not reissue a browser-generation marker.
- Story 2.2 no longer claims deterministic logout-wins ordering for delayed login, MFA-completion, or
  password-change responses. The browser must not treat a local logout as irrevocable while one of those requests is
  in flight.
- Stories 2.3 through 2.12 are unaffected. They retain their order and existing acceptance criteria.

### Contract Impact

- FR-25 in the adopted PRD and `docs/product-requirements.md` currently promises that an older in-flight response
  cannot restore authentication. This must be narrowed.
- Epic 2's resolved transport decision, Story 2.2 acceptance criteria, and the frozen Story 2.2 artifact repeat the
  same unachievable guarantee and must be aligned.
- `docs/api.md`, `api-doc/openapi.yaml`, and `api-doc/paths/auth.yaml` must document the reduced browser contract
  and safe frontend behavior for an in-flight authentication transition at logout.
- `docs/architecture.md` and `docs/server-configuration.md` must remove any universal late-response claim while
  retaining the stateless-session and no-server-revocation invariants.
- Story 2.2 tests must cover the retained ordinary-response protection and explicitly stop asserting that a delayed
  authentication transition becomes unusable after logout.
- No database schema, session store, provider, deployment, or frontend UI change is required. Frontend state-machine
  documentation needs only the updated transport caveat when the backend contract is published.

## Options Considered

### Option A: Durable Per-Browser Revocation

Add a server-side browser-session or revoked-generation record and consult it on every request.

- Effort: high.
- Risk: high. This reverses the MVP's stateless-session exclusion, adds migrations, record expiry and cleanup,
  per-request storage availability, new authorization and concurrency behavior, and a new operational data class.
- Decision: rejected for the MVP.

### Option B: Defer the Late Authentication-Transition Race

Retain the signed stateless auth cookie and browser-generation marker. Narrow the logout ordering guarantee and track
a future design that supplies a durable ordering or revocation primitive.

- Effort: low.
- Risk: medium. A delayed authentication-transition response can restore that browser's authenticated state until
  its normal idle, absolute, account-state, or security-generation invalidation takes effect.
- Decision: selected.

## Reduced MVP Guarantee

Logout clears the current browser's authentication cookie and rotates its signed browser-generation marker. A delayed
response that does not issue a new matching marker, including an ordinary authenticated response or Continue working,
cannot restore usable authentication after logout.

This guarantee does not apply to a delayed login, MFA-completion, or password-change response that was admitted before
logout and issues a fresh authentication cookie with a matching marker. Such a response may restore authentication in
that browser. MIA still does not revoke other browsers, create server-side browser-session records, or extend the
session's idle or absolute deadlines through ordinary activity.

The frontend must treat an attempted logout as incomplete until no authentication-transition request is outstanding;
it must reconcile any later session response through session discovery rather than assuming local logout prevailed.

## Detailed Change Proposals

### Product Requirements

**Files:**
`_bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/prd.md` FR-25 and
`docs/product-requirements.md` Authentication session lifetime.

**Old:** Logout rotates the browser generation so an older in-flight response cannot restore authentication.

**New:** Logout rotates the browser generation so delayed ordinary authenticated and Continue working responses cannot
restore authentication. A delayed pre-logout login, MFA-completion, or password-change response may restore the current
browser because the MVP has no server-side per-browser revocation state. Clients reconcile this outcome through session

### Architecture and API Contract

**Files:** `docs/architecture.md`, `docs/server-configuration.md`, `docs/api.md`, `api-doc/openapi.yaml`, and
`api-doc/paths/auth.yaml`.

**Old:** The browser-generation marker makes every pre-logout authentication cookie unusable.

**New:** The marker invalidates delayed responses that do not issue a new matching marker. Authentication transitions
can reissue a matching cookie pair, so clients must not infer durable logout ordering against an in-flight transition.
The OpenAPI contract documents session-discovery reconciliation after logout and an ambiguous transition response.

### Epic and Story Artifacts

**Files:** `_bmad-output/planning-artifacts/epics.md`,
`_bmad-output/implementation-artifacts/spec-2-2-discover-and-extend-browser-sessions-safely.md`, and
`_bmad-output/specs/spec-mia/stories.yaml` if its Story 2.2 description needs clarification.

**Old:** Story 2.2 requires no pre-logout response to restore usable authentication.

**New:** Story 2.2 requires protection against delayed ordinary and Continue working responses, plus documented
reconciliation for delayed authentication transitions. It does not require durable ordering between logout and a
previously admitted authentication transition.

### Tests

**Files:** Story 2.2 session and HTTP tests.

**Old:** Reordered logout tests assert that all pre-logout responses, including authentication transitions, cannot
restore usable authentication.

**New:** Retain regression coverage for late ordinary and Continue working responses. Replace transition-race rejection
assertions with the documented reconciliation behavior and ensure no test implies a server-side revocation record.

## Backlog Item

**ID:** `auth-durable-logout-ordering`

**Title:** Make logout deterministically win over delayed authentication transitions.

**Problem:** Stateless cookies cannot order a logout response against a previously admitted response that can issue a
new matching auth-cookie and browser-marker pair.

**Future acceptance criteria:** The chosen design makes a confirmed logout in one browser reject any delayed login,
MFA-completion, or password-change response from before that logout, without revoking other browsers. It defines durable
record lifetime, cleanup, per-request behavior, outage handling, CSRF and cookie transitions, data-retention rules,
concurrency tests, and migration/operational consequences before implementation.

**Status:** backlog. It must not be implemented during the MVP without a separate approved architecture and product
decision.

## Implementation Handoff

**Scope classification:** Moderate. The code is substantially complete, but binding product, transport, and planning
contracts need a coordinated re-scope before the Story 2.2 review can pass.

**Owner:** Epic Worker updates the listed contracts and focused Story 2.2 tests. Epic Reviewer verifies that the
reduced guarantee is consistent across requirements, OpenAPI, documentation, and implementation. Epic Solver resumes
Story 2.2 from review only after those updates pass the full project check.

**Success criteria:**

- All affected authoritative and human-readable contracts state the same reduced guarantee.
- The tracked backlog item is visible in sprint status without changing Epic 2 ordering.
- Story 2.2 tests prove retained ordinary-response protection and reconciliation of delayed transition responses.
- `./run-all-tests.sh` passes and Story 2.2 passes independent review.

## Approval Required

Approval authorizes the contract and test changes above, creation of the backlog item, and resumption of Story 2.2.
It does not authorize server-side browser-session records or remote session revocation.

**Approved:** 2026-09-14 by Productowner.
