# Reconciliation: README / docs index / AGENTS (product content) vs PRD + Addendum

Scope: `README.md`, `docs/README.md`, `AGENTS.md` (product-relevant content only) compared against
`prd.md` and `addendum.md`. Focus on qualitative content (positioning, voice, onboarding guidance,
differentiators, deployment framing, cost honesty) and on contradictions.

Verdict: **PASS WITH GAPS** — no material contradictions found; the FR structure silently dropped several
pieces of qualitative operating guidance and one piece of unsourced framing was added untagged.

---

## Findings (ordered by severity)

### F1 — MEDIUM (dropped qualitative guidance): "Adding students is not fire and forget" onboarding step is gone

- Source: `README.md:103-106` — **Step 4: make students familiar**: "While technically not required, this
  step is crucial: Make the students familiar with the tutoring platform. Integrate it into regular
  classes. Or conduct the first tutoring sessions under personal supervision. The younger the students
  are, the more initial help they will need. Adding students to a course is not fire and forget."
- PRD state: The six-step operating model is restated in §5 (User Journeys), but the journeys jump from
  UJ-3 (provisioning) straight to UJ-4 (student session). §2 claims the journeys "restate the product's
  documented six-step operating model", yet Step 4 — the only step that is pure human guidance rather
  than a feature — has no counterpart anywhere in the PRD.
- Why it matters: This is deliberate product voice about safe rollout for a population that may include
  minors. It aligns with §10 (Responsible AI / human oversight) and with the operator/supervisor
  responsibility framing. Dropping it makes the PRD read as if onboarding is purely technical.
- Affected sections: PRD §2 (Context), §5 (User Journeys), §10 (Responsible AI and Safeguarding).
- Suggested fix: Add the familiarization step to §5 (e.g., a short paragraph between UJ-3 and UJ-4 or as
  part of UJ-3) or to §10 as an adoption/oversight expectation, preserving the "not fire and forget"
  intent (age-dependent initial help, integration into regular classes, first sessions under personal
  supervision).

### F2 — LOW-MEDIUM (dropped qualitative guidance): Course-scoping guidance dropped

- Source: `README.md:59-61` — Step 1: "The course must be aligned with the school curriculum, and it
  should not cover more than the school year."
- PRD state: UJ-1 and §6.8 define course mechanics (states, activation, uniqueness) but carry no guidance
  on intended course granularity or curriculum alignment at creation time.
- Why it matters: This is a differentiating positioning point (curriculum-anchored, bounded scope) that
  shapes how administrators and supervisors are expected to model courses. Without it, nothing in the PRD
  discourages one giant multi-year course.
- Affected sections: PRD §5 UJ-1, §6.8 (Courses); could also live in §2.
- Suggested fix: Add the alignment/one-school-year guidance as narrative in UJ-1 or as a non-normative
  note in §6.8.

### F3 — LOW (dropped qualitative guidance): Material-preparation quality guidance dropped

- Source: `README.md:69-70` — Step 2: "Ideally, the entire text book using in class is uploaded.
  High-quality scans are preferred."
- PRD state: UJ-2 says Sofía "uploads the class textbook as PDF" (partial coverage of the ideal case),
  but the explicit guidance — upload the whole textbook, prefer high-quality scans because OCR quality
  drives tutoring quality — appears nowhere in §6.9 or the journeys.
- Why it matters: OCR-dependent material quality directly affects the core value proposition; the README
  treats this as operator-facing success guidance.
- Affected sections: PRD §5 UJ-2, §6.9 (Learning material).
- Suggested fix: One sentence of guidance in UJ-2 or a note attached to FR-48/FR-50.

### F4 — LOW (unsourced framing, untagged): Supervisors equated with teachers

- Source check: Neither `README.md` nor `AGENTS.md` states that supervisors are teachers. AGENTS.md
  vocabulary rules require using "supervisor" consistently for course responsibility and warn against
  introducing another role for a person's relationship to the course outside MIA.
- PRD state: §1 says supervisors are "typically the student's own teachers"; §2 opens with "Teachers
  (supervisors in MIA) have the right material". These are plausible marketing inferences but are not
  sourced and are not tagged `[ASSUMPTION]`, unlike the journey framing in §5, which is tagged.
- Why it matters: Supervisors may be tutors, homeschool parents, or training staff; asserting "teachers"
  narrows the audience beyond what the sources commit to, and the PRD elsewhere claims everything
  unsourced is tagged.
- Affected sections: PRD §1 (Executive Summary), §2 (Context and Problem).
- Suggested fix: Either tag the teacher framing as `[ASSUMPTION]` or soften to "typically school staff or
  other educators responsible for the class".

### F5 — LOW (positioning nuance thinned): "Not a self-learning platform" framing only partially carried

- Source: `README.md:16-18` — "MIA is not a self-learning platform, and it does not come with any
  prepared material, courses, or curricula. An administrator must create each course and assign its
  supervisors. The supervisors are responsible for preparing the course content."
- PRD state: The non-goal "No predefined courses, curricula, or learning material shipped with the
  product" (§3) covers the second half. The first half — MIA is explicitly *not* a self-learning
  platform, i.e., it is unusable without an institution-side preparation effort — is a stronger
  positioning statement than "no bundled content" and is not restated.
- Why it matters: This sets buyer/operator expectations (setup effort is a feature, not a deficiency) and
  is a differentiator versus consumer self-study apps.
- Affected sections: PRD §1, §3 (Non-Goals).
- Suggested fix: Add "MIA is not a self-learning platform" (with the human-preparation dependency) to §1
  or as the first non-goal.

### F6 — INFO (adequately covered; recorded for completeness)

Verified present, no action needed:

- Licensing/cost honesty: "MIT-licensed but not free to run" plus the paid-provider list — PRD §1 and §8
  (provider table) match `README.md:23-31`.
- Deployment framing: self-hosted Linux + browser, single dependency-free binary, mandatory HTTPS behind
  a reverse proxy, no TLS listener, SQLite + data dir as one backup unit, reliable internet required —
  PRD §2 (Deployment context), G4, NFR-8, NFR-14 match `README.md:20-21,154-175`.
- Provider-data transparency and "self-hosting is not fully local" — PRD §8 matches AGENTS security
  baseline and README link-metadata/no-fetch rules (FR-51, non-goals).
- Operator responsibility, no guardian accounts / age verification / consent workflow — PRD §3 non-goals
  and §10 match `README.md:41-45`.
- Copyright/attestation and malware honesty — PRD §3 non-goals match `README.md:47-49` and AGENTS.
- Safeguarding voice (not an emergency service, no monitoring claim, supportive response, trusted-person
  guidance) — PRD §10 matches `README.md:144-148` and AGENTS.
- Role capabilities in `README.md:177-234` (admin/supervisor/mentor/student bullets, time-zone handling,
  student vs staff recovery, immutable staff email, permanent staff roles) all map to PRD §4, §6.2-§6.7,
  §6.13; no contradictions found.
- Vocabulary: "AI tutor is not a user role", supervisor/mentor/student definitions, union-of-permissions,
  authorization-beyond-role-flags — PRD §4 matches AGENTS.
- Docs authority ordering and "silence is not a product decision" — PRD §1, §13, §15 match
  `docs/README.md:36-48`.
- Mentoring-as-last-resource, disabled entry points without an assigned mentor, mentor removal vs
  reassignment — PRD §6.11 matches README Step 5 and AGENTS.
- Supervisor testing a course as student via ordinary membership — FR-44 matches `README.md:97-98`.

## Contradiction sweep result

No wrong role capabilities, wrong vocabulary usage, or scope errors were found between the PRD/addendum
and the three source documents. The closest item is F4 (unsourced "teachers" framing), which is a framing
addition rather than a capability contradiction.
