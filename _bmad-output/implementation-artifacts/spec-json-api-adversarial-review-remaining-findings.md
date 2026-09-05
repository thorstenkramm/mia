---
title: 'Apply remaining JSON:API adversarial review findings'
type: 'bugfix'
created: '2026-09-05'
status: 'completed'
review_loop_iteration: 0
baseline_commit: '22f05b6a9398aeb0459b417b0868f81ccf39c380'
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/.agents/rules/json-api.md'
  - '{project-root}/tmp/json-api-allignment-plan.md'
---

<frozen-after-approval reason="human-owned intent - do not modify unless human renegotiates">

## Intent

**Problem:** The completed adversarial JSON:API review found remaining protocol, route-wiring, test, documentation,
and verification-script gaps after the already-completed OpenAPI and identity decisions.

**Approach:** Apply findings P3-P21 exactly as requested, preserving the approved dirty worktree and the D1/D2 and
P1/P2 decisions. Use shared-kernel behavior where protocol-wide and endpoint domain errors for supplied create IDs.

## Boundaries & Constraints

**Always:** Reject duplicate JSON members recursively; preserve body and Unicode bounds; keep unknown members at 400
and known invalid values at 422; test real route registrations; keep the OpenAPI 3.2 contract complete and strict;
run the requested checks sequentially; preserve unrelated worktree changes.

**Ask First:** None. The user pre-approved all checkpoints and edits and directed execution without confirmation.

**Never:** Commit, regress invitation/auth-session decisions, invent routes, weaken Redocly, accept last-wins JSON,
add broad authenticated rate limits, mutate files during verification, or replace unique product narrative with schema
duplication.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Duplicate JSON | Duplicate key at any object depth | Request rejected | 400 `malformed_request` |
| Accept list | Quoted comma in a parameter | Split only outside quotes | 406 only when all JSON:API instances are parameterized |
| Unknown route | Unknown path or method | Router result wins | Ordinary 404/405 before Accept |
| Create ID | Non-empty `data.id` on a create/command request | Endpoint rejects it | Documented domain 422 |
| Bounded files | Any query on material files collection | Request rejected | 400 `malformed_request` |
| Max offset | `offset=10000`, `hasMore=true` | Preserve prev, omit unusable next | No out-of-range link |

</frozen-after-approval>

## Code Map

- `internal/httpserver/decode.go`, `routes.go`, `limit.go`, `links.go` -- shared decoding, negotiation, precedence,
  route classification, limiting, and navigation behavior.
- `internal/{auth,invitation,user,course,material,jobs}` -- real route registrations, request resource validation,
  audit behavior, and representative family tests.
- `api-doc/` -- concrete OpenAPI 3.2 transport source of truth; create schemas already reject unknown properties but
  must explicitly forbid `id`.
- `.agents/rules/json-api.md`, `.agents/references/json-api-examples.md`, `docs/api.md` -- normative protocol and lean
  cross-route narrative.
- `run-all-tests.sh` -- non-mutating pinned verification entry point.

## Tasks & Acceptance

**Execution:**

- [x] Harden shared JSON decoding, Accept parsing/precedence, route representations, limiting, and pagination links.
- [x] Reject create IDs with endpoint 422 codes and reject all material-file collection query parameters.
- [x] Add focused kernel and representative real-family tests, including route limiting, non-JSON Accept exemptions,
  audit boundaries, 429 shape, and create-ID behavior.
- [x] Align OpenAPI, normative rules, examples, API narrative, and the verification script.
- [x] Run all user-requested checks sequentially and report every result.

**Acceptance Criteria:**

- Given each reviewed edge case, when exercised through the shared kernel or an actual feature registration, then the
  requested status, stable JSON:API shape, route precedence, representation, and limiter behavior are observable.
- Given the completed contracts, when documentation and tooling are linted, then Redocly remains recommended and
  strict, Markdown passes, and verification does not mutate the tree.

## Spec Change Log

- 2026-09-05: Implemented P3-P21 and completed all available verification gates. `govulncheck` was not installed.

## Verification

**Commands:**

- `gofmt`, `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...` -- Go quality gates.
- Pinned JSCPD, Redocly, Markdown lint, optional govulncheck, Trivy, `./run-all-tests.sh`, and `git diff --check` --
  repository quality and security gates.
