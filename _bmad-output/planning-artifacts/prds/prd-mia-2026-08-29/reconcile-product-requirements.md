# Reconciliation: docs/product-requirements.md vs prd.md + addendum.md

Scope: confirmed product behavior in `docs/product-requirements.md` (1,681 lines) checked against
`prd.md` and `addendum.md`. Compression and downstream implementation detail were not flagged.
All PRD numeric limits, actors, and scopes were spot-checked against the source; no wrong numbers,
wrong actors, or inverted rules were found. Findings below are ordered by severity.

Verdict: **0 hard contradictions, 1 materially weakened rule, 14 requirement-level gaps
(5 medium, 9 low).**

---

## Weakened / potentially contradicting statements

### W-1 (Medium) — Session-creation idempotency after completion is a conflict, not a replay

- Source: `docs/product-requirements.md:1056-1063` — while the created session is active, replaying
  the same creation request ID with the same content returns that session; **after the session is
  completed, every reuse of its creation request identifier is a conflict**.
- PRD: FR-56 states "identical replay returns the existing resource; … post-completion **message**
  replays are read-only." The unqualified first clause invites the wrong reading that a completed
  session's creation-ID replay also returns the resource. The source makes creation-ID reuse after
  completion always a conflict; only message-ID replays are read-only post-completion.
- Affected PRD section: 6.10 FR-56.

## Gaps — Medium

### G-1 — Staff/administrator MFA-reset effects are unstated

- Source: `docs/product-requirements.md:661-676` — the staff reset (by a different administrator)
  removes the factor, invalidates all recovery codes, **requires password replacement at next
  login**, and **restricts existing browser cookies to password replacement and logout**. The local
  sole-administrator recovery has the same effects.
- PRD: FR-32 fully describes the student path (cookie invalidation, forced replacement) but for
  staff says only "a different administrator performs the reset" and describes the local command
  mechanics. The staff-path consequences (forced password replacement, cookie restriction rather
  than invalidation) are absent from PRD and addendum. A reader would infer either no consequence
  or the student consequence — both wrong.
- Affected PRD section: 6.6 FR-32.

### G-2 — Invitation acceptance under an email-uniqueness conflict does not consume the invitation

- Source: `docs/product-requirements.md:385-387` — "An email uniqueness conflict rejects acceptance
  **without consuming the invitation**."
- PRD: FR-8 covers only creation-time rejection of an already-registered email. Acceptance-time
  conflict behavior (invitation stays pending and usable) is absent, yet it interacts with FR-9's
  "single-use, consumed by acceptance" rule — silence suggests the opposite outcome.
- Affected PRD section: 6.2 FR-8/FR-9.

### G-3 — Page-counting rule needed to interpret the page limits

- Source: `docs/product-requirements.md:955-956` — each PDF page and each JPEG or PNG file counts
  as one page; DOCX, text, and Markdown are **non-paged** and subject only to byte/expansion limits.
- PRD: FR-50 states the 1,000/2,000 page limits but not what a "page" is, so the limit is not
  interpretable (does a 5 MB text file have pages? does an image count?). Absent from addendum too.
- Affected PRD section: 6.9 FR-50.

### G-4 — Successful staff profile-mobile *change* also deletes pending SMS enrollments

- Source: `docs/product-requirements.md:309-310` — "Successful profile-mobile **change or removal**
  deletes any pending SMS enrollment or replacement."
- PRD: FR-33 attaches this effect only to mobile *removal*; FR-35 (self-service change) omits it.
  Only the supervisor-entered student mobile path (FR-34) carries the invalidation. The staff
  self-change branch is silently dropped.
- Affected PRD section: 6.7 FR-33/FR-35.

### G-5 — No notification to the previous mobile number after a change

- Source: `docs/product-requirements.md:304-305` — "After a successful change, MIA sends no
  notification to the previous mobile number." This is a deliberate product decision (a common
  security expectation MIA explicitly does not meet), not implementation detail.
- PRD: absent from FR-35, the non-goals list, and the addendum.
- Affected PRD section: 6.7 FR-35 (or Non-Goals).

## Gaps — Low

### G-6 — Extracted-content size bound (512 MiB per material) absent

- Source: `docs/product-requirements.md:921-924` — total decoded segment text and encoded
  `content.jsonl` are each limited to 512 MiB per material.
- PRD/addendum: `content.jsonl` is mentioned (FR-48, addendum) but this bound appears nowhere;
  NFR-13's generic "everything is bounded" does not carry the confirmed number.

### G-7 — TOTP authenticator issuer/label is user-visible and confirmed

- Source: `docs/product-requirements.md:584-585` — issuer `MIA (<hostname>)` from normalized
  `main.public_url`; account label is the username. Visible to every TOTP user; absent from both
  documents.

### G-8 — Voice-ID constraint and "no provider voice discovery" scoping

- Source: `docs/product-requirements.md:1390-1392` — voice ID ≤128 printable ASCII characters;
  **MIA provides no provider voice discovery** (a product-scope exclusion). FR-72 mentions only "a
  stored ElevenLabs voice ID"; the exclusion is missing from Non-Goals.

### G-9 — "MIA does not store or display operator-configured safeguarding text"

- Source: `docs/product-requirements.md:1538-1539`. Section 10 of the PRD says supervisors provide
  local guidance outside MIA but does not carry the explicit exclusion that MIA has no configurable
  safeguarding text — a scoping decision an operator/PM would otherwise assume exists.

### G-10 — Time-zone value rules: reject `Local` and numeric fixed offsets

- Source: `docs/product-requirements.md:141-143` — accepts `UTC` or a named IANA identifier from
  the embedded database; rejects `Local` and fixed offsets. FR-3 says only "preferred IANA time
  zone"; the rejection of offsets is a requirement-level validation rule with UX impact.

### G-11 — Recovery-code verification ignores display hyphens and ASCII letter case

- Source: `docs/product-requirements.md:638-639`. User-facing entry behavior (codes displayed as
  4×4 groups must be accepted with or without hyphens, case-insensitively); absent from FR-29.

### G-12 — Original-filename constraints

- Source: `docs/product-requirements.md:967-970` — valid NFC UTF-8, path components stripped,
  basename ≤255 code points and ≤1 KiB, never used as a filesystem path, rejects NUL/controls/bidi.
  Borderline implementation detail, but the 255/1 KiB limit is a confirmed input bound absent from
  both documents (NFR-13 is generic).

### G-13 — Login backoff specifics and reset-token nuance

- Source: `docs/product-requirements.md:1561-1567, 1573-1575` — progressive delays of 1/2/4 s after
  failures 2/3/4, `429` + `Retry-After` instead of handler sleeps, successful login clears username
  state but not source-IP state; password-policy failures on reset submission count toward the
  limit but do not consume an otherwise valid token. NFR-10 carries the counts but says only
  "progressive delays"; these confirmed values/behaviors appear nowhere. Arguably downstream, but
  the source treats rate limits as fixed product decisions.

### G-14 — An already-established SSE stream may continue after a ban

- Source: `docs/product-requirements.md:549-551`. FR-74 says a ban does not cancel in-flight
  provider/background work, but the distinct user-visible nuance — a banned student's open SSE
  stream keeps delivering until its natural end — is not stated in the PRD.

---

## Verified as covered (spot-check summary)

Identity limits, invitation lifecycle incl. SMTP fault/timeout, direct role grants, student
provisioning/recovery, password policy, session lifetimes and login stages, MFA numbers and proofs,
avatar/logo pipeline, course lifecycle incl. loss-of-approved-material and deactivation scope,
material scopes/processing/link-only/upload caps, tutoring session start/context/retrieval/
streaming/retry/shutdown numbers, mentoring flow and triage, time handling, provider disclosure
table, speech retention, bans, deletion lifecycle and zero-row guard, audit exclusions, rate-limit
counts, safeguarding tone, and all 32 acceptance scenarios — all match the source with correct
numbers, actors, and scope.
