---
title: 'Enable local HTTP browser E2E authentication'
type: 'feature'
created: '2026-09-12'
status: 'done'
review_loop_iteration: 0
baseline_commit: '00bf7a3a1759d4b9f03e839618730dc6cba18ac9'
context:
  - 'AGENTS.md'
  - '.agents/rules/echo.md'
  - '.agents/rules/json-api.md'
  - 'docs/server-configuration.md'
---

<frozen-after-approval reason="human-owned intent - do not modify unless human renegotiates">

## Intent

**Problem:** Browser and cookie-jar E2E clients cannot use MIA through an allowed loopback HTTP origin because the
server always emits secure `__Host-` session and CSRF cookies. As a result, unsafe API requests fail CSRF validation
and authenticated test flows cannot run.

**Approach:** Derive a local-only cookie policy from the existing validated loopback-HTTP `main.public_url`. Retain
the production HTTPS cookie policy and CSRF validation unchanged, while local HTTP uses valid non-secure cookie names
and is prevented from binding a remotely reachable listener.

## Boundaries & Constraints

**Always:** Preserve the production `__Host-mia_session` and `__Host-mia_csrf` names and all their current flags.
Preserve double-submit CSRF matching, Fetch Metadata rejection, CSRF rotation, API-path scope, and rate limiting before
CSRF. Treat the public URL-derived policy as immutable server wiring rather than request-derived behavior. Keep local
HTTP restricted to loopback public origins and loopback TCP listeners.

**Ask First:** Any request to add an operator-controlled insecure-cookie switch, accept a remote or reverse-proxied
local-HTTP listener, enable CORS, alter the CSRF validation algorithm, or support a production non-HTTPS origin.

**Never:** Do not implement a header-only CSRF fallback, test-only endpoint, environment override, TLS listener, or
session-security configuration key. Do not log cookies or CSRF tokens. Do not relax existing handler authorization or
session-expiry behavior.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Production HTTPS | HTTPS public URL | `__Host-` session and CSRF cookies remain `Secure` | Existing behavior |
| Local E2E HTTP | Loopback HTTP public URL and loopback listener | `mia_session` and `mia_csrf` omit `Secure`; cookie jar completes double-submit CSRF | Existing CSRF `403` on absent/mismatched token |
| Unsafe local setup | Loopback HTTP public URL with wildcard, remote TCP, or Unix listener | Startup configuration is rejected | Clear validation error without starting |
| Cross-site unsafe request | Valid local or production CSRF token with `Sec-Fetch-Site: cross-site` | CSRF remains rejected | Existing `csrf_invalid` / `403` |

</frozen-after-approval>

## Code Map

- `internal/config/config.go` -- validates and normalizes `main.public_url`; add the derived cookie-policy input and
  reject non-loopback or Unix listeners when the normalized public URL is loopback HTTP.
- `internal/config/config_test.go` -- extend public-origin/listener coverage for loopback HTTP and unchanged HTTPS
  listener forms.
- `cmd/mia/main.go` -- construct the HTTP server with the validated, immutable transport/cookie policy during `serve`
  wiring; never derive it from a request or forwarded header.
- `internal/httpserver/httpserver.go` -- owns Gorilla CookieStore, session load/save/clear, `RotateCSRF`, and custom
  CSRF middleware. Centralize active cookie names and flags so all session and CSRF operations use the same policy.
- `internal/httpserver/httpserver_test.go` -- retain middleware-order coverage and add policy-level cookie attribute
  tests plus a real `net/http/cookiejar` local-HTTP flow.
- `internal/auth/auth.go` -- replace the duplicated hard-coded session cookie name and invoke the policy-aware server
  CSRF rotation path at auth-stage transitions.
- `internal/auth/auth_test.go` and feature handler test fixtures under `internal/*/*_test.go` -- use server-owned
  cookie names rather than literals when manually issuing sessions and CSRF state.
- `docs/server-configuration.md`, `mia.example.toml`, `docs/api.md`, `docs/architecture.md`, and
  `api-doc/openapi.yaml` -- document the constrained loopback HTTP mode, production defaults, and actual custom CSRF
  implementation without adding a configuration setting.

## Tasks & Acceptance

**Execution:**
- [x] `internal/config/config.go` and `internal/config/config_test.go` -- derive and validate the local-only cookie
  policy from the normalized public URL and listener -- prevent an insecure browser session from becoming remotely
  reachable.
- [x] `cmd/mia/main.go` and `internal/httpserver/httpserver.go` -- pass and own one immutable cookie policy, then use
  it consistently for session read/write/clear and CSRF issue/rotation -- ensure names and flags cannot drift.
- [x] `internal/auth/auth.go` and affected handler test fixtures -- remove duplicated session-cookie knowledge and use
  the kernel policy -- keep auth transitions and focused handler tests correct under both policies.
- [x] `internal/httpserver/httpserver_test.go` and `internal/auth/auth_test.go` -- add table-driven cookie-policy
  coverage and real cookie-jar HTTP regression coverage -- prove browser delivery works without weakening CSRF.
- [x] `docs/server-configuration.md`, `mia.example.toml`, `docs/api.md`, `docs/architecture.md`, and
  `api-doc/openapi.yaml` -- document behavior and test configuration accurately -- make the local exception safe for
  operators and test authors.

**Acceptance Criteria:**
- Given an HTTPS `main.public_url`, when MIA starts, then it emits only the existing `__Host-` session and CSRF cookie
  names with their existing `Secure`, path, host-only, SameSite, and HttpOnly properties.
- Given a loopback HTTP public URL and loopback TCP listener, when an HTTP cookie jar obtains CSRF state and sends its
  `mia_csrf` value in `X-CSRF-Token`, then an otherwise authorized unsafe API request succeeds.
- Given either policy, when the CSRF token is absent, mismatched, duplicated, or sent with cross-site Fetch Metadata,
  then the request remains rejected with `csrf_invalid` and HTTP `403`.
- Given a loopback HTTP public URL and a wildcard, non-loopback, or Unix listener, when configuration is validated,
  then `serve` refuses to start; HTTPS public URLs retain their current listener compatibility.
- Given login, logout, password replacement, and MFA stage transitions, when each rotates CSRF state, then the active
  policy's CSRF cookie name and flags are used.

## Design Notes

`__Host-` is a browser-enforced secure-cookie contract, not merely a naming convention. Local HTTP must therefore
switch both names and flags. The public URL is already validated to allow HTTP only for loopback origins, making it a
safer source of truth than a broadly applicable opt-out setting. The listener restriction completes that boundary.

## Verification

**Commands:**
- `gofmt -w cmd/mia/main.go internal/config/config.go internal/config/config_test.go internal/httpserver/httpserver.go internal/httpserver/httpserver_test.go internal/auth/auth.go internal/auth/auth_test.go` -- expected: changed Go files are formatted.
- `./run-all-tests.sh` -- expected: all repository Go, duplication, dependency, OpenAPI, and Markdown checks pass.

## Suggested Review Order

**Local-only security boundary**

- Derives cookie policy and rejects exposed or trusted-proxy loopback HTTP.
  [`config.go:411`](../../internal/config/config.go#L411)

- Passes validated policy unchanged into the shared HTTP kernel.
  [`main.go:528`](../../cmd/mia/main.go#L528)

**Cookie and CSRF behavior**

- Accepts only fixed production or local policy tuples in the HTTP kernel.
  [`httpserver.go:441`](../../internal/httpserver/httpserver.go#L441)

- Applies policy names and transport flags across sessions and CSRF issuance.
  [`httpserver.go:356`](../../internal/httpserver/httpserver.go#L356)

- Verifies real local HTTP cookie-jar authentication and policy-specific cookie lifecycle.
  [`httpserver_test.go:15`](../../internal/httpserver/httpserver_test.go#L15)

**Authentication and operator guidance**

- Uses server-owned cookie policy for authentication-stage CSRF rotation.
  [`auth.go:143`](../../internal/auth/auth.go#L143)

- Covers complete serve configuration and unsafe local-listener rejection.
  [`config_test.go:161`](../../internal/config/config_test.go#L161)

- Documents the local mode and its remaining operator security boundary.
  [`server-configuration.md:145`](../../docs/server-configuration.md#L145)
