# PRD Quality Review — MIA — AI-Powered Tutoring Platform (MVP)

## Overall verdict

This is an unusually trustworthy distillation PRD: nearly every requirement carries exact, testable bounds, scope
omissions are stated rather than implied, and the document refuses to invent behavior where sources are silent —
including the deliberate, flagged absence of success metrics. What's at risk is downstream consumption and the
launch decision itself: there is no glossary for a chain-top document that leans heavily on compound state terms
("approved, ready, file-backed"), FR-57 references two artifacts ("course brief", "student brief") defined
nowhere in the PRD, and the success-metrics deferral leaves no criterion — even qualitative — by which anyone
can call the launch successful.

## Decision-readiness — strong

Decisions read as decisions throughout. §3 Non-Goals names confirmed exclusions with their rationale locus
("operator responsibility", "MVP-scoped exclusions"), NFR-5 states the field-encryption trade-off bluntly
("Anyone with storage access is fully trusted, and operators must be told so"), and §13 Open Questions are
genuinely open — item 5 (interrupted-response retry eligibility) and item 4 (brief regeneration path) have no
smuggled answers. The single `[NOTE FOR PM]` (§12) sits at the PRD's one real unresolved tension rather than at a
safe checkpoint. The addendum's "Cross-document tensions" section (items 1–7) is exactly the honest
trade-off surfacing this rubric asks for.

### Findings

- **medium** No priority tiers across 84 FRs (§6) — every FR is implicitly launch-blocking. A decision-maker
  forced to cut scope has no guidance on what is contract-mandatory versus deferrable; the "MVP-scoped
  exclusions" list (§3) shows what was already cut but not what could still move. *Fix:* add one sentence stating
  all FRs are launch-blocking by contract, or mark the few that could slip (e.g., FR-72/73 text-to-speech, which
  is already optional-provider-gated).

## Substance over theater — strong

No furniture found. The four journey protagonists (Ana, Sofía, Mia, Marco) map one-to-one onto the four roles and
each drives acceptance scenarios (§11 items 1, 2, 4, 13, 22). NFRs carry product-specific thresholds throughout —
NFR-10 gives exact rate-limit tuples, NFR-16 gives "every 16 KiB or 1 second", NFR-12 gives chunking bounds with
a named failure code (`summary_input_too_large`). There is no differentiation/innovation section written to fill
a template slot; §2's positioning ("ships no content of its own") is doing real framing work. The Success Metrics
section refusing to fabricate KPIs is the opposite of theater.

## Strategic coherence — adequate

The thesis is clear and stated twice: curriculum-anchored tutoring with humans structurally in control (§1, §2's
four-step human-in-the-loop pipeline, G1–G5). Features consistently serve it — the approval/revocation machinery
(FR-46, FR-40), mentoring-as-last-resource (FR-65), and completed-session review (FR-63) all trace to G1/G2. The
scope kind is coherent: a problem-solving MVP whose exclusions (§3) protect the pipeline rather than trim
randomly.

What keeps this at adequate is that the thesis is unverifiable as written. The PRD honestly reports the
repository defines no metrics, but a launch-grade contract with zero success criteria — not even qualitative
ones — means the bet on G1–G5 can never be called won or lost.

### Findings

- **high** Success metrics deferred without a decision mechanism (§12) — the `[NOTE FOR PM]` correctly refuses to
  invent KPIs and even names plausible counter-metrics ("over-reliance on mentoring escalation", "provider-cost
  per session"), but "before launch, the product owner should decide" has no owner, forum, or deadline, and the
  32 acceptance scenarios verify behavior, not success. *Fix:* the PM either commits a minimal metric set
  compatible with the no-telemetry posture (e.g., operator-visible counters already in FR-84) or records an
  explicit decision that scenario-based verification is the launch criterion — either way, close the question.

## Done-ness clarity — strong

This is the PRD's best dimension. Nearly every FR carries at least one verifiable consequence with exact bounds:
FR-27 specifies TOTP down to "a time step succeeds only once per factor", FR-50 gives default/cap pairs for every
upload dimension, FR-59 bounds retrieval at "≤3 retrieval rounds, ≤8 excerpts, each ≤4,000 code points and
≤16 KiB", FR-56 defines idempotency conflict semantics precisely. The 32 acceptance scenarios (§11) are
observable and mostly negative-case (items 7, 8, 21, 27, 31), which is where done-ness usually leaks. The
handful of soft phrases that remain are either quantified elsewhere (FR-60's "bounded intervals" → NFR-16) or
knowingly deferred (§13 item 6).

### Findings

- **medium** "progressive delays" unquantified (NFR-10) — login throttling gives exact failure counts and
  windows but no delay schedule; an engineer cannot implement or test "progressive" without inventing it. *Fix:*
  state the schedule or explicitly defer it to the auth slice contract alongside §13 item 6.
- **medium** "minimal identity" undefined (§4 Visibility; FR-67) — the field set mentors may see is nowhere
  enumerated, and addendum tension #6 confirms `GET /users` visibility shaping is a live authorization-design
  risk. For a PRD whose §4 says role flags never suffice, the one visibility tier left as an adjective is the
  mentor one. *Fix:* enumerate the fields (e.g., name, nickname, avatar) or add this to §13 as an explicit open
  question.
- **low** Deferred code enumerations — "sanitized failure code" (FR-9), "safe restart code" (FR-64) are
  placeholders for the error-code catalog deferred by §13 item 6. Acceptable, but story authors should treat
  these FRs as blocked on that catalog. *Fix:* none required; cross-reference §13 item 6 from these FRs.

## Scope honesty — strong

Exemplary. Non-Goals (§3) does real work — "No malware scanning and no claim that accepted files are
malware-free" and "No monetary budgets or provider spending limits" preempt exactly the assumptions readers would
otherwise make. The MVP-exclusion list names seven silent-assumption traps individually. §13's seven open items
are concrete and sourced, and item 7 explicitly separates "candidates for post-MVP revisiting" from committed
behavior. The framing rule — "silence is not a product decision" (§1, restated §14) — is applied consistently:
the Success Metrics gap is flagged, not papered over. Open-items density (7 open questions + 3 assumptions +
1 PM note) is low-to-appropriate for a green-light PRD of this size, and each open item is either
implementation-time by design or escalated.

## Downstream usability — adequate

This is a chain-top PRD feeding architecture and story creation, so this dimension carries weight — and it is the
weakest. IDs are clean (FR-1..84 contiguous, NFR-1..21 contiguous, UJ-1..6 with named protagonists), internal
cross-references resolve (FR-35→FR-28, §9→FR-50), and the addendum's separation of technical depth is exactly
right for downstream layering. But there is no Glossary, and the PRD leans on repeated compound state phrases as
de-facto defined terms: "approved, ready, file-backed" appears in FR-39, FR-40, FR-51, FR-59; "student-only
account" versus "staff" carries the entire security model (§4, FR-19, FR-32, FR-74, FR-76); "ready", "faulty",
"draft", "processing" form a material state machine that is only implied by FR-48. Story extraction will
re-derive these definitions per story, with drift risk.

### Findings

- **high** No Glossary (whole document) — for a chain-top PRD, domain nouns and state terms ("staff",
  "student-only account", "ready", "faulty", "approved", "file-backed", "active session", "restricted stage",
  "security generation") are defined only in situ inside individual FRs. Downstream workflows cannot
  source-extract a section alone without hunting for the defining FR. *Fix:* add a ~15-term glossary covering
  roles-vs-account-kind terms, material states, and session states; point each entry at its defining FR.
- **medium** Undefined artifacts "course brief" and "student brief" (FR-57) — the tutor's initial context names
  "course brief and instructions, student brief and per-student instructions", but the PRD defines briefs only
  for materials (FR-49); §5 UJ-2 mentions a "course description" and FR-34 "per-student AI instructions", neither
  called a brief. An architect cannot tell whether these are new artifacts or renamings. *Fix:* align FR-57's
  terms with the defined artifacts or define the two briefs explicitly.

## Shape fit — strong

The shape matches the product and the situation: a multi-role B2B-like platform gets load-bearing UJs with named
protagonists (§5), while the bulk of the document is a capability/behavior contract — correct for a distillation
of an authoritative behavior source into a launch contract. The 84-FR density is earned by source completeness,
not over-formalization, and the addendum absorbs what would otherwise bloat the PRD (provider deadlines, job
mechanics, DB design). The `[ASSUMPTION]` marking that journey protagonists are "illustrative framing over
documented steps" (§5 intro, §14) is exactly the right honesty for a brownfield-documentation distillation. No
forced sections detected — notably, no fabricated Differentiation or Market section.

## Mechanical notes

- **Assumptions Index roundtrip**: §14 lists three `[ASSUMPTION]` entries, but only the journey-framing one
  appears inline (§5). The "primary audiences" and "launch-grade means self-hosted MVP" assumptions are
  index-only with no inline tag at the locations they govern (§1). Minor; fix by tagging §1 or noting they are
  document-level.
- **ID continuity**: FR-1..84 and NFR-1..21 contiguous, no duplicates. Acceptance scenarios numbered 1–32 but not
  ID-prefixed (AS-n) — story creation may want stable IDs for traceability.
- **Cross-references**: FR-35→FR-28 and §9→FR-50 resolve. Addendum tension items reference PRD behavior
  consistently.
- **Glossary drift**: "course-wide material" vs "course material" (§5 UJ-4 "downloads approved course material",
  §4 role list) — same referent, minor. "brief" is overloaded across material brief / course brief / student
  brief (see downstream finding). "Session" is used for both authentication sessions (§6.5) and tutoring sessions
  (§6.10); context disambiguates, but a glossary should split the terms.
- **Required sections**: all present for the agreed stakes; Success Metrics present-but-empty is a flagged
  decision, not a template omission.
