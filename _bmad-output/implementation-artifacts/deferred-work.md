# Deferred Work

## Deferred from: code review of spec-1-3-staff-password-recovery-over-smtp (2026-08-31)

- Expired and consumed `password_reset_challenges` rows are never pruned. Growth is bounded per hour by the
  recovery limiters, but the table grows indefinitely. AGENTS.md's baseline ("retain operational data until
  authorized deletion") makes no-pruning the compliant default; introducing cleanup (e.g. at startup alongside
  speech cleanup) needs an explicit product decision.

## Deferred from: code review of json-api-allignment-plan (2026-09-05)

- Plan finding 1.2 requires adding the Redocly lint command to repository CI; the repository has no CI
  configuration at all. `run-all-tests.sh` and the rule's endpoint checklist carry the gate today. Revisit
  when CI infrastructure is introduced.

## Deferred from: code review of spec-fix-mfa-completion-session (2026-09-19)

- Reset all inherited session values instead of the current stage-specific key; this is broader session hardening
  beyond the present MFA carry-over defect.
- Validate the `stage` and `challengeID` pairing at the session API boundary; current callers satisfy the contract and
  changing that boundary is outside this focused fix.

## Deferred from: spec-frontend-entry-contracts (2026-09-25)

- source_spec: spec-frontend-entry-contracts.md
  summary: Design bounded current-account scope retrieval to replace the unbounded assignment arrays.
  evidence: The user explicitly deferred the existing pagination-rule conflict for this task only. The current
    capability response returns complete, unpaginated scope arrays that grow with assignments; this is a known
    limitation, not a permanent pagination exemption. Follow-up must define handling of assignment changes between
    pages and how the frontend establishes complete scope before mounting protected sections, without treating
    partial results as complete or cached scope as authorization. Scope transport redesign requires approval.
