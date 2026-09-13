---
title: 'Finalize frontend UX validation'
type: 'chore'
created: '2026-09-13'
status: 'done'
review_loop_iteration: 0
baseline_commit: '3297ff4044ff0a029652ca0acd57f6ceefbe7038'
context: []
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The MIA frontend UX handoff has been substantially validated, but two companion matrices still reference
the former 13-blocker backend ledger and the synthesized validation reports predate the latest contract corrections.
The package therefore cannot yet provide one coherent, current handoff to the frontend team.

**Approach:** Reconcile the matrices with the authoritative 15-blocker ledger, rerun all four validation lenses, fix
actionable contradictions in the UX package, regenerate both synthesized reports, and run the frontend quality gate.

## Boundaries & Constraints

**Always:** Treat backend product requirements and implemented OpenAPI as read-only authority. Preserve complete mappings
for all 143 source requirements, action-scoped blocking on mixed-capability surfaces, tab-local inactivity, exact
backend-response disclosure boundaries, and every confirmed accessibility and security invariant. Keep BCG identifiers
and meanings aligned with `BACKEND-CONTRACT-GAPS.md`. Preserve unrelated worktree changes.

**Ask First:** Ask before changing backend product behavior, resolving the two intentionally open frontend architecture
library choices, reducing accessibility acceptance, or accepting an unresolved critical or high-severity contradiction.

**Never:** Do not edit backend contracts, invent unimplemented endpoints, hide an unresolved dependency, weaken privacy
or authorization behavior, claim control over browser-owned artifacts, or treat generated validation prose as more
authoritative than the canonical UX contracts.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Stale blocker mapping | Matrix omits BCG-014 or BCG-015 | Relevant flows and surfaces name the current blocker | Fail validation |
| Mixed capability | One action is blocked on a usable surface | Only the dependent action is blocked | Preserve usable actions |
| Review contradiction | A lens finds conflicting normative text | Correct the owning contract and rerun affected lenses | Do not waive high risk |
| Backend dependency | Required transport remains absent | Report a named BCG release blocker | Do not invent transport |
| Clean validation | No unresolved actionable contradiction | Replace all reviews and both reports | Retain genuine blockers |

</frozen-after-approval>

## Code Map

- `../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/BACKEND-CONTRACT-GAPS.md`
  -- authoritative ledger; implementation review added BCG-016 for mentor-completion eligibility.
- `../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/REQUIREMENT-FLOW-MATRIX.md`
  -- maps all 143 source requirements; the baseline omitted BCG-014, BCG-015, CP-13, and CP-14, now corrected.
- `../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/SURFACE-STATE-MATRIX.md`
  -- owns concrete surface states; the baseline lacked current invitation/mobile blockers and CP-13/14 coverage, now
  corrected.
- `../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/{DESIGN,EXPERIENCE,FRONTEND-PRD-FOUNDATION,FRONTEND-STATE-MACHINES,ACCESSIBILITY-ACCEPTANCE,SECURITY-PRIVACY-ACCEPTANCE}.md`
  -- canonical and companion contracts to change only when a fresh review identifies an actionable contradiction.
- `../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/review-*.md`
  -- generated rubric, accessibility, security/privacy, and edge/resilience review outputs to overwrite.
- `../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/validation-report.{md,html}`
  -- stale synthesized outputs to regenerate after review closure.
- `../mia-frontend/.agents/skills/bmad-ux/references/validate.md` -- read-only validation and synthesis procedure.
- `../mia-frontend/mia/docs/product-requirements.md` and `../mia-frontend/mia/api-doc/openapi.yaml`
  -- read-only product and implemented transport authority.

## Tasks & Acceptance

**Execution:**
- [x] Reconcile both matrices with BCG-014 and BCG-015 while preserving complete requirement and surface coverage.
- [x] Run rubric, accessibility, security/privacy, and edge/resilience reviews against the reconciled package.
- [x] Correct every actionable contradiction in its owning contract and rerun affected review lenses.
- [x] Regenerate `validation-report.md` and `validation-report.html` from the final review outputs.
- [x] Run the frontend repository's full quality gate and focused Markdown integrity checks for the UX workspace.

**Acceptance Criteria:**
- Given the authoritative blocker ledger, when matrix mappings are inspected, then BCG-001 through BCG-015 have accurate
  topic-based coverage and mixed surfaces block only dependent actions.
- Given the final canonical package, when all four lenses run, then no unresolved critical or high-severity frontend
  contradiction remains and backend dependencies stay explicitly named.
- Given the final lens outputs, when synthesis runs, then both validation reports describe the current package rather
  than superseded findings.
- Given all changed artifacts, when verification runs, then the full frontend gate, Markdown lint, 120-character check,
  and whitespace check pass or any unavailable component is reported precisely.

## Spec Change Log

- 2026-09-13: Reconciled the 15-blocker mappings, corrected the idle-warning and announcement contracts, regenerated all
  four reviews and both reports, and completed the available frontend quality checks.
- 2026-09-13: Applied independent-review patch findings for CP-13/14, explicit lifecycle and SSE recovery, single-response
  Continue working, exact BCG-014 scope, corrected BCG-015 resend semantics, and deterministic recovery states.
- 2026-09-13: Final review added route/surface parity, instantiated WCAG and action-scoped accessibility matrices, and
  registered BCG-016 and BCG-017 for mentor completion and privacy-safe mentor-role targeting.

## Design Notes

The validation report is evidence, not a new source of product behavior. A finding changes the narrowest contract that
owns the issue; reruns then establish whether the package is coherent. Persistent missing backend transport remains in
the BCG ledger rather than being approximated in frontend requirements.

## Verification

**Commands:**
- `./run-all-tests.sh` from `../mia-frontend` -- expected: the authoritative frontend quality gate passes.
- `npx markdownlint <changed UX Markdown files>` -- expected: no Markdown findings.
- `git diff --check` from `../mia-frontend` -- expected: no whitespace errors.

**Manual checks (if no CLI):**
- Confirm the final synthesized report cites all four current review outputs and distinguishes release blockers from
  intentionally deferred architecture choices.

**Results:**
- `./run-all-tests.sh` passed every available component: Trivy, Tailwind Plus catalog validation, and repository
  Markdown lint. It skipped npm checks, duplication detection, and npm audit because no `package.json`, source files, or
  `package-lock.json` exist yet.
- Focused Markdown lint passed for every changed Markdown artifact.
- The dedicated 120-character scan passed for every changed Markdown and HTML artifact.
- `npx --yes html-validate validation-report.html` passed.
- `git diff --check` and a direct trailing-whitespace scan passed; the direct scan covers the untracked planning files
  that Git cannot include in a normal diff.
- The final report cites all four review outputs and separates BCG release blockers from the two deferred architecture
  choices.
- Final integrity counts are 143 source requirements, 128 matched routes and surfaces, 31 state machines, 56 WCAG rows,
  53 action-scoped accessibility rows, and 17 registered backend blockers.

## Suggested Review Order

**Validation Result**

- Start with the final verdict, verified coverage, and remaining release gates.
  [`validation-report.md:7`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/validation-report.md#L7)

**Backend Boundaries**

- Review the authoritative blocker count and release rule before dependent UX details.
  [`BACKEND-CONTRACT-GAPS.md:31`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/BACKEND-CONTRACT-GAPS.md#L31)

- Check server-authored mentor completion rather than local-clock eligibility.
  [`BACKEND-CONTRACT-GAPS.md:924`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/BACKEND-CONTRACT-GAPS.md#L924)

- Check privacy-safe supervisor targeting without administrator-directory access.
  [`BACKEND-CONTRACT-GAPS.md:980`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/BACKEND-CONTRACT-GAPS.md#L980)

**Navigation And State**

- Use the route contract as the router and protected-mount implementation entry point.
  [`ROUTE-INVENTORY.md:13`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/ROUTE-INVENTORY.md#L13)

- Verify mentor eligibility and ambiguous completion before implementing scheduled work.
  [`FRONTEND-STATE-MACHINES.md:524`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/FRONTEND-STATE-MACHINES.md#L524)

- Verify tab-local expiry, explicit continuation, and privacy shielding.
  [`FRONTEND-STATE-MACHINES.md:651`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/FRONTEND-STATE-MACHINES.md#L651)

- Verify invitation success, delivery failure, and ambiguous-response separation.
  [`FRONTEND-STATE-MACHINES.md:775`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/FRONTEND-STATE-MACHINES.md#L775)

**Accessibility Evidence**

- Review shared state lanes and action-scoped release cells together.
  [`ACCESSIBILITY-ACCEPTANCE.md:944`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/ACCESSIBILITY-ACCEPTANCE.md#L944)

- Review all ungrouped WCAG 2.2 A/AA planning records.
  [`ACCESSIBILITY-ACCEPTANCE.md:1203`](../../../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/ACCESSIBILITY-ACCEPTANCE.md#L1203)
