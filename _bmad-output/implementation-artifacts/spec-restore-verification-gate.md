---
title: 'Restore verification after frontend request status updates'
type: 'chore'
created: '2026-10-18'
status: 'done'
baseline_commit: '70c7a524b309054cdcaf831537d2b641eb1fa65a'
review_loop_iteration: 0
context: []
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Verification of the frontend request status changes exposed an existing malformed Markdown table and
nine reachable Go standard-library vulnerability findings under Go 1.27.0. The full gate stops at govulncheck.

**Approach:** Prefer patched Go 1.27.2 for this module and fix the request's table formatting, then rerun the full
gate without suppressing findings. Go 1.27.2 is available locally and is the scanner's stated fixed version.

## Boundaries & Constraints

**Always:** Preserve the frontend requests' status/spec mapping and substantive content, including the literal
`/JS ` marker's trailing space. Keep the documented minimum language version at Go 1.27.0 and use the module's
toolchain preference for patched builds. Preserve the unrelated untracked authentication/invitation document.

**Ask First:** Any remaining failure requiring unrelated application, dependency, or policy changes.

**Never:** Do not suppress vulnerability findings, weaken gates, change global Go settings, or push automatically.

</frozen-after-approval>

## Code Map

- `go.mod:3` — currently `go 1.27.0`, no toolchain preference; GOTOOLCHAIN is auto.
- `docs/architecture.md:12` — minimum Go version; add the distinction between minimum and preferred patched toolchain.
- `tmp/frontend-request-relaxed-pdf-validation-2026-10-18.md:25-32` — oversized misaligned diagnostic table.
  This file is ignored by Git but must still be edited and explicitly linted.
- `run-all-tests.sh:54-73` — govulncheck fails before Trivy, API lint and Markdown; keep this gate unchanged.

## Tasks & Acceptance

**Execution:**
- [x] `go.mod` — add `toolchain go1.27.2` without changing dependency versions or the minimum Go directive.
- [x] `docs/architecture.md` — document patched toolchain preference for development and verification.
- [x] PDF frontend request — use a compact table plus separate explanatory text; retain every diagnostic fact.
- [x] All three frontend requests — explicitly lint and verify their front matter remains unchanged.
- [x] Run the full gate and record its result; investigate any remaining findings within scope.

**Acceptance Criteria:**
- Given default automatic toolchain selection, when `go version` runs in the module, then it selects Go 1.27.2 or newer.
- Given the frontend request documents, when explicitly linted, then all pass without changing their status mappings.
- Given the project, when `./run-all-tests.sh` runs, then every component passes, or a remaining blocker is reported.

## Spec Change Log

## Verification

- `go version` — `go version go1.27.2 darwin/arm64`.
- `npx --yes markdownlint-cli@0.49.1 --fix docs/architecture.md tmp/frontend-request-*.md` — pass.
- All three frontend request front-matter blocks match their pre-edit values exactly; both backlog files are
  byte-for-byte unchanged. The PDF request retains the literal `/JS ` marker in a fenced text block.
- The unrelated untracked authentication/invitation document is byte-for-byte unchanged.
- `./run-all-tests.sh` — passed every component: gofmt, Go tests, vet, golangci-lint, race tests,
  duplication marker balance, JSCPD, govulncheck, Trivy, Redocly API lint, and Markdown lint.
  govulncheck reports zero reachable vulnerabilities and one non-called module-level vulnerability.
  Trivy reports zero vulnerabilities under the existing ignore policy; no suppression or gate was changed.
- `git diff --check` — no whitespace errors.

## Suggested Review Order

- Select the patched toolchain while retaining the minimum language version.
  [`go.mod:3`](../../go.mod#L3)
- Explain the module's development and verification toolchain preference.
  [`architecture.md:12`](../../docs/architecture.md#L12)
- Review the compact diagnostic table and preserved literal hazard markers.
  [`frontend-request-relaxed-pdf-validation-2026-10-18.md:25`](../../tmp/frontend-request-relaxed-pdf-validation-2026-10-18.md#L25)

The frontend request files remain Git-ignored local artifacts; their edits are not included in the implementation commit.
