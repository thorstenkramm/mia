---
title: 'Discover and Extend Browser Sessions Safely'
type: 'feature'
created: '2026-09-13'
status: 'done'
review_loop_iteration: 1
baseline_commit: '8abd6de'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The browser cannot discover authoritative anonymous or staged session state, ordinary authenticated
requests silently extend idle lifetime, and delayed responses require explicit logout and reconciliation semantics.

**Approach:** Add session discovery and one explicit Continue working operation, make all other access non-refreshing,
and bind authentication cookies to an independently signed per-browser generation marker rotated by logout. Protect
against delayed responses that do not reissue the marker and reconcile delayed authentication transitions.

## Boundaries & Constraints

**Always:** Reload account security state for authenticated discovery and access; expose exact stage, applicable MFA
challenge ID, and server-authored UTC idle and absolute deadlines. Rotate session and CSRF values only at documented
authentication transitions, and rotate the browser marker on logout so delayed ordinary and Continue working cookies
are unusable. Reconcile a delayed authentication transition through discovery. Apply CSRF, same-origin, public
limiting, JSON:API, no-store, and restricted-stage rules consistently.

**Ask First:** Any server-side browser-session record, cross-device logout, background refresh, or change to the
fixed 30-minute idle and 12-hour absolute limits.

**Never:** Refresh idle expiry from reads, ordinary mutations, SSE establishment/reconnect/traffic, or lifecycle checks;
reissue the marker from ordinary activity, SSE, or Continue working; claim universal logout ordering; expose factor
secrets or destinations; reconnect SSE from Continue working; trust account state from cookies; or reveal why stale
authentication became invalid.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Discovery | No usable auth cookie | Anonymous state and usable public CSRF | Clear stale auth uniformly |
| Staged discovery | Valid MFA, password-change, or full session | Exact stage and unchanged UTC deadlines | MFA exposes challenge ID only |
| Continue working | Valid session before expiry | Advance idle only and return rotated auth cookie | Expired/invalid is uniformly unauthenticated |
| Concurrent logout | Delayed ordinary or Continue response | Rotated marker rejects its auth cookie | Other browsers remain valid |
| Transition race | Delayed login, MFA completion, or password change | Matching pair may restore; discovery reconciles | No durable ordering claim |

</frozen-after-approval>

## Code Map

- `internal/httpserver/httpserver.go` -- central cookie validation, identity reload, deadline headers, explicit refresh,
  and signed browser-generation lifecycle; remove the current success callback that refreshes every request.
- `internal/httpserver/routes.go` -- reuse public and authenticated route classifications; auth alone retains staged
  route registration.
- `internal/auth/auth.go` -- register discovery and Continue working handlers and return complete auth-session resources.
- `internal/auth/auth_test.go`, `internal/httpserver/httpserver_test.go` -- replace implicit-refresh expectations and
  cover stages, expiry, cookie/CSRF rotation, SSE-equivalent non-refresh, retained ordinary-response protection, and
  authentication-transition reconciliation.
- `api-doc/openapi.yaml`, `api-doc/paths/auth.yaml`, `api-doc/schemas/resources/resources.yaml` -- define both operations,
  deadline headers/attributes, anonymous document, security, limits, and stable errors.
- `docs/api.md`, `_bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/prd.md` -- align the human-readable session
  contract with the resolved explicit-refresh decision recorded in the epic.

## Tasks & Acceptance

**Execution:**
- [x] `internal/httpserver` and `internal/auth` -- implement discovery, explicit extension, non-refreshing access, and
  logout-safe browser generation binding.
- [x] `internal/**/*_test.go` -- adapt cookie fixtures and exercise authoritative state and response-ordering boundaries.
- [x] `api-doc/` and session documentation -- publish the complete browser contract and resolved FR-22 behavior.

**Acceptance Criteria:**
- Given no valid cookie, when session discovery runs, then it returns anonymous state, refreshes public CSRF, and leaks
  no account existence.
- Given any valid stage, when discovery or another authenticated response succeeds, then exact unchanged deadlines and
  only stage-applicable metadata are returned.
- Given explicit Continue working before expiry, when accepted, then only idle expiry advances and no SSE action occurs.
- Given logout races an earlier authenticated response, when responses arrive in either order, then the old session is
  unusable when the delayed response does not issue a matching marker, while another browser remains valid.
- Given logout races an admitted authentication transition, when the transition response arrives last, then its
  matching cookie pair may restore authentication and session discovery returns that authoritative state.

## Spec Change Log

- 2026-09-13: Implemented and reviewed the approved session discovery, continuation, non-refresh, and browser-generation
  contract.

## Review Triage Log

### 2026-09-13 -- Review pass

- [x] [Review][Patch] Truncate newly issued deadlines to persisted whole-second precision so discovery returns the exact
  deadline originally sent at login or continuation.
- [x] [Review][Patch] Preserve the browser marker in the expired-session regression test so it exercises timer expiry
  rather than failing early on a missing marker.
- [x] [Review][Patch] Remove the obsolete request-context logout flag left after automatic refresh was removed.
- [x] [Review][Patch] Persist the browser-generation marker through the bound authentication lifetime and cover browser
  restart without allowing ordinary requests or Continue working to refresh it.
- [x] [Review][Decision] Apply the approved reduced MVP guarantee: ordinary and Continue working responses remain
  protected, while delayed authentication transitions may restore state and require discovery reconciliation.

## Design Notes

The browser marker is independent cookie state signed with the session key. Each auth cookie binds its marker
generation. Authentication transitions persist the marker through the bound session's absolute lifetime. Logout
rotates the marker in the current browser; normal and Continue working responses never reissue it, so a late
pre-logout auth cookie from those operations cannot match the browser's post-logout marker. A delayed login,
MFA-completion, or password-change response reissues a matching pair and may restore authentication. This is reconciled
through session discovery; the MVP adds no server-side browser-session or per-browser revocation record.

## Verification

**Commands:**
- `gofmt` on changed Go files -- expected: no formatting diff.
- `./run-all-tests.sh` -- expected: all Go, race, duplication, vulnerability, OpenAPI, and Markdown checks pass.

**Result:** `gofmt`, `go test ./...`, `go vet ./...`, and `golangci-lint run ./...` passed. The complete
`./run-all-tests.sh` gate passed, including race tests, duplication checks, Trivy, Redocly, and Markdown lint.
