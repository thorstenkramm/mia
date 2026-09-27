---
title: 'Clarify session discovery and authenticated Home entry contracts'
type: 'feature'
created: '2026-09-25'
status: 'done'
baseline_commit: '92837424379c59cc8b2eab6b7c532708faff845e'
review_loop_iteration: 0
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/.agents/rules/json-api.md'
  - '{project-root}/.agents/rules/echo.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Frontend requests session-stage discovery and a complete current-account scope contract before mounting
Home. Both discovery endpoints already exist, but the Home eligibility rule and the interpretation of scopes are not
explicit enough to implement the ordered entry gate without assumptions.

**Approach:** Reuse `GET /api/v1/auth/session` and `GET /api/v1/users/me/capabilities`. Document that every fully
authenticated account may mount the basic Home shell after successful current-account capability discovery, including
accounts with no assignments. Gate each protected section and dependent request by its relevant capability and scope;
each backend operation still independently authorizes its target. Strengthen regression coverage of these contracts.

## Boundaries & Constraints

**Always:** Session discovery chooses exactly one entry stage. MFA precedes mandatory password replacement. Restricted
stages render only their focused flow and logout. Successful capability discovery, rather than session discovery alone,
is required before Home mounts. Empty assignment scope permits an empty Home, not access to unrelated resources.
Capabilities describe current account scope, never roles alone or permanent authorization grants. Preserve the existing
response shapes, action catalog, statuses, non-refreshing behavior, and no-store policy. Document both GET operations'
media type, stage/access semantics, CSRF requirements, and safe failure handling in OpenAPI.

**Ask First:** Any new permission, endpoint, response field, action-specific authorization rule, session lifetime change,
or scope transport redesign. The user approved keeping both requested contracts in this one spec.

**Never:** Add `home.view`, a Home authorization endpoint, cookie inspection, denial probing, resource enumeration,
or frontend implementation. Do not treat mentor course/student pairs as independently combinable IDs. Do not infer
tutoring-start eligibility from membership alone. Do not use a failed capability read as permission to mount protected
content. Keep the complete unpaginated scope response for this task under the user's explicit temporary deferral of
the existing pagination-rule conflict; record bounded scope retrieval as follow-up, not as a permanent exemption.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Anonymous entry | Missing or invalid authentication | Discovery returns anonymous; no Home mount | Existing uniform anonymous response |
| Restricted entry | MFA or password-change stage | Focused entry flow; capabilities unavailable | Existing restricted-stage error |
| Empty Home | Full session, no assignments | Capability read succeeds; assignment scopes empty | No assignment-based Home denial |
| Assigned account | Full session and current assignments | Only current action scopes guide protected sections | Independent target authorization remains |
| Changed assignments | Assignment removed before next check | Next capability response excludes removed scope | No cached grant or existence probing |
| Discovery unavailable | Failed session or capability read | Neutral loading/error or sign-in state; no protected mount | Preserve existing status/error contract |

</frozen-after-approval>

## Code Map

- `internal/auth/auth.go:474–518` -- discovery and session serialization already implement the four-stage contract.
  Anonymous uses `data: null` with `meta.stage`; other stages use `data.attributes.stage`.
- `internal/httpserver/httpserver.go:312–331,450–503` -- non-refreshing discovery and current identity/stage validation;
  read-only security boundary. `internal/auth/auth_test.go:349–441` already covers stage discovery and reconciliation.
- `internal/capability/capability.go:45–117,129–157` -- transaction-backed 14-action projection, normalized empty arrays,
  full-session route. Preserve production behavior unless a concrete acceptance failure requires a scoped correction.
- `internal/course/course.go:106–142` -- complete course and shared-student scope loader, with no pagination.
- `internal/mentoring/service.go:43–71` -- real mentor pair loader; wire this into scope isolation tests.
- `internal/capability/capability_test.go` -- existing unioned-scope and non-refreshing route tests; extend existing
  fixtures and use real migrations rather than mocking away assignment authorization.
- `api-doc/paths/auth.yaml:31–52`, `api-doc/paths/users.yaml:50–68` -- existing operations; root path references are
  already present in `api-doc/openapi.yaml`. Clarify operation descriptions rather than duplicate routes.
- `api-doc/schemas/resources/resources.yaml:53–115,241–287` -- capability/scope and session discovery schemas.
  Clarify field meanings, complete scope, response-stage location, and account singleton identity.
- `docs/product-requirements.md:233–240`, `docs/api.md:365–377` -- directly affected human-readable Home/navigation
  contracts; product behavior belongs in the former and transport detail remains in OpenAPI.
- Frontend request files in `../mia-frontend/tmp/backend-contract-{account-scope,session-stage}-request.md` are read-only
  intent evidence. The frontend repository remains outside implementation scope.

## Tasks & Acceptance

**Execution:**
- [x] `docs/product-requirements.md`, `docs/api.md` -- document the ordered gate, empty Home, safe failure behavior,
  restricted stages, and independent section/target authorization consistently with the approved decision.
- [x] `api-doc/paths/auth.yaml`, `api-doc/paths/users.yaml` -- make existing discovery usage explicit, including GET
  CSRF requirements and Home eligibility, without introducing new transport or error semantics.
- [x] `api-doc/schemas/resources/resources.yaml` -- explain action availability and all scope discriminators, mentor
  pairing, own/global scope, absence representations, and session-stage extraction; preserve existing wire shapes.
- [x] `internal/capability/capability_test.go` -- cover successful unassigned-account discovery, denial for both
  restricted stages, exact mentor pair isolation and assignment removal on subsequent reads. Assert no unrelated
  scope leaks. Reuse existing non-refreshing and unioned-role coverage.
- [x] `_bmad-output/implementation-artifacts/deferred-work.md` -- append the explicitly approved bounded-scope follow-up,
  including inter-page assignment changes, frontend completeness, and current unbounded-array limitation.

**Acceptance Criteria:**
- Given the two frontend requests, when a client follows the documented existing endpoints, then it can select its
  entry flow and decide basic Home eligibility without cookie inspection, denial probing, or a new endpoint.
- Given a fully authenticated unassigned account, when capability discovery succeeds, then it receives explicit empty
  assignment scopes and can mount an empty Home without receiving unrelated protected data.
- Given restricted authentication or changed assignments, when capabilities are requested, then current backend
  authorization and scope boundaries hold and the documentation does not imply broader permission.

## Verification

**Commands:**
- `gofmt -w internal/capability/capability_test.go` -- changed Go is formatted.
- `./run-all-tests.sh` -- authoritative gate, including Go, race, duplication, security, OpenAPI and Markdown checks.

**Review:** Check both frontend requests against final OpenAPI descriptions and schemas. Report any unavailable check
explicitly. Verify the existing session tests cover all four stages and avoid duplicating tests of unchanged behavior.

## Implementation verification

- Updated the product, API narrative, and OpenAPI descriptions against both read-only frontend requests.
  Existing production behavior, action catalog, response shapes, and statuses required no correction.
- Added real-migration route coverage for unassigned student, mentor, supervisor, and administrator accounts;
  both restricted stages; exact mentor pairs with other-mentor cross-pairs excluded; subsequent assignment removal;
  and storage failure returning an error without partial scope or database details.
- Retained unioned-role and non-refreshing tests. Existing auth tests cover anonymous and password-change discovery
  (`TestSessionDiscoveryReturnsAnonymousAndAuthoritativeStages`), MFA (`TestMFADiscoveryReturnsOnlyOpaqueChallenge`),
  and authenticated discovery (`TestLogoutReorderingUsesReducedStatelessGuarantee`).
- `gofmt -w internal/capability/capability_test.go` and `go test ./internal/capability` passed.
- `./run-all-tests.sh` passed: Go tests, vet, golangci-lint, race tests, marker balance, JSCPD, Trivy,
  Redocly OpenAPI lint, and Markdown lint. Optional govulncheck was not installed and was skipped by the script.
- Frontend mounting behavior is documented, not browser-tested in this backend task. The approved unbounded scope
  limitation is recorded in deferred-work.md.

## Review outcome

- Completed blind, edge-case, and verification-gap review layers.
- Documented existing internal-error responses for both discovery operations.
- Added standalone MFA denial coverage, duplicate-action detection, and populated-scope failure assertions.
- Full project verification passed again after review patches; optional govulncheck remains unavailable.

## Suggested Review Order

**Entry contract**

- Defines Home eligibility and safe failure behavior without a new permission.
  [`product-requirements.md:241`](../../docs/product-requirements.md#L241)
- Explains stage selection and the transition to capability discovery.
  [`auth.yaml:31`](../../api-doc/paths/auth.yaml#L31)
- Defines successful capability discovery as the basic Home gate.
  [`users.yaml:50`](../../api-doc/paths/users.yaml#L50)
- Clarifies complete scopes and indivisible mentor assignments.
  [`resources.yaml:53`](../../api-doc/schemas/resources/resources.yaml#L53)

**Regression coverage**

- Tests empty scope, restricted stages, assignment isolation, removal, and failure without partial disclosure.
  [`capability_test.go:82`](../../internal/capability/capability_test.go#L82)
