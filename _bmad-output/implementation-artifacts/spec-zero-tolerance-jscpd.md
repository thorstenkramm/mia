---
title: 'Zero-Tolerance JSCPD Policy'
type: 'chore'
created: '2026-09-03'
status: 'in-progress'
review_loop_iteration: 0
baseline_commit: '22f05b671a543bf242afb1dde0eaa058cec9fe8e'
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/.agents/rules/golang.md'
  - '{project-root}/.agents/rules/json-api.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The zero-threshold JSCPD command reports 29 Go clone ranges across the current MIA backend. The project
needs a maintainable policy that detects future duplication without forcing unrelated domain and security behavior into
unsafe abstractions.

**Approach:** Assess every reported clone for an existing shared responsibility. Refactor only genuine common behavior;
otherwise, use the smallest supported JSCPD ignore range and an immediately adjacent comment that states why the code
must retain separate ownership.

## Boundaries & Constraints

**Always:** Preserve all pre-existing uncommitted JSON:API alignment work; retain the exact JSCPD options and
`--threshold 0`; run formatting and requested verification sequentially; use only recognized `jscpd:ignore-start` and
`jscpd:ignore-end` ranges; place explanatory comments at every ignored clone range.

**Ask First:** Stop rather than ignore a clone that indicates a behavioral defect requiring a product decision or a
refactor that changes an authorization, security, or lifecycle contract.

**Never:** Increase JSCPD's threshold; add whole-file ignores outside generated or third-party code; create `util` or
`misc` packages; add speculative common abstractions; commit any change; rewrite unrelated JSON:API work.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Clone scan | Existing Go and markup sources | Exact JSCPD command finds zero clones | Command exits successfully |
| Distinct code | Parallel security or provider flows | Narrow marker covers only detected sequence | Comment records separate ownership |
| Shared code | Same concrete responsibility | One minimal implementation replaces the duplicated sequence | Preserve current error and transaction semantics |

</frozen-after-approval>

## Code Map

- `run-all-tests.sh:12-15` -- already invokes zero-threshold JSCPD and includes `.agents/**`; retain both in the
  project check.
- `cmd/mia/main.go:56-140` -- bootstrap and sole-administrator MFA commands duplicate offline lock/database setup;
  determine whether a command-local helper can preserve their intentionally different terminal flows.
- `internal/auth/auth.go:481-591,595-649,670-749,851-1017,1019-1119` -- parallel MFA verification, resend, and
  atomic TOTP consumption flows; factor only transaction-safe shared behavior with one responsibility, otherwise mark
  distinct challenge/enrollment/proof protocols.
- `internal/auth/delivery.go:72-84` and `internal/invitation/delivery.go:89-102` -- parallel lifecycle cleanup under
  different delivery contracts; check a marker rather than exporting a cross-package worker abstraction.
- `internal/course/course.go:1120-1228` and `internal/material/store.go:368-384` -- duplicated storage instant
  parsing and file mutation shapes; shared protocol instant formatter/parser already belongs to `internal/httpserver`,
  while course logos and material persistence retain separate domain ownership.
- `internal/course/handler.go`, `internal/material/handler.go`, `internal/user/handler.go`, and
  `internal/jobs/oversight.go` -- JSON:API resource construction and handler code must use existing
  `internal/httpserver` protocol helpers when possible, without moving resource-specific authorization or attributes.
- `internal/httpserver/httpserver.go:138-165` -- route setup repetition is owned by the shared HTTP layer and is a
  candidate for a local constructor/helper.
- `internal/invitation/invitation.go:560-613` -- similar invitation lifecycle operations need assessment for shared
  pending-state ownership.
- `internal/provider/mistral/mistral.go`, `internal/provider/openai/openai.go`, and `internal/provider/smtp/smtp.go`
  -- distinct provider protocols and SMTP message classes; preserve boundary-specific request and error behavior with
  narrow markers where no common provider contract exists.
- `internal/user/profile.go:377-434` -- similar profile mutation flows; assess whether shared profile persistence
  responsibility already exists.

## Tasks & Acceptance

**Execution:**
- [x] `cmd/mia/main.go` -- remove or narrowly mark duplicated command setup after preserving distinct offline command
  authorization and terminal behavior.
- [x] `internal/auth/auth.go`, `internal/auth/delivery.go`, `internal/invitation/handler.go`, and
  `internal/invitation/delivery.go` -- refactor only reusable auth behavior; mark separately owned invitation and MFA
  protocols where appropriate.
- [x] `internal/course/course.go`, `internal/course/handler.go`, `internal/material/store.go`,
  `internal/material/handler.go`, `internal/user/handler.go`, and `internal/jobs/oversight.go` -- use existing shared
  HTTP protocol helpers where that is their concrete ownership; otherwise add narrow documented ignores.
- [x] `internal/httpserver/httpserver.go`, `internal/invitation/invitation.go`, `internal/provider/mistral/mistral.go`,
  `internal/provider/openai/openai.go`, `internal/provider/smtp/smtp.go`, and `internal/user/profile.go` -- resolve the
  remaining reported clone families with minimal local refactors or documented marker ranges.
- [x] `run-all-tests.sh` -- ensure the script's JSCPD invocation ignores `.agents/**` and retains `--threshold 0`.
- [x] Changed Go files -- run `gofmt` before sequential verification.

**Acceptance Criteria:**
- Given the exact user-provided JSCPD command, when run from the repository root, then it exits successfully at
  threshold zero with no reported clones.
- Given a deliberate parallel implementation, when it remains in source, then a recognized, smallest-range marker and
  nearby rationale explain the distinct protocol, security, or domain ownership.
- Given the dirty JSON:API worktree, when this policy is applied, then it remains preserved and no commit is created.
- Given the complete test script and Go checks, when source validation tools are available, then each finishes without
  a new source failure; unavailable external tooling is reported separately.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: changed Go sources use standard formatting.
- `npx jscpd --min-lines 10 --min-tokens 50 --threshold 0 --reporters console --no-tips --ignore "**/*_test.go,**/vendor/**,**/_bamd/**,.cache/**,_bmad-output/**,.agents/**" --format go,markup .` -- expected: no clones and successful exit.
- `./run-all-tests.sh` -- expected: all included checks pass, or any unavailable external tool is identified separately.
- `go test ./...` -- expected: successful package tests.
- `go vet ./...` -- expected: no diagnostics.
- `golangci-lint run ./...` -- expected: no diagnostics.
