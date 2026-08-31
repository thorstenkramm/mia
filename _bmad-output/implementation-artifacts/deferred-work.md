# Deferred Work

## Deferred from: code review of spec-1-3-staff-password-recovery-over-smtp (2026-08-31)

- Expired and consumed `password_reset_challenges` rows are never pruned. Growth is bounded per hour by the
  recovery limiters, but the table grows indefinitely. AGENTS.md's baseline ("retain operational data until
  authorized deletion") makes no-pruning the compliant default; introducing cleanup (e.g. at startup alongside
  speech cleanup) needs an explicit product decision.
