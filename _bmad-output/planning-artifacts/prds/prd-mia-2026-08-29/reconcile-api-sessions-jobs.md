# Reconciliation: api.md / tutoring-sessions.md / jobs.md vs PRD + addendum

Scope: `docs/api.md` (draft, not authoritative), `docs/tutoring-sessions.md`, `docs/jobs.md` compared against
`prd.md` and `addendum.md`. Where api.md and `docs/product-requirements.md` agree, the behavior is treated as
confirmed. Findings are ordered by severity.

## Verdict

Largely faithful. No hard numeric contradictions found (limits, deadlines, budgets, lease/retry mechanics all
match). Seven gaps: one security-relevant omission (staff/sole-admin MFA reset consequences), two behavioral
compressions in the tutoring-session contract that a PM/architect would need, and four lower-severity omissions.

## Findings

### 1. HIGH — Staff and sole-admin MFA reset omit forced password replacement and cookie restriction

- Source: api.md:163-178 (`reset-admin-mfa` "requires password replacement", "Current browser cookies become
  restricted to password replacement and logout"); confirmed by product-requirements.md:668-676 (staff reset
  "requires password replacement at next login. Existing browser cookies are restricted to password replacement
  and logout"; local recovery likewise).
- PRD: FR-32 states forced password replacement and cookie invalidation only for the student-only reset path.
  For staff resets (different administrator) and the local `reset-admin-mfa` command it describes the actor and
  mechanics but not the mandatory password-replacement gate or the restriction of existing cookies.
- Impact: an implementer following FR-32 alone would leave a staff account fully usable after an MFA reset with
  its old password and live cookies — a security behavior difference, not just compression.
- Affected: PRD 6.6 FR-32 (also touches FR-24 stage description).

### 2. MEDIUM — Session-creation request-ID: post-completion replay is always a conflict

- Source: api.md:497-504 ("After completion, every reuse of that request ID is a conflict while the session
  remains retained"); confirmed by product-requirements.md:1059-1060.
- PRD: FR-56 merges session-creation and message idempotency: "identical replay returns the existing resource;
  same ID with different content is a conflict; post-completion message replays are read-only." Read literally,
  an identical session-creation replay after completion would return the session — the sources say it must
  conflict. Only message replays are read-only after completion.
- Impact: wrong idempotency semantics for the session-creation endpoint; frontend retry logic depends on this.
- Affected: PRD 6.10 FR-56.

### 3. MEDIUM — Queued generation includes preserved partial response text as conversation context

- Source: tutoring-sessions.md:131-134 ("The queued response begins after the current response reaches any
  terminal state and uses preserved partial text as conversation context"); confirmed by
  product-requirements.md:1149-1151.
- PRD: FR-58 describes context assembly (instructions + current message + newest complete turns) and FR-60/FR-61
  cover the queue and partial-text preservation, but nowhere states that the interrupted/failed response's
  partial text feeds the next generation's context. FR-58's "newest complete turns" wording arguably excludes it.
- Impact: product-visible tutor continuity behavior after interruption/failure; also affects token budgeting.
- Affected: PRD 6.10 FR-58, FR-60/FR-61.

### 4. LOW/MEDIUM — Upload validation and extraction scope beyond encryption is uncaptured

- Source: jobs.md:66-74 (PDF validation rejects malformed cross-references, embedded files, JavaScript, and
  launch actions before Mistral; DOCX extraction covers main document, tables, headers, footers, footnotes,
  endnotes, inserted tracked changes, disables entities/external relationships); confirmed by
  product-requirements.md:1004-1011.
- PRD: FR-45 mentions only encrypted/password-protected rejection and "no macros"; FR-48 says "bounded local
  parsers." Neither PRD nor addendum records which PDF constructs cause rejection or which DOCX parts become
  tutor-visible content.
- Impact: product-visible (users see specific uploads rejected; supervisors need to know what DOCX content the
  tutor can and cannot see, e.g. comments and hidden text are excluded).
- Affected: PRD 6.9 FR-45/FR-48; addendum job-system section.

### 5. LOW — Session-summary job proceeds after used-material deletion, omitting that material's identity

- Source: jobs.md:172-175 ("If a used material was deleted before this job reads its identity, the input omits
  that material name and type. Material deletion does not delete or block a session-owned summary job");
  confirmed by product-requirements.md:846.
- PRD: FR-52 preserves summaries on deletion and FR-63 covers summary generation, but neither states that a
  pending/queued session summary still runs and silently omits deleted materials from its input.
- Affected: PRD 6.9 FR-52, 6.10 FR-63.

### 6. LOW — 512 MiB per-material bound on extracted content is missing from the bounds catalog

- Source: jobs.md:56-57 ("Encoded JSONL and decoded text are each bounded to 512 MiB per material"); confirmed
  by product-requirements.md:923-924.
- PRD/addendum: FR-50 lists upload caps and NFR-13 asserts "nothing unbounded" generically, but this specific
  extraction-output bound appears nowhere. Architects sizing storage and streaming search need it.
- Affected: PRD 6.9 FR-50 / 7.3 NFR-13; addendum job-system section.

### 7. LOW — Course-logo management actor is unstated

- Source: api.md:409-412 ("An administrator or assigned supervisor may mutate the course logo"); confirmed by
  product-requirements.md:705-707.
- PRD: FR-36 covers logo format pipeline and download authorization but never says who may upload, replace, or
  remove a logo. Notable because it is one of the few places a plain administrator (without supervisor
  assignment) may mutate course content.
- Affected: PRD 6.7 FR-36 / 6.8.

## Checked and NOT reported

- Invitation resend SMTP failure making the invitation faulty: PRD FR-9 "(or resend)" matches
  product-requirements.md:367; api.md's silence is a draft omission, not a PRD gap.
- All numeric limits in the three sources (retrieval rounds/excerpt sizes, 8,000 cp/32 KiB inlining and message
  caps, token budgets, SSE heartbeat/deadline/queue bounds, persistence cadence, job lease/attempt/delay values,
  provider deadlines, summarization chunking, speech constraints) match PRD/addendum exactly.
- api.md SSE state enum lacking `queued`, `GET /tutoring-sessions/{id}/materials` semantics, shutdown asymmetry,
  and tutor/speech work sitting outside the jobs table are already recorded as addendum cross-document tensions.
- jobs.md detail that a shutdown-interrupted attempt "stays counted" against the 3-attempt limit is a mechanics
  nuance the addendum's "cancel and requeue if attempts remain" does not spell out — acceptable compression, but
  worth one clause if the addendum job section is revised.
