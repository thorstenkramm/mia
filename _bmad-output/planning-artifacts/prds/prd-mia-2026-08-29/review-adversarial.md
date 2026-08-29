# Adversarial Review — MIA MVP PRD (prd.md + addendum.md)

Reviewed: `prd.md` (747 lines), `addendum.md` (109 lines), cross-checked against
`docs/product-requirements.md` (authoritative source) where findings required verification.

Verdict: **Strong fidelity overall, but not safe to hand downstream as-is** — one requirement contradicts the
authoritative source in a security-relevant way, and two lifecycle/oversight collisions would force
architecture and epics to guess.

Scope note honored: absence of unconfirmed features is not flagged; findings below are contradictions,
ambiguities, unverifiable statements, silent edge-case collisions, or fidelity drift from the source.

---

## Critical

### C1. FR-34 drops the "student-only" scoping and creates a staff-profile escalation path

- Severity: critical
- Location: §6.7 FR-34 (vs FR-33, §4 authorization principles)
- Attack: FR-34 says "Any supervisor sharing an assigned course edits **the student's** non-security profile
  fields (… email, mobile …)". The authoritative source scopes this power to **student-only accounts**
  (docs/product-requirements.md:194-195 "edit non-security profile fields for any **student-only** account that
  shares an assigned course"; :266-267 repeats it). FR-44 puts supervisors into their own courses as ordinary
  students, so accounts holding the student role are not necessarily student-only. Read literally, FR-34 lets
  supervisor A edit fellow supervisor B's email (contradicting FR-33 "staff email is immutable") and enter a
  mobile number that is "immediately verified", bypassing FR-35's SMS-confirmation requirement for staff mobile
  changes. Two developers will implement this two materially different ways, and one of them ships a
  scope-escalation hole.
- Fix: Rewrite FR-34's second sentence to "Any supervisor sharing an assigned course with a **student-only
  account** edits that student's non-security profile fields…", mirroring FR-19/FR-32/FR-74 which all carry the
  qualifier correctly. Add one sentence stating that accounts with any staff role are governed exclusively by
  FR-33/FR-35 regardless of course enrollment.

---

## High

### H1. Ban × active tutoring session creates an unresolvable lifecycle deadlock the PRD never addresses

- Severity: high
- Location: §6.13 FR-74, §6.10 FR-54/FR-55, §6.8 FR-42/FR-43, §6.14 FR-76/FR-77
- Attack: A banned student cannot log in (FR-74), so their active session can never be finished — finishing is
  the owning student's only action and staff cannot force completion (FR-55). Sessions never expire (FR-55).
  That eternal active session permanently blocks student removal from the course (FR-42) and course deletion
  (FR-43). FR-76/FR-77 are silent on whether account deletion is permitted while a session is active; because
  FR-42 and FR-43 both state explicit no-active-session preconditions, a developer may reasonably infer the
  same precondition for deletion — producing a fully deadlocked account whose only remedy is unbanning. The
  source (docs/product-requirements.md:1435-1449) confirms deletion has no active-session precondition, but the
  PRD does not say so, and "silence = undecided" is the PRD's own stated reading rule. Same trap for a student
  who simply abandons a session and never returns.
- Fix: Add one sentence to FR-76/FR-77: "Account deletion proceeds regardless of active tutoring sessions;
  the session and its data are deleted with the account (per FR-78 semantics)." Add one sentence to FR-74 or
  FR-42 acknowledging that a banned student's active session blocks course-scoped removal until unban or
  account deletion — so downstream UX/epics design the supervisor remediation path deliberately.

### H2. Human-oversight goal is structurally evadable: chat review is gated on student-controlled completion

- Severity: high
- Location: §3 G2, §5 UJ-6, §6.10 FR-55/FR-63, §10 Responsible AI
- Attack: Supervisors can read chat messages only after completion (FR-63); only the student can complete
  (FR-55); sessions never expire (FR-55). A student can therefore keep one session active indefinitely and
  conduct unbounded tutoring inside it that no human can ever review — while G2 and §10 present
  "reviewable completed sessions" as the structural human-oversight mechanism for a product that may serve
  minors. The behavior is source-confirmed, so this is not invented product design — but the PRD sells
  oversight as structural without disclosing the evasion window, which misleads downstream safeguarding and UX
  decisions (e.g., whether supervisors need session-age visibility).
- Fix: Do not change behavior (source-confirmed). Add an explicit limitation note in §10: "Completed-session
  review is the oversight mechanism; an active session is reviewable only as status, and completion timing is
  student-controlled." Add it to §13 as an open product question (e.g., supervisor visibility of long-running
  active sessions) so architecture does not silently discover it.

### H3. NFR-9 broadens the source's rate-limit scope and the fixed IP limits collide with the stated deployment context

- Severity: high
- Location: §7.2 NFR-9/NFR-10/NFR-11, §2 Deployment context
- Attack: The source limits "every unauthenticated **API** endpoint" (docs/product-requirements.md:1550-1552);
  NFR-9 says "Every unauthenticated endpoint". Static file serving (NFR-8) is unauthenticated — under NFR-9 as
  written, SPA asset loads fall under a 60 req/min/IP token bucket with burst 30, which breaks first page load
  for any frontend with >30 assets and is almost certainly not intended. Separately: the limits are fixed and
  not operator-configurable (NFR-11), yet the primary deployment story is schools, where an entire class sits
  behind one NAT IP — 30 login failures/IP/15 min and 10 recovery requests/IP/hour shared by a whole school is
  a plausible morning-login outage. That collision between §2's deployment context and §7.2's fixed limits is
  never acknowledged.
- Fix: Restore the word "API" in NFR-9. Add a deployment note (or §13 open question) on shared-NAT
  environments: either document trusted-proxy per-client IP derivation (NFR-7) as the mitigation the operator
  must configure, or flag fixed IP limits vs school NAT as a known launch risk.

---

## Medium

### M1. Selectability of link-only material at session start is omitted, inviting the wrong implementation

- Severity: medium
- Location: §6.9 FR-51, §6.10 FR-54/FR-57
- Attack: FR-51 says link material "provides no retrievable source content and does not satisfy course
  activation or session-start readiness"; FR-54 constrains "any selected source-backed material". Whether a
  student may select link-only material at session start — and whether its identity/brief enters FR-57's
  initial context — is left to inference, and FR-51's "does not satisfy session-start readiness" nudges readers
  toward "not selectable". The source decides this: link-only material may be selected and supplies identity
  and brief context (docs/product-requirements.md:1070-1073). The PRD dropped a confirmed decision.
- Fix: Add to FR-54: "Link-only material may be selected; it contributes identity and brief context (FR-57)
  but no retrievable content and does not count toward source-backed readiness."

### M2. Invitation-preview responses for revoked/faulty/accepted/unknown tokens are undefined — an existence-leak surface

- Severity: medium
- Location: §6.2 FR-11, §7.1 NFR-2
- Attack: FR-11 defines what a valid pending-invitation preview reveals. Nothing defines what preview returns
  for a revoked, faulty, accepted, or never-issued token. If those states are distinguishable, a token holder
  (or brute-forcer within NFR-10's 10-per-digest budget) learns invitation lifecycle facts, and NFR-2's
  "out-of-scope resources behave exactly like unknown resources" gives no guidance because an invitation token
  is the possession credential itself. The source is also silent, so this is a genuine undecided point — but it
  is security-relevant and absent from §13, so downstream will guess.
- Fix: Add to §13 open questions: "Preview/acceptance response shape for non-pending tokens (revoked, faulty,
  accepted, unknown) — must define which states are mutually indistinguishable."

### M3. NFR-2's blanket "indistinguishable responses" is untestable as written and conflicts with mandated 429 throttling

- Severity: medium
- Location: §7.1 NFR-2 vs §7.2 NFR-10
- Attack: NFR-2 requires that "out-of-scope resources, unknown usernames, banned accounts under recovery, and
  rate-limited public recovery all produce indistinguishable responses" — one equivalence class across four
  unrelated situations. The source mandates login return `429` with `Retry-After` during progressive delays
  (docs/product-requirements.md:1563-1564), which is by definition distinguishable from a non-throttled
  response. The real invariant is narrower: responses must not vary **by resource existence** within the same
  endpoint and throttle state (the source states this precisely for recovery: identical when "absent, a limit
  applies, or email is sent" — :1569-1570). As written, NFR-2 cannot be accepted verbatim and a literal test
  would fail correct implementations.
- Fix: Restate NFR-2 as: "Within any public endpoint, response status, body, and timing behavior must not
  depend on whether the referenced account/resource exists or is in scope; recovery responses are additionally
  identical across absent/limited/sent." Drop the cross-endpoint equivalence claim.

### M4. Start of the 12-hour absolute session lifetime is ambiguous for MFA-only logins

- Severity: medium
- Location: §6.5 FR-22/FR-24
- Attack: FR-22 caps sessions at "12 hours after authentication". FR-24 says completing password replacement
  "starts a new authenticated-session lifetime" — but says nothing about the MFA-only path. Does the 12-hour
  clock start at password verification (entering the `mfa` stage) or at MFA completion? Implementations differ
  by up to 30 minutes of restricted-stage time, and acceptance scenario 24 cannot be tested without the answer.
- Fix: One sentence in FR-24: "Completing the final stage (MFA when no password replacement is required)
  starts the authenticated-session lifetime," or explicitly state the lifetime anchors at password success.

### M5. "Finalization freezes the file set" contradicts the faulty-material repair flow

- Severity: medium
- Location: §6.9 FR-48
- Attack: FR-48 states finalization freezes the file set, then three sentences later says the uploader
  "removes or replaces the failed file and may re-finalize". The mutable surface of the failed state is
  undefined: may the uploader also add new files? Edit only failed files? Return to full draft? The declared
  state machine (draft → processing → ready or failed) does not say whether "failed" is editable or transitions
  back to draft. Two materially different implementations satisfy the text.
- Fix: State the failed-state contract explicitly, e.g., "A failed material returns to an editable state in
  which files may be removed, replaced, or added before re-finalization; the freeze applies only from
  finalization until a terminal processing outcome."

### M6. Cross-course blast radius of global student-profile edits is never surfaced as a decision

- Severity: medium
- Location: §6.7 FR-34, §6.11 FR-66, §6.13 FR-74, §4 authorization principles
- Attack: §4 promises "a global role does not broaden course or student scope", yet FR-34 lets a supervisor
  from any one shared course edit **global** student attributes — per-student AI instructions (feeding FR-57
  tutor context in every course), `mentoring_requests_allowed` (gating requests in every course per FR-66),
  language, email, mobile — and FR-74 lets any single course's supervisor ban the account globally. This is
  source-confirmed behavior, but the PRD states "Changes affect the global profile" in passing without naming
  the consequence: supervisor A of course X materially alters tutoring and mentoring behavior inside course Y
  supervised by B. Authorization design and UX (edit warnings, audit expectations) will trip over this.
- Fix: Add one explicit sentence to FR-34 (and cross-reference from §4): "Profile edits and bans are global
  account effects authorized by any single shared course; they intentionally affect the student's other
  courses." Keeps behavior unchanged, removes the silent collision with §4's scope principle.

### M7. Model-behavior "must" requirements have no acceptance mechanism

- Severity: medium
- Location: §6.11 FR-65, §6.9 FR-49, §10
- Attack: FR-65 ("the AI tutor **must** first attempt clarifying questions, alternative explanations, examples,
  guided exercises, and relevant authorized material, and may suggest mentoring only when those fail") and §10
  ("must never state or imply that anyone was notified") are hard requirements on stochastic model output.
  Neither is deterministically enforceable or testably acceptable as written; FR-49's "ungroundable briefs fail
  visibly" implies a grounding-verification mechanism that is nowhere characterized (validator? model
  self-report? schema check?). The 32 acceptance scenarios are declared the verification basis (§12), yet
  scenarios 6 and 19 depend on these unverifiable behaviors.
- Fix: Reclassify these as prompt-level steering requirements with a stated enforcement boundary: which parts
  are mechanically enforced by MIA (e.g., mentoring suggestion gated by FR-66 preconditions, brief schema
  validation) versus best-effort prompt constraints verified by sampled evaluation. Downstream cannot otherwise
  write acceptance tests.

---

## Low

### L1. "Session" terminology collision between authentication sessions and tutoring sessions

- Severity: low
- Location: §11 scenarios 24 vs 29; §6.5 vs §6.10
- Attack: Scenario 24 "Sessions expire after 30 idle minutes" and scenario 29 "A session stays active through
  extended inactivity" use the same bare noun for opposite behaviors. Correct on context, but a hurried reader
  of the scenario list alone sees a contradiction.
- Fix: Qualify every occurrence: "browser sessions" / "tutoring sessions".

### L2. Username grammar under-specified for consecutive interior separators

- Severity: low
- Location: §6.1 FR-1
- Attack: Is `j..doe` or `a-_b` valid? FR-1 constrains endpoints and the interior character set but not
  separator runs. Validators will differ across backend and frontend teams (separate repos).
- Fix: State whether consecutive separators are allowed.

### L3. "Authenticated user actions" that reset the idle timer are undefined

- Severity: low
- Location: §6.5 FR-22
- Attack: Does an idle SSE stream, a read poll, or any GET count as a user action? The frontend team (separate
  repo) must know what keeps a session alive to build correct expiry UX; "server heartbeats … do not" hints but
  does not define the positive set.
- Fix: Define the rule (e.g., "any authenticated API request initiated by user interaction; SSE keepalives and
  automatic polling excluded" — or simply "any authenticated API request except SSE stream reads").

### L4. Role bundle created by accepting an administrator invitation is unstated

- Severity: low
- Location: §6.2 FR-12
- Attack: FR-12 defines supervisor acceptance (supervisor + student) and mentor acceptance (mentor only) but
  not administrator acceptance. Admin-only is the obvious inference (admins get no student role per §4/FR-44
  logic), but the asymmetric enumeration invites the question.
- Fix: Add "an administrator invitation creates only the account and permanent administrator role."

### L5. Whether the supervisor-corrected or original summary feeds the next session's tutor context

- Severity: low
- Location: §6.10 FR-57 vs FR-63
- Attack: FR-63 makes supervisor corrections first-class; FR-57 injects "the previous completed session's
  summary and follow-up" into tutor context. Corrected or original? The source is also silent
  (docs/product-requirements.md:1097-1098), so this is a genuine undecided point — but it directly affects G2
  (corrections steering future tutoring) and belongs in §13 rather than left implicit.
- Fix: Add to §13 open questions.

---

## Notes (not findings)

- Addendum tension #4 says course-deactivation preconditions are "unstated", but
  docs/product-requirements.md:742-751 states them and FR-41 reflects them correctly; the addendum entry is
  stale and should be dropped or narrowed to whatever residual question remains.
- Open question #5 (interrupted-response retry eligibility) correctly captures the FR-61/FR-62 gap — good.
- Numeric-limit spot checks (avatar caps, SMS quotas, Crockford recovery codes, rate-limit figures, login
  progressive delays) all traced to the source; no invented numbers found beyond the NFR-9 scope-word drift
  (H3).
- The absent success metrics (§12) are self-flagged with an explicit PM note; treated as acknowledged, not a
  finding.

## Counts

| Severity | Count |
| --- | --- |
| Critical | 1 |
| High | 3 |
| Medium | 7 |
| Low | 5 |
| **Total** | **16** |
