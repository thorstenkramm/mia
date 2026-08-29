# Reconciliation — ARCHITECTURE-SPINE.md vs prd.md + addendum.md

Date: 2026-08-29
Verdict: RECONCILED WITH FINDINGS — no hard numeric/mechanism contradictions found; 2 high, 3 medium,
4 low findings. High items are one Capability-Map misgrouping and one dropped cross-cutting credential-storage
rule.

## High

### H-1 — Capability Map misgroups FR-15..17 into the auth slice

Spine row: `Auth sessions, passwords, recovery (FR-15..25) | auth | AD-7, AD-8`.
PRD §6.3 defines FR-15..17 as **student provisioning and course membership** (supervisor provisions student,
add-by-exact-username enrollment, rejoin semantics). These belong to `user`/`course` ownership, not the `auth`
credential slice. PRD §6.4 (passwords/recovery) starts at FR-18 and §6.5 (sessions) at FR-22. As written, the
map assigns student-account creation and course-membership tables to `auth`, colliding with the
`Courses & memberships (FR-37..44) → course` and `Identity & account data → user` rows and inviting two owners
for membership data (violates the spine's own AD-3). Fix: split the row to
`Auth sessions, passwords, recovery (FR-18..25)` and map FR-15..17 to `user`/`course`.

### H-2 — Credential-storage mechanisms (Argon2id, SHA-256 digests) dropped

NFR-4 fixes **fixed-parameter Argon2id** for passwords and **SHA-256-digest-only** storage for bearer tokens;
FR-14 extends digest-only storage to invitation/reset tokens and FR-30 to `mfa-management` proofs. The spine
never states either mechanism (not in Stack, Conventions, or any AD). Password hashing is exercised by at least
four independently built slices (bootstrap-admin, invitation acceptance, student provisioning, recovery/MFA),
and token digesting by three. Without a spine rule, slices can plausibly diverge (different KDF parameters,
plaintext-token persistence). This is exactly the class of quiet cross-cutting rule the spine exists to pin.
Suggested home: a Conventions row ("Credentials: fixed-parameter Argon2id for passwords; opaque
tokens/proofs persisted only as SHA-256 digests") or an addition to the Redaction row.

## Medium

### M-1 — "No generic job retry" rule not encoded (addendum tension 6)

FR-83 and addendum tension 6 require that the only retry paths be the domain actions material re-finalization
and session-summary regeneration — never a generic job-retry operation. AD-5 fixes the execution split and AD-6
the commit discipline, but neither forbids a slice from exposing a job requeue/retry endpoint or an internal
generic retry helper. The Jobs-oversight map row (FR-83..84 → AD-5, AD-6) therefore does not carry this
constraint. One sentence in AD-5 or AD-6 ("retries exist only as the jobs system's automatic attempts and the
named domain actions; no generic retry operation") closes it.

### M-2 — Emailed links built only from configured public URL (NFR-8) dropped

NFR-8 / addendum: invitation and reset links are built **only** from the mandatory configured HTTPS
`main.public_url`, never derived from request or forwarded headers. This spans two independently built slices
(invitation, recovery) plus the smtp adapter, and the natural-but-wrong implementation (derive origin from
`Host`/`X-Forwarded-*`) is a security bug NFR-8 explicitly forbids. The spine's client-IP-trust wiring in
`httpserver` does not imply it. Belongs in Conventions or AD-10.

### M-3 — Addendum tensions 3–5 neither resolved nor named in Deferred

The spine's Deferred list names only tension 1 (`GET /jobs` visibility) and resolves tension 2 (shutdown
asymmetry, encoded in AD-5). Tensions 3 (`GET /tutoring-sessions/{id}/materials` used-vs-selected semantics and
per-role visibility), 4 (SSE state enum lacking `queued`; snapshot for never-started queued response), and
5 (`GET /users` expressing three visibility shapes on one route — flagged as an authorization-design **risk**,
PRD §13 item 13) are covered only implicitly by the generic "per-route API contracts" deferral. Tension 5 in
particular is an AD-4-adjacent design risk, not a routine contract detail; the Deferred section should name
these three so the auth/user slice does not treat them as settled.

## Low

### L-1 — FR-81/FR-82 absent from the Capability Map

FR-81 (UTC instants, per-user IANA display zone, dual-field DST-safe local schedules) is only partially covered
by the Instants convention row; the user-preferred-timezone and dual-field schedule rules (needed by mentoring
scheduling) map to no row. FR-82 (no notifications) is a non-feature and is acceptable to omit, but the binds
line claims FR-1..FR-84 coverage.

### L-2 — AD-5 binds omit FR-49

AD-5 lists FR-48, FR-60..64, FR-72..73, FR-83..84, but the material-summary job it names implements FR-49
(brief generation). Cosmetic binds gap.

### L-3 — HTTP-layer body caps and server timeouts unstated

NFR-13's 1 MiB non-upload body cap and the addendum's fixed HTTP server timeouts (5 s headers, 30 s non-upload
body, 15 min upload body, 120 s idle, 30 s graceful shutdown) appear nowhere in the spine. Risk is low because
they live once in the `httpserver` kernel, but the graceful-shutdown 30 s figure interacts with AD-5's
shutdown behavior and could be pinned there.

### L-4 — Email content rules (FR-21) span two slices without a spine hook

English-only plain-text UTF-8 with sanitized headers applies to both invitation and recovery emails. Deferring
to the smtp adapter contract is defensible; noting it in AD-10 ("adapters enforce plain-text/sanitized-header
mail") would remove the divergence window.

## Verified non-findings (checked, consistent)

- Binds range FR-1..84 / NFR-1..21 / AS-1..32 matches PRD numbering exactly.
- AD-5 lease/attempt numbers (2 min lease, 30 s renewal, ≤3 attempts), 16 KiB/1 s persistence, and
  cancel-vs-requeue shutdown asymmetry match addendum job mechanics and NFR-16 (tension 2 resolved).
- AD-8 cookie contents, security-generation semantics, and per-request reload match FR-22..25 and addendum.
- AD-9 SQLite settings, lock-before-open, migration refusal match NFR-19/20 and addendum.
- AD-11 startup order matches NFR-15 verbatim.
- AD-12 same-tx audit, content-free, fingerprints, no denied-read auditing match FR-79..80.
- Conventions (uniqueness normalization, text handling, instants, redaction) match FR-1/2/5/37/45, NFR-6.
- Stack (Echo 5.3.1, modernc sqlite, golang-migrate v4, o200k_base, SecLists top-100k, gpt-5.6-terra,
  mistral-ocr-4-1) matches PRD/addendum.
