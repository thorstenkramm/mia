---
title: 'Zero-Tolerance JSCPD Policy'
type: 'chore'
created: '2026-09-03'
status: 'done'
review_loop_iteration: 0
baseline_commit: '3804f13ff5fbea742fdf4b67ebd2a294d11bb076'
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

| Scenario      | Input / State                       | Expected Output / Behavior                                  | Error Handling                                   |
|---------------|-------------------------------------|-------------------------------------------------------------|--------------------------------------------------|
| Clone scan    | Existing Go and markup sources      | Exact JSCPD command finds zero clones                       | Command exits successfully                       |
| Distinct code | Parallel security or provider flows | Narrow marker covers only detected sequence                 | Comment records separate ownership               |
| Shared code   | Same concrete responsibility        | One minimal implementation replaces the duplicated sequence | Preserve current error and transaction semantics |

</frozen-after-approval>

## Code Map

- `run-all-tests.sh:12-15` -- already invokes zero-threshold JSCPD and includes `.agents/**`; retain both in the project
  check.
- `cmd/mia/main.go:56-140` -- bootstrap and sole-administrator MFA commands duplicate offline lock/database setup;
  determine whether a command-local helper can preserve their intentionally different terminal flows.
- `internal/auth/auth.go:481-591,595-649,670-749,851-1017,1019-1119` -- parallel MFA verification, resend, and atomic
  TOTP consumption flows; factor only transaction-safe shared behavior with one responsibility, otherwise mark distinct
  challenge/enrollment/proof protocols.
- `internal/auth/delivery.go:72-84` and `internal/invitation/delivery.go:89-102` -- parallel lifecycle cleanup under
  different delivery contracts; check a marker rather than exporting a cross-package worker abstraction.
- `internal/course/course.go:1120-1228` and `internal/material/store.go:368-384` -- duplicated storage instant parsing
  and file mutation shapes; shared protocol instant formatter/parser already belongs to `internal/httpserver`, while
  course logos and material persistence retain separate domain ownership.
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

## Recurrence

This policy has been applied twice. The first pass ran against baseline `22f05b6` and cleared the 29 clone ranges the
Code Map above describes. Stories 1-10 through 1-13 then introduced 10 new clone ranges without re-running the gate, so
the check was failing at `3804f13` before the second pass. `baseline_commit` now points at that second baseline; the
first pass remains recorded in the Code Map and the first execution list.

The recurrence showed the gate is only as good as its invocation. Nothing in the repository ran it automatically, and
`run-all-tests.sh` carries it alone while no CI configuration exists. See the deferred CI item in `deferred-work.md`.

A second, quieter failure mode surfaced during the second pass: a `jscpd:ignore-start` without its matching
`jscpd:ignore-end` silently suppresses duplication detection to the end of that file and still exits successfully. A
dropped marker is therefore indistinguishable from a clean result, so marker balance is now checked before the scan.

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

**Execution, second pass (baseline `3804f13`, resolved in `1c5d5a4`):**

- [x] `internal/sqlite/scan.go` -- own the single-column string scan, replacing eleven loops in `internal/course`,
  `internal/material`, `internal/speech`, and `internal/tutoring` that had grown three different row-closing spellings.
  `closeRows` stays for multi-column scans, which also own per-row decoding.
- [x] `internal/material/material.go` and `internal/tutoring/store.go` -- take a condition with arguments for material
  deletion, matching the existing `deleteSessions` shape, and call the adjacent `scanResponse` that `loadResponse`
  duplicated.
- [x] `internal/httpserver/response.go` and `internal/httpserver/decode.go` -- own paginated collection rendering and
  tri-state string attribute decoding, leaving loading, authorization, error translation, and the domain optional types
  with the packages that own them.
- [x] `internal/mentoring/handler.go` -- share the authenticate and decode preamble between session create and update
  while each mutation keeps its own attribute invariants and denial audit.
- [x] `internal/jobs/jobs.go` -- own the guarded-commit rule for handler commits. The two call sites disagreed, one
  reporting a `RowsAffected` failure as a stale lease and discarding the result; the shared guard keeps that error.
- [x] `internal/tutoring/handler.go` and `internal/material/jobs.go` -- mark the two clones that stay by intent, one
  narrow marker per pair with a note on both sides.
- [x] `run-all-tests.sh` -- fail on unbalanced duplication markers before the scan runs.

**Acceptance Criteria:**

- Given the exact user-provided JSCPD command, when run from the repository root, then it exits successfully at
  threshold zero with no reported clones.
- Given a deliberate parallel implementation, when it remains in source, then a recognized, smallest-range marker and
  nearby rationale explain the distinct protocol, security, or domain ownership.
- Given the dirty JSON:API worktree, when this policy is applied, then it remains preserved and no commit is created.
- Given the complete test script and Go checks, when source validation tools are available, then each finishes without a
  new source failure; unavailable external tooling is reported separately.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: changed Go sources use standard formatting.
- The zero-threshold JSCPD command in `run-all-tests.sh` -- expected: no clones and successful exit.
- `./run-all-tests.sh` -- expected: all included checks pass, or any unavailable external tool is identified separately.
- `go test ./...` -- expected: successful package tests.
- `go vet ./...` -- expected: no diagnostics.
- `golangci-lint run ./...` -- expected: no diagnostics.
