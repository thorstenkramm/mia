---
title: 'First auth slice with user package and bootstrap-admin'
type: 'feature'
created: '2026-08-29'
status: 'in-progress'
baseline_commit: 'd0cc6db'
review_loop_iteration: 0
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
  - '{project-root}/_bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/prd.md'
  - '{project-root}/_bmad-output/planning-artifacts/architecture/architecture-mia-2026-08-29/ARCHITECTURE-SPINE.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** MIA has kernel infrastructure but no accounts, credentials, local first-administrator setup, or browser
authentication. The first secure vertical slice must establish the account ownership and stateless-session rules that
subsequent invitation, course, and MFA work will rely on.

**Approach:** Add `user`, `auth`, and `audit` packages with their owned SQLite tables and public transaction APIs;
wire the local bootstrap command and minimal JSON:API login, logout, and forced-password-change routes into the
existing command and HTTP kernels.

## Boundaries & Constraints

**Always:** Preserve AD-1 through AD-13: `user` owns user and role records plus account security state; `auth` owns
credential-flow data; all audited mutations write their content-free audit entry in the same transaction. Passwords
are UTF-8, 12–128 Unicode code points and at most 512 bytes, remain unmodified, use the bundled common-password
blocklist, and are stored only with fixed-parameter Argon2id. Sessions are one signed-and-encrypted CookieStore
cookie; each authenticated request reloads account state and validates the security generation. Successful login,
logout, and all stage transitions rotate CSRF state. Document the supplied auth API contract in `docs/api.md`.

**Ask First:** Halt for any behavior outside login/logout/password-change/bootstrap, including MFA implementation,
password recovery, invitations, staff role grants, student provisioning, or new profile-editing behavior.

**Never:** Do not add a public signup/bootstrap route, store server-side browser-session records, log or audit
passwords, hashes, cookies, credential values, or profile data unnecessarily, accept bootstrap account values from
flags/environment/files/redirected input, or alter the supervisor-managed sprint status record.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Bootstrap | TTY, validated fields, no admin | Create verified-email administrator and audit event atomically | Reject non-TTY, invalid input, or an existing admin without writes |
| Login | Valid password, no MFA, no password gate | Set authenticated cookie and return safe auth-session resource | Invalid credentials or ban return `401 invalid_credentials` |
| Password gate | Valid temporary-password login | Set `password-change` stage; only replacement and logout are available | Other protected access returns `403 password_change_required` |
| Replacement | Password-change cookie and matching valid password fields | Clear gate, rotate to fresh authenticated session, return safe resource | Invalid/mismatched policy input returns `422 invalid_password` |
| Logout | Valid login-stage cookie | Clear browser cookie and return `204` | Missing, invalid, or expired cookie returns `401 unauthenticated` |

</frozen-after-approval>

## Code Map

- `cmd/mia/main.go` -- Cobra root wiring currently exposes unavailable offline placeholders; replace only the
  bootstrap placeholder and keep command-level lock/config/database lifecycle consistent with `serve`.
- `internal/config/config.go` -- `Load(flags, false)` validates only the required offline configuration subset,
  appropriate for bootstrap; do not require providers or document root.
- `internal/sqlite/sqlite.go` and `migrations/` -- existing embedded migration runner and shared `Querier`/`WithTx`
  contract are the sole persistence integration points.
- `internal/httpserver/httpserver.go` -- owns CookieStore and CSRF middleware; extend through an injected identity
  loader and authenticated route registration rather than per-handler cookie parsing.
- `internal/httpserver/errors.go` -- central registry/mapping must receive the slice's stable auth error codes.
- `internal/identity/identity.go` -- reuse username, email, language, country, and time-zone validation; add normal
  password validation without mutating password input.
- `docs/api.md` -- has bootstrap contract at lines 152–163 and route list/stage rules at lines 182–219; add the
  authorized JSON:API request/response/error shapes for this slice.

## Tasks & Acceptance

**Execution:**
- [ ] `migrations/000002_auth_and_users.up.sql` -- create owned `users`, `user_roles`, credential, and audit tables
  with normalized keys, state constraints, and required indexes.
- [ ] `internal/identity/` -- add Argon2id password hashing, verification, and embedded common-password policy.
- [ ] `internal/user/` -- implement the one account-creation API, idempotent role grant, account-state loading, and
  password-gate update through the shared SQLite querier.
- [ ] `internal/audit/` -- write bounded, content-free same-transaction events for bootstrap and auth actions.
- [ ] `internal/auth/` -- implement password login, restricted-stage selection, password replacement, logout, and
  HTTP handlers with the authorized JSON:API shapes.
- [ ] `internal/httpserver/` -- expose mandatory authenticated-route registration, stage enforcement, session/CSRF
  rotation, and central errors required by auth without permitting other features to register restricted stages.
- [ ] `cmd/mia/main.go` -- implement TTY-only interactive bootstrap using the user/audit APIs inside one transaction.
- [ ] `docs/api.md` -- document only the approved auth endpoint contract and its safe fields/error outcomes.
- [ ] `cmd/mia`, `internal/auth`, `internal/user`, `internal/identity`, `internal/httpserver`, and `internal/audit`
  tests -- cover policy boundaries, atomic bootstrap preconditions, state reloading/stages, session expiry,
  unauthorized routes, cookie/CSRF rotation, and safe API responses.

**Acceptance Criteria:**
- Given a local terminal and no administrator, when bootstrap fields and twice-entered valid password are supplied,
  then one verified-email administrator and one local-operator audit event commit together.
- Given valid credentials, when a non-banned account logs in, then the cookie and returned resource contain only the
  current permitted stage and the authenticated stage has 30-minute idle and 12-hour absolute lifetimes.
- Given a password-gated account, when it logs in and then submits a compliant matching replacement, then no route
  other than logout/replacement is available before replacement and the final authenticated lifetime begins after it.
- Given invalid credentials, a banned account, malformed input, or throttling, when login is attempted, then the
  documented indistinguishable/status-specific central JSON:API error is returned without sensitive data.

## Design Notes

The auth package may create the `mfa` cookie stage and opaque challenge identifier from existing durable MFA state,
but this story must not implement factor enrollment or verification. The user state loader remains the authority for
the account's current password gate and security generation, so old cookies cannot bypass later state changes.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: all changed Go sources are formatted.
- `go test ./...` -- expected: all unit and SQLite integration tests pass.
- `go vet ./...` -- expected: no vet findings.
- `golangci-lint run ./...` -- expected: no lint findings.
- `go test -race ./...` -- expected: session and route concurrency checks are race-free.
