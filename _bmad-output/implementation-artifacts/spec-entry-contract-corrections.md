---
title: 'Correct and verify the existing entry contracts'
type: 'bugfix'
created: '2026-09-27'
status: 'done'
baseline_commit: '0626b751558c8baa7e4645708ef588b309446ed8'
review_loop_iteration: 0
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/.agents/rules/json-api.md'
  - '{project-root}/.agents/rules/echo.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The frontend correction request identifies incomplete MFA schema constraints, missing discovery CSRF
errors, inaccurate rotation wording, and missing validated examples and evidence for the existing entry contract.
All findings are confirmed against the baseline; middleware behavior need not change.

**Approach:** Correct shared schemas and maintained descriptions, add synthetic entry examples and executable schema
and HTTP regression tests, then provide an evidence handoff to the frontend. Keep the accepted Home gate unchanged.

## Boundaries & Constraints

**Always:** Require a non-empty opaque challenge identity for MFA responses and forbid its presence in non-MFA
response variants. Reuse the established ResourceID string/minLength contract. Preserve all four stage shapes,
deadlines, action catalog, scope isolation, non-refreshing discovery, JSON:API errors, and no-store behavior.
Document duplicate CSRF-cookie rejection on both discovery GETs without requiring a header or body.
Anonymous discovery rotates; valid-stage discovery retains an existing CSRF cookie; middleware issues one when absent.
Old-header/new-cookie mismatch fails on unsafe requests; rotation does not revoke every previously matching pair.

**Ask First:** Changes to runtime authorization, cookie behavior, identifiers, or accepted entry policy.

**Approved exception:** Repair malformed browser-generation-cookie recovery so discovery returns anonymous with a
fresh marker instead of failing while decoding the invalid marker again. Preserve strict authentication validation,
valid marker reuse, cookie policy, persistence-error handling, and the reduced stateless logout guarantee.

**Never:** Add an endpoint, Home capability, redirect, pagination redesign, stronger logout/CSRF revocation, or SSE
changes. Do not modify frontend routing or claim backend tests establish browser acceptance or close all frontend gaps.
Never print cookie values or credentials in test diagnostics. No push is included.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| MFA schema | Non-empty bound ID | Accepted in shared auth and discovery documents | Missing, empty, null or wrong-type ID rejected |
| Other stages | Anonymous, password-change, authenticated | Existing shapes accepted | Non-MFA auth response with challenge rejected |
| Duplicate CSRF | Two configured-name request cookies on either GET | JSON:API denial without protected data | 403 csrf_invalid, subject to earlier middleware |
| Anonymous CSRF | Existing or missing cookie | Handler rotates; missing also triggers middleware issuance | Tests use effective last-issued cookie |
| Valid-stage CSRF | Existing or missing cookie | Retain existing; issue when missing | No CSRF header required for GET |
| Unsafe token check | Old header with new cookie | Reject mismatch | Prior matching pair is not revoked by rotation alone |
| Authentication discovery | Missing or invalid authentication | Uniform anonymous result | Dependency failure remains an error |
| Capability discovery | Restricted, empty, assigned or failed | Existing denial/scope contracts maintained | Error contains no partial scope |

</frozen-after-approval>

## Code Map

- `api-doc/schemas/resources/resources.yaml:269,300,1912` -- AuthSession, discovery union, shared auth document;
  conditional attributes validation reaches all consumers. `api-doc/openapi.yaml:235` owns ResourceID's non-empty
  opaque string schema. Do not infer a UUID or prefix constraint from implementation-generated IDs.
- `api-doc/paths/auth.yaml:31,486`, `api-doc/paths/users.yaml:50` -- discovery operations and shared Session response;
  add named response examples and existing Forbidden references. `api-doc/responses/errors.yaml:27` owns JSON:API 403.
- `internal/httpserver/httpserver.go:770,802,209` -- CSRF middleware, cookie issuance, RotateCSRF comment.
  Duplicate-cookie detection precedes safe-method bypass; middleware has no token revocation state.
- `internal/auth/auth.go:474,507` -- anonymous discovery rotation and stage-specific serialization; read-only behavior.
  `docs/api.md:142–147` incorrectly claims unconditional refresh and immediate old-token invalidation.
- `internal/auth/auth_test.go:349–441,991,1009` -- existing stage and MFA lifecycle coverage. `serve` automatically
  pairs cookie/header; build explicit requests for duplicate/mismatch tests. Cookie helpers take the first response
  cookie, but anonymous missing-cookie discovery sets two; use the effective last value without logging it.
- `internal/capability/capability_test.go:82–239` -- empty-account, restricted-stage, mentor-pair/removal and partial
  failure evidence. Strengthen action-name completeness rather than duplicating existing scope tests.
- `internal/httpserver/httpserver_test.go:97` -- existing unsafe-CSRF coverage, reusable evidence.
- `run-all-tests.sh` -- already runs all Go tests and pinned Redocly lint; no executable schema regression suite exists.
  `go.mod` already includes YAML v3 indirectly. Add a test-only Draft 2020-12 validator dependency to integrate schema
  tests into existing Go checks rather than relying on machine-specific Python or npm-cache internals.

## Tasks & Acceptance

**Execution:**
- [x] `internal/httpserver/httpserver.go`, `internal/auth/auth_test.go` -- issue a fresh replacement browser marker
  without decoding malformed incoming marker data; verify anonymous recovery and subsequent usable discovery.
- [x] `api-doc/schemas/resources/resources.yaml` -- conditionally require non-empty MFA identity and exclude it from
  other auth stages, reusing ResourceID; preserve unrelated schema permissiveness.
- [x] `api-doc/paths/auth.yaml`, `api-doc/paths/users.yaml` -- document 403 csrf_invalid, issuance/retention/rotation,
  and stage reconciliation. Capability's shared restricted-stage code cannot distinguish MFA from password replacement.
  Add four discovery examples and full single-role, multi-role, empty-assignment capability examples with synthetic IDs.
- [x] `docs/api.md`, `internal/httpserver/httpserver.go` -- correct CSRF narrative and misleading RotateCSRF comment;
  distinguish cookie replacement from server-side revocation. Runtime middleware remains unchanged.
- [x] `api-doc/entry_contract_test.go`, `go.mod`, `go.sum` -- validate actual local YAML schemas/examples with a pinned
  `github.com/santhosh-tekuri/jsonschema/v6` test import and existing YAML v3. Resolve local references only, reject
  network loads, enable format assertions, distinguish schema compilation failure from expected instance invalidity.
  Cover both shared auth and discovery positive/negative MFA variants and validate complete capability examples.
- [x] `internal/auth/auth_test.go`, `internal/capability/capability_test.go` -- cover the CSRF matrix on real discovery
  routes, stale-header rejection, discovery-specific invalid authentication and dependency failure as needed;
  reuse sufficient existing evidence and assert the complete expected action set. Never dump token values.
- [x] `docs/entry-contract-evidence.md`, `docs/README.md` -- concise guarantee-to-test mapping and index link, including
  schema cases; reference OpenAPI as transport owner rather than duplicating it.
- [x] `../mia-frontend/tmp/backend-entry-contract-corrections-response.md` -- handoff with contract references, evidence
  mapping, actual gate results/skips, and resulting commit ID after a separately authorized commit. Until then state
  uncommitted explicitly. Preserve frontend request files and distinguish backend tests from browser evidence.

**Acceptance Criteria:**
- Given valid and invalid stage documents, when the real schema validator runs, then valid shapes pass and missing or
  forbidden challenge identities fail across shared authentication and discovery schemas.
- Given existing middleware behavior, when either discovery GET is exercised, then tests and documented CSRF outcomes
  agree without requiring a GET header or implying server-side token revocation.
- Given the frontend request, when its owner follows the handoff, then each entry guarantee has executable backend
  evidence, validated examples, and explicit remaining frontend and deferred-scope boundaries.

## Verification

- Format changed Go files with `gofmt`.
- Run `./run-all-tests.sh`, including new schema tests through `go test ./...`; report every skipped or failing component.
- Verify examples directly from OpenAPI, not separately maintained fixture copies; never contact external providers.
- Keep the frontend handoff outside the backend commit. No frontend application or browser behavior is changed.

## Implementation verification

- Implementation and independent review complete; uncommitted. No commit or push performed.
- Approved marker repair uses fresh CookieStore session state for marker persistence, preserving strict validation,
  reuse of valid generations, configured cookie attributes, save-error propagation, and stateless logout semantics.
- Additional persistence-failure regression: `TestBrowserMarkerSaveFailureIsReturned` in
  `internal/httpserver/httpserver_test.go`.
- Every edge-case matrix row has passing executable evidence mapped in
  [Entry contract evidence](../../docs/entry-contract-evidence.md).
- Final backend `./run-all-tests.sh` completed on 2026-09-27: formatting, Go tests, vet, golangci-lint, race tests,
  duplication marker balance, JSCPD, Trivy, Redocly, and Markdown passed. Redocly reported no warnings;
  golangci-lint reported zero issues and JSCPD zero clones. Optional `govulncheck` was skipped (not installed).
  Trivy reported no unsuppressed findings using existing repository suppressions.
- Frontend handoff was independently Markdown-linted. The existing frontend full suite also passed, including
  30 unit tests and 5 Chromium scaffold tests, with no skipped components. This does not establish backend-integrated
  entry acceptance or close the frontend gaps identified in the handoff.

## Review outcome

- Completed blind, edge-case, and verification-gap review layers.
- Removed shared examples advertising impossible stage outcomes for some authentication operations.
- Corrected malformed-authentication test setup to reach decode failure with a valid browser marker.
- Strengthened local-only schema reference tests and valid-stage marker/deadline preservation assertions.
- Re-ran the authoritative suite successfully after all review patches; govulncheck remains unavailable.

## Suggested Review Order

**Cookie recovery and contract**

- Creates fresh marker state without decoding invalid input again.
  [`httpserver.go:555`](../../internal/httpserver/httpserver.go#L555)
- Documents discovery errors, CSRF behavior, and stage examples.
  [`auth.yaml:31`](../../api-doc/paths/auth.yaml#L31)
- Defines the shared authentication stage schema.
  [`resources.yaml:269`](../../api-doc/schemas/resources/resources.yaml#L269)

**Verification and handoff evidence**

- Validates actual local schemas and examples, including negative MFA cases.
  [`entry_contract_test.go:1`](../../api-doc/entry_contract_test.go#L1)
- Maps each entry guarantee to executable evidence and records verification limits.
  [`entry-contract-evidence.md:1`](../../docs/entry-contract-evidence.md#L1)
