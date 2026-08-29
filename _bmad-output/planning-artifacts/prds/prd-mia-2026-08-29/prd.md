---
title: "MIA — AI-Powered Tutoring Platform (MVP)"
status: final
created: 2026-08-29
updated: 2026-08-29
---

# MIA — AI-Powered Tutoring Platform: Product Requirements Document

## 1. Executive Summary

MIA ("mi inteligencia artificial") is a self-hosted, browser-based learning platform in which an AI tutor conducts
personal, custom-tailored tutoring sessions grounded in the learning material actually used at the student's school.
Unlike platforms with a predefined curriculum, MIA ships no content of its own and is not a self-learning
platform: tutoring is based on the curriculum, learning goals, educational approach, and materials selected by
the course supervisors — in a school deployment, often the student's own teachers.

The product ships as a single dependency-free Linux binary (backend/API) plus a separately distributed frontend,
stores everything in SQLite and a local data directory, and integrates external AI providers (OpenAI for tutoring
and summarization, Mistral for OCR) plus SMTP, and optionally ClickSend (SMS) and ElevenLabs (text-to-speech).
It is MIT-licensed but not free to run: operators pay for the external provider services.

MIA processes sensitive educational data and may serve minors. Security, privacy, and responsible-AI constraints
are product requirements in this document, not optional hardening.

This PRD is the launch-grade MVP contract. It consolidates the repository's confirmed product behavior
(`docs/product-requirements.md` is the authoritative source) into PM-consumable requirements. Where repository
documents are silent, this PRD treats the behavior as undecided — silence is not a product decision.

## 2. Context and Problem

Students preparing for current classes and exams get little value from generic tutoring platforms whose content
does not match their school's curriculum, pacing, or teaching approach. Course supervisors — in practice usually
teachers — have the right material (textbooks, worksheets, exams) but no safe, supervised way to turn it into
personalized AI tutoring.

MIA closes that gap with a human-in-the-loop pipeline:

1. Supervisors upload and approve the actual class material; an AI-generated, supervisor-corrected brief describes
   each material.
2. Students hold tutoring chat sessions in which the AI tutor works only from authorized material and
   supervisor-defined instructions.
3. Supervisors review completed sessions, correct summaries, and fine-tune instructions per course and per
   student.
4. Human mentors are available as a deliberate last resource when AI tutoring is not enough.

Improved marks are an intended benefit, not a guaranteed outcome.

### Deployment context

- Self-hosted on a Linux server, used through a browser. Not a desktop application.
- HTTPS is mandatory and terminates at an operator-managed reverse proxy; MIA exposes no TLS listener.
- The operator (the person or organization running the server) is responsible for lawful operation, user
  eligibility, required consent, and local and institutional policies. The MIA project and authors do not operate
  third-party deployments.
- Requires reliable internet: the server continuously calls external LLM APIs.

## 3. Goals and Non-Goals

### Goals

- G1. Deliver curriculum-anchored AI tutoring: sessions grounded exclusively in supervisor-approved course
  material and student-owned private material, under supervisor-defined instructions.
- G2. Keep humans in control of the AI: supervisor-reviewed material briefs, revocable approval, completed-session
  review and correction, per-course and per-student instruction tuning.
- G3. Provide safe, scoped multi-role collaboration: administrators, supervisors, mentors, and students with
  strict course-, student-, and ownership-scoped authorization.
- G4. Be operable by a single self-hosting operator: one binary, one consistent data set (SQLite + data
  directory), no external database, provider outages degrade features rather than the platform.
- G5. Protect sensitive educational data of a population that may include minors: existence-hiding responses,
  bounded inputs everywhere, content-free audit records, explicit provider-data transparency.

### Non-Goals (confirmed exclusions)

- No predefined courses, curricula, or learning material shipped with the product.
- No public sign-up or self-registration of any kind.
- No guardian accounts, age verification, or guardian-consent workflow (operator responsibility).
- No guarantee of academic results.
- No emergency service, automated safeguarding alerts, or real-time human monitoring.
- No malware scanning and no claim that accepted files are malware-free.
- No copyright or license validation and no uploader attestation.
- No server-side fetching of external material URLs; model output can never trigger network retrieval.
- No data-export feature.
- No workflow notifications, subscriptions, quiet hours, or workflow SMS in the initial release.
- No live mentor chat, audio, or video; no attendance or no-show tracking.
- No monetary budgets or provider spending limits; usage counters are operational, not billing totals.
- MVP-scoped exclusions: no server-side browser-session records or remote session revocation; no cookie
  revocation after staff password reset; staff email immutable; ready material cannot be reopened; no
  application-layer field encryption; no separate authenticated tutoring/upload/finalization/speech rate limits;
  students cannot edit their own profile fields.

## 4. Users, Roles, and Authorization Model

### Roles

- **AI tutor** — the model-driven tutoring behavior. Not a user role.
- **Administrator** — named global platform capabilities: manage course records, assign supervisors, invite
  administrators/supervisors or grant those roles, delete accounts and courses (per lifecycle rules), supervise
  background jobs, access the audit log. Administrator status alone grants no access to student-private material
  or course-scoped student records and no supervisor capabilities.
- **Supervisor** — responsible for assigned courses: prepare and activate courses, manage course-wide material
  and approvals, provision and manage students, invite and assign mentors, inspect student-private material,
  review completed sessions, correct summaries, manage student-account recovery and bans.
- **Mentor** — supports a specific student in a course: receives the mentoring topic, responds, and schedules
  a personal session held outside MIA. No access to private uploads, briefs, or chat histories.
- **Student** — receives tutoring in joined courses: conducts sessions, uploads and deletes private material,
  views own summaries, downloads approved course material, requests mentoring when allowed.
- **Operator** — not an in-app role. Installs and runs MIA; owns proxy/TLS, backups, provider accounts,
  eligibility, and consent.

### Authorization principles

- Authorization always considers the requested action, user role, course assignment, student assignment,
  resource ownership, and resource state. Role flags alone never suffice; possession of a resource ID never
  suffices.
- Multiple roles grant the union of permissions in the relevant scope; a global role does not broaden course or
  student scope.
- Any staff role (administrator, supervisor, mentor) makes the account "staff" for profile, password-recovery,
  MFA-recovery, ban, and deletion behavior. Student administration operations target student-only accounts.
- MIA must not reveal whether an out-of-scope student, session, upload, invitation, or other sensitive resource
  exists. Out-of-scope resources behave exactly like unknown resources.
- Visibility: every user sees their own account; assigned supervisors see students and staff relationships in
  their courses; mentors see minimal identity (username, name, nickname, avatar) for explicitly assigned
  students only; students see no course
  roster; administrators see global account and relationship metadata only.

### Glossary

- **Staff** — an account holding any of administrator, supervisor, or mentor (see §4); staff rules govern
  profile, recovery, MFA reset, ban, and deletion behavior.
- **Student-only account** — an account with the student role and no staff role; the only kind that can be
  banned (FR-74) or supervisor-managed (FR-19, FR-32, FR-34).
- **Operator** — the person or organization running the server; a responsibility role, not an in-app role.
- **Course states** — `inactive` and `active` (FR-37); activation requirements in FR-39.
- **Material scope** — `course-wide` (supervisor-managed, approval-gated) or `student-private`
  (owner/tutor/assigned-supervisor access only); immutable per material (FR-45–FR-47).
- **Material states** — `draft` → `processing` → `ready` or `failed` (FR-48).
- **Approved** — the revocable supervisor flag on course-wide material that makes it student-visible and
  tutor-usable (FR-46).
- **File-backed** — material whose source content comes from uploaded files (as opposed to link-only material,
  FR-51); only ready, file-backed material yields retrievable content (FR-59).
- **Material brief** — the grounded structured summary of one material (FR-49). Distinct from the **course
  brief** and **student brief**, the fixed context bundles defined in FR-57.
- **Tutoring session** — the student's chat with the AI tutor; `active` until the owning student finishes it,
  then `completed` (FR-54–FR-55). Never expires from inactivity.
- **Browser session** — the authenticated cookie session (FR-22); expires after 30 idle minutes / 12 hours.
- **Restricted stage** — the limited `mfa` or `password-change` login cookie stage before full authentication
  (FR-24).
- **Security generation** — the per-account counter whose increment invalidates all existing browser cookies
  (FR-19, FR-23, FR-32).
- **Triage** — the supervisor queue for open mentoring work whose mentor was removed (FR-70) or whose request is
  not yet assigned (FR-68).

## 5. User Journeys

These journeys restate the product's documented six-step operating model. [ASSUMPTION] The named protagonists and
narrative framing are illustrative; the underlying steps and rules come from the repository documentation.

### UJ-1: Ana the administrator sets up a course

Ana bootstraps the first administrator account offline via the local CLI (server stopped), then logs in. She
creates the course "English, fifth grade" and assigns teacher Sofía as supervisor in the same operation. The
course starts inactive and is invisible to students. Ana never sees student-private uploads — she is not assigned
as a supervisor.

### UJ-2: Sofía the supervisor prepares and activates the course

Sofía scopes the course to the school curriculum — no more than the school year — and writes the course
description, learning goals, and AI tutor instructions ("guardrails" for all student chats). She uploads the
class textbook as PDF (ideally the entire textbook; high-quality scans give the best OCR and tutoring results).
MIA OCRs the material and generates a structured brief; Sofía reviews and corrects it, then approves the
material. With learning goals, instructions, a language, and at least
one approved, ready, file-backed material in place, she activates the course. Later she edits an approved brief —
approval is automatically revoked until she explicitly re-approves.

### UJ-3: Sofía provisions students

Sofía creates a student account for Mia (no email needed), choosing username and a temporary password, and hands
over credentials outside MIA. She adds per-student context and special AI instructions. At first login, Mia must
replace the temporary password before anything else. Sofía also adds an existing student by exact username — no
search, no autocomplete. Adding students is not fire-and-forget: Sofía plans onboarding — first sessions in class
or under personal supervision — knowing that the younger the students, the more initial help they need.

### UJ-4: Mia the student holds a tutoring session

Mia logs in, picks the course, and starts a session with just an intent: "Let's learn vocabulary." The AI tutor,
once the intent is clear, searches the authorized material, requests bounded excerpts from the approved textbook,
cites chapter and section, and runs exercises. Mid-session Mia uploads a photo of her homework as private
material; after processing it is usable without approval and visible only to her, the AI tutor, and assigned
supervisors. She finishes the session; MIA generates a summary of progress, strengths, weaknesses, and follow-up
suggestions. Her next session builds on it. She can resume the same active session from any device; she cannot
start a second one anywhere until she finishes the first.

### UJ-5: Marco the mentor handles an escalation

The AI tutor has tried clarifying questions, alternative explanations, examples, exercises, and relevant material
without success, and — because Marco is assigned to Mia in this course and requests are allowed — suggests
mentoring. Mia files a request with a proposed time. Sofía triages the unassigned request and assigns Marco.
Marco responds, confirms a schedule, and adds an external HTTPS meeting link. The session happens outside MIA;
Marco marks it completed after its scheduled time has begun.

### UJ-6: Sofía reviews and fine-tunes

Sofía reviews Mia's completed session — full chat history included — and corrects the generated summary (the
correction is attributed and audited; the chat itself is immutable). She sharpens the course-level and
student-level AI instructions for future sessions.

## 6. Functional Requirements

Requirement IDs are stable and globally numbered. Numeric limits stated here are confirmed product decisions
unless tagged otherwise. All FRs are launch-blocking contract behavior; features gated on optional providers —
SMS-dependent flows and text-to-speech — are required when the provider is configured and degrade to stable
unavailable errors when it is not (NFR-17).

### 6.1 Identity and account data

- FR-1. Usernames use 3–32 ASCII characters, start and end with a letter or digit, may contain interior `.`,
  `_`, `-`, preserve case for display, and compare/unique by ASCII lowercase.
- FR-2. Emails use a practical ASCII mailbox subset, max 254 characters after trimming, reject display names,
  comments, quoted local parts, and domain literals, and compare/unique (when present) by ASCII lowercase.
- FR-3. Every account requires username, password, preferred language (canonical BCP 47), country (ISO 3166-1
  alpha-2), and preferred IANA time zone. Staff additionally require a verified email. Student email is optional.
- FR-4. Optional profile text uses null as its sole absent representation. Names ≤100 Unicode code points,
  nicknames ≤24, student-specific AI instructions ≤4,000 code points and ≤16 KiB. Year of birth is an optional
  integer 1900..current UTC year (self-reported, not age verification). Mobile numbers use strict E.164.
- FR-5. Multiline profile and descriptive text is validated UTF-8, CRLF/CR-normalized to LF, trimmed, with NUL
  and control characters (except tab/newline) rejected. Passwords and chat messages are never trimmed or
  normalized.

### 6.2 Registration, invitations, and roles

- FR-6. There is no public sign-up. Staff registration is invitation-only; students are provisioned by assigned
  supervisors. Only an actor authorized for the intended role and scope may initiate account creation.
- FR-7. The first administrator is created by a one-time local `bootstrap-admin` command run while the server is
  stopped: interactive terminal only, available only while no administrator exists, never a public web route.
- FR-8. Invitations are single-use, carry no course or student scope, and register new accounts only. The invitee
  chooses username and password and completes required profile fields at acceptance; acceptance verifies the
  invitation email. Inviting an already-registered email is rejected (direct role grant applies instead).
- FR-9. Invitations never expire; they remain pending until accepted or revoked. A definite initial (or resend)
  SMTP failure makes an invitation terminally faulty (token invalidated, sanitized failure code recorded); an
  SMTP timeout leaves it pending and usable, logged, never retried automatically.
- FR-10. Invitation DELETE revokes a pending invitation, physically deletes a faulty one, and rejects accepted or
  revoked invitations. Resend rotates the token, invalidating earlier links. Any administrator manages
  administrator/supervisor invitations; a mentor invitation is manageable only by the inviting supervisor or an
  administrator. All transitions are audited.
- FR-11. Unauthenticated invitation preview reveals only that it is a MIA invitation and the invited role. To
  token holders, preview and acceptance treat revoked, faulty, accepted, and unknown tokens identically with one
  generic invalid-invitation response that reveals no lifecycle state.
- FR-12. Accepting a supervisor invitation creates the account with the supervisor and student roles in one
  transaction.
  Administrator and mentor invitations create only the account and their single permanent role. An acceptance
  rejected for an email-uniqueness conflict does not consume the invitation.
- FR-13. Additional permanent roles are granted directly only to registered staff accounts by user ID, take
  effect immediately without user approval, and require a verified email. Administrators grant administrator or
  supervisor; any supervisor grants mentor. Granting supervisor atomically grants student. Repeat grants are
  idempotent. A banned account must be unbanned first. Staff roles are permanent for the account's lifetime.
- FR-14. Invitation and password-reset bearer tokens are canonical lowercase UUID v4 values; only SHA-256 digests
  are persisted. Links use URL fragments (`/invitation#token=…`, `/password-reset#token=…`) so tokens stay out of
  access logs; the frontend strips the fragment before calling the API.

### 6.3 Student provisioning and course membership

- FR-15. A supervisor provisions a student account by choosing username and a policy-compliant temporary
  password, adds it to an assigned active course, and delivers credentials outside MIA. The student must replace
  the temporary password at first login before using any other authenticated feature.
- FR-16. Existing students are added to a course by complete username only — no global search or autocomplete.
  The account must already hold the student role; enrollment never grants roles. Unknown usernames and
  non-student accounts fail identically without disclosure. Adding a current member is idempotent. There is no
  pending-membership or student-acceptance workflow.
- FR-17. A removed student may rejoin an active course as a new membership; previously deleted course-scoped data
  is never restored.

### 6.4 Passwords and recovery

- FR-18. Passwords are 12–128 Unicode code points, ≤512 encoded bytes, valid UTF-8, with spaces and Unicode
  allowed and no character-class rules. Known-common passwords are rejected via a locally bundled list (exact
  comparison, no mutation rules, no external service). Passwords are never trimmed or altered. The policy applies
  to temporary passwords and cannot be weakened by operators.
- FR-19. Student recovery: any supervisor sharing an assigned course with a student-only account sets a new
  temporary password. Doing so immediately invalidates every existing browser cookie for that student; the
  student must log in and replace the password before other features. Password values never appear in logs or
  audit records.
- FR-20. Staff recovery: self-service by complete username; a single-use reset link goes to the verified email
  and expires after 30 minutes. Public responses never reveal account existence. New requests leave earlier
  unexpired links valid; one successful reset invalidates all remaining challenges. Banned accounts can neither
  request nor complete recovery, indistinguishably. Successful reset does not revoke other cookies in the MVP.
- FR-21. Definite SMTP failure invalidates a newly created reset challenge; SMTP timeout leaves it usable, logged,
  not retried. All recovery emails (and invitation emails) are English-only plain-text UTF-8 with sanitized
  headers and no HTML.

### 6.5 Authentication sessions

- FR-22. Browser sessions use one signed and encrypted stateless cookie; the MVP keeps no server-side session
  records. Sessions expire after 30 minutes of inactivity and no later than 12 hours after authentication. The
  12-hour maximum anchors at completion of the final login stage — the moment the full authenticated cookie is
  created — not at the initial password verification.
  Each successful authenticated HTTP request (including SSE establishment) resets only the idle timer;
  server-sent heartbeats and background provider work do not.
- FR-23. Account, role, assignment, ban, and password-gate state are reloaded from the database on every request —
  never trusted from the cookie. Ban and deletion take effect on the next request. A security-generation mismatch
  clears the cookie and rejects the request.
- FR-24. Login stages: password first; with an active MFA factor, a restricted `mfa` stage (only verification,
  recovery-code consumption, SMS resend for the bound challenge, and logout); then, when password replacement is
  required, a restricted `password-change` stage (replacement and logout only). MFA precedes password
  replacement. Restricted stages expire after 30 non-refreshing minutes; the cookie rotates at every stage
  transition. Completing password replacement starts a new authenticated-session lifetime.
- FR-25. Logout clears the cookie in the current browser only. Concurrent sessions on multiple devices are
  allowed, each with its own timers.

### 6.6 Multi-factor authentication

- FR-26. MFA is optional for every user and role; no role-based enrollment mandate exists. At most one active
  method per account: TOTP or SMS.
- FR-27. TOTP: SHA-1, six digits, 30-second steps, one adjacent step of clock skew, 20-byte secret; a time step
  succeeds only once per factor. SMS: uniformly random six-decimal-digit codes, 30-minute single-use expiry,
  enrollment/replacement requires ClickSend and a verified profile mobile; active login challenges use the
  factor's immutable enrolled destination snapshot even after profile-mobile change or removal.
- FR-28. Pending enrollment expires after 30 minutes; five failed verification submissions delete it. Login
  challenges last 30 non-refreshing minutes, may coexist across login attempts, and are invalidated by five
  failed submissions. SMS resends reuse the same code without extending expiry or resetting failure counts, and
  share durable limits: 60-second cooldown, 5 sends/hour, 10 sends/day per account and per destination, counting
  failed sends.
- FR-29. Enabling MFA issues 10 single-use recovery codes (16 characters, Crockford Base32, shown exactly once,
  stored only as non-reversible digests). No standalone regeneration: replacing the factor invalidates unused
  codes and issues a new set. Code values never appear in logs or audit.
- FR-30. Disabling or replacing MFA requires the current password plus a fresh proof from the current factor or a
  recovery code. The resulting `mfa-management` proof is opaque and single-use, expires after 5 minutes, is
  stored only by SHA-256 digest, and is consumed atomically with exactly one MFA mutation. A new factor must verify
  before activation; the old factor stays active until replacement succeeds.
- FR-31. Any password, MFA-configuration, ban-state, or account-state change invalidates all outstanding MFA
  challenges and management proofs for that user.
- FR-32. Lost-factor recovery: for student-only accounts, any supervisor sharing an assigned course resets MFA
  (removes the factor, invalidates recovery codes and all student cookies, forces password replacement after a
  fresh login). For staff, a different administrator performs the reset: the factor is removed, recovery codes
  are invalidated, password replacement is required at next login, and existing cookies are restricted to
  password replacement and logout. When exactly one administrator exists, a local `reset-admin-mfa` command
  (server stopped, interactive, exact-username confirmation) performs the same reset; this action is never
  exposed as a web route.

### 6.7 Profiles, avatars, and mobile verification

- FR-33. Staff edit their own name, nickname, language, country, time zone, avatar, and text-to-speech voice, and
  may remove their verified mobile (which invalidates pending mobile challenges and pending SMS factors but does
  not change an active SMS factor's destination). Username, email, roles, ban state, and password state are not
  writable via profile operations. Staff email is immutable in the MVP.
- FR-34. Student-only accounts cannot change any of their own profile fields. Any supervisor sharing an assigned
  course with a student-only account edits that student's non-security profile fields (name, nickname, year of
  birth, email, mobile, time zone, avatar, language, country, per-student AI instructions, text-to-speech voice,
  mentoring-request permission). Accounts holding any staff role are governed exclusively by FR-33 and FR-35,
  regardless of course enrollment. Profile edits are global account effects authorized by any single shared
  course — they intentionally affect the student's other courses — and every change is audited. A
  supervisor-entered student mobile is immediately verified and atomically invalidates pending mobile challenges
  and pending SMS factors.
- FR-35. Staff mobile changes require SMS confirmation to the new number: single-use codes, 30-minute expiry,
  five-failure invalidation, and the shared SMS resend limits (FR-28). A successful change — like removal —
  deletes pending SMS MFA enrollments and replacements but never moves an active SMS factor's destination. Codes
  are never returned by the API or written to logs or audit. Without configured SMS, self-service mobile changes
  are unavailable.
- FR-36. Avatars (and course logos) accept signature-validated JPEG or PNG only: encoded source ≤10 MiB,
  ≤40 decoded megapixels, ≤10,000 px per dimension, animation rejected. Images are orientation-corrected,
  metadata-stripped, aspect-preserved, resized to at most 512 px per dimension, and stored as one non-animated
  PNG. The untrusted source is never served. Avatar download inherits profile-view authorization; logo download
  inherits course-view authorization.

### 6.8 Courses

- FR-37. Courses have exactly two states, inactive and active — no archive. Course names are globally unique by
  NFC-normalized, Unicode-case-folded comparison. New courses start inactive and invisible to students.
- FR-38. Only an administrator creates a course, assigning one or more existing supervisors in the same
  transaction. Every course keeps at least one supervisor at all times; only an administrator removes a
  supervisor assignment, and removing the last one is rejected.
- FR-39. Activation requires learning goals, AI tutor instructions, a valid language, and at least one approved,
  ready, file-backed course-wide material. Only active courses accept new student memberships and new tutoring
  sessions.
- FR-40. An active course that loses its last approved, ready, file-backed material stays active but accepts no
  new tutoring sessions; existing sessions may finish without access to revoked material. Re-approval restores
  session starts without reactivation.
- FR-41. Any assigned supervisor deactivates an active course (administrators need a supervisor assignment).
  Deactivation immediately blocks new sessions and new memberships; running sessions may finish with
  still-approved material. Reactivation re-checks all activation requirements.
- FR-42. An assigned supervisor removes a student from a course only when the student has no active tutoring
  session there. Removal permanently deletes all of that student's course-scoped data (private material, sessions
  and chats, summaries, generated speech, mentoring records, mentor assignments, related jobs) while preserving
  the account and other-course data, keeping one minimal content-free audit event.
- FR-43. Only an administrator deletes a course, and only while it is inactive with no active tutoring sessions.
  Deletion removes all course-scoped data; accounts and other-course data remain.
- FR-44. Supervisors may join their own active course as ordinary students to test it (the supervisor role
  includes the student role). There is no separate preview mode and no inactive-course test session.

### 6.9 Learning material

- FR-45. Every material has one immutable scope — course-wide or student-private — and one supported format for
  all of its files. Material names are unique within a course (same normalization scheme as course names).
  Accepted formats, exclusively: PDF, JPEG, PNG, UTF-8 plain text, UTF-8 Markdown, and DOCX without macros.
  A file extension alone never causes a file to be accepted; encrypted or password-protected documents are
  rejected and MIA never
  handles document passwords. PDFs are pre-validated and rejected for encryption, malformed cross-references,
  embedded files, JavaScript, or launch actions; DOCX is parsed as untrusted with entities and external
  relationships disabled, ignoring macros, comments, deleted tracked changes, hidden text, and embedded objects.
- FR-46. Course-wide material is managed by assigned supervisors and carries a revocable approval flag. Approval
  requires ready state and a non-empty brief. Only approved material is visible to course students or usable by
  the AI tutor. Changing the validated brief content of approved material atomically revokes approval and clears
  attribution; an unchanged submission does not. The revised brief needs explicit re-approval.
- FR-47. Student-private material requires at least one uploaded file, needs no approval, and is accessible only
  to the owning student, the AI tutor, and supervisors assigned to the course. Mentors, other students, unrelated
  supervisors, and non-assigned administrators have no access. Private material can never become course-wide.
- FR-48. Material processing: draft → processing → ready or failed. Finalization freezes the file set and creates
  one extraction job per file in a single transaction. PDF/PNG/JPEG go to Mistral OCR; DOCX, text, and Markdown
  use bounded local parsers. Extracted content is stored as strict per-file `content.jsonl`. Any failed file
  makes the whole material faulty — it is never presented as complete with a file silently excluded. A failed
  material returns to the editable draft state when the uploader removes or replaces a source file, and may be
  re-finalized after automatic retries are exhausted; the file-set freeze applies only from finalization to a
  terminal processing outcome. Ready material is immutable in the MVP (delete and recreate to change).
- FR-49. Each material gets one grounded, structured brief (summary, subjects, learning goals, section outline
  with search terms, warnings; ≤256 KiB). Per-field limits (Unicode code points): summary required, 1–4,000 and
  ≤16 KiB; subjects 1–20 items × ≤100; learning goals ≤30 × ≤300; sections ≤500 entries, each with required
  title ≤200, nullable label ≤100, required description 1–500, and ≤10 search terms × ≤50; warnings ≤20 × ≤500;
  nullable educational level ≤100. Briefs must not invent learning goals, levels, locations, or content;
  ungroundable briefs fail visibly. OCR page positions are never presented as printed page numbers. Course-wide
  briefs are drafts for supervisor review and correction; regeneration must never silently overwrite supervisor
  edits, and a supervisor-edited brief on ready material is never regenerable through any exposed operation —
  deleting and recreating the material is the only path to a fresh generated brief.
- FR-50. Upload limits are operator-configurable within fixed hard caps (defaults/caps): single file 100/512 MiB;
  combined material 200/512 MiB (never below the single-file limit); pages 1,000/2,000; files per material
  200/200; decoded image 40/100 megapixels plus a fixed 20,000 px dimension cap. Each PDF page and each JPEG/PNG
  file counts as one page; DOCX, text, and Markdown files are non-paged (byte and expansion limits apply). DOCX
  archives are bounded (≤10,000 entries, ≤512 MiB expansion, ≤100 MiB/entry, ≤100:1 ratio). Oversized uploads are
  rejected before any content reaches a provider. Nothing is unbounded.
- FR-51. Website and YouTube material stores HTTPS link metadata only (≤2,048 ASCII bytes, no credentials;
  YouTube restricted to recognized YouTube hosts). It is course-wide only, requires a supervisor-authored brief
  to finalize, becomes ready without jobs, is visible to students when approved, but provides no retrievable
  source content and does not satisfy course activation or session-start readiness. MIA never fetches external
  URLs server-side.
- FR-52. A student deletes their own private material: source files and brief are removed and future retrieval is
  blocked, while completed chats, session summaries, and a content-free audit record are preserved (historical
  quotes may remain sourceless). Material selected by an active tutoring session cannot be deleted until the
  session completes.
- FR-53. Material and file downloads are authorized per request, served with safe media types and download
  headers, and uploaded/generated files live outside the public static root.

### 6.10 Tutoring sessions

- FR-54. A student starts a session in one joined, active course, optionally selecting one or more materials or
  starting with intent only. Selected course-wide material must be approved; any selected source-backed material
  must be ready and file-backed; selected private material must belong to the student and the course. Link-only
  material may also be selected: it contributes identity and brief context (FR-57) but no retrievable content and
  does not count toward source-backed readiness. One active session per student across all courses and devices;
  starting another is rejected with instruction to finish the active one.
- FR-55. Sessions never expire from inactivity. The student resumes the same active session after logout, browser
  close, network loss, or device change. Finishing is the student's only way to end a session; only the owning
  student can finish it, and never while a response is generating or queued. Supervisors and administrators
  cannot force completion.
- FR-56. Session creation and message submission carry client-generated UUID v4 request IDs with strict
  idempotency: identical replay returns the existing resource, and the same ID with different content is a
  conflict. After session completion, message replays remain read-only, but any reuse of the session-creation ID
  is a conflict. Request-ID history exists only while the owning data is retained.
- FR-57. The AI tutor's initial context, in fixed order: global tutor instructions; the course brief (name,
  description, curriculum context, learning goals, language) with the course AI instructions; the student brief
  (nickname, year of birth, language, country) with the per-student AI instructions; identity, brief, and content
  outline of selected material; and the previous completed session's summary and follow-up for the same student
  and course (the supervisor-corrected version when one exists). Complete selected-material content is inlined
  only when ≤8,000 code points and ≤32 KiB and within budget — never all material at start.
- FR-58. Fixed budgets per tutor request: 32,000-token input, 2,048-token output. Required instructions and the
  current message take priority; the newest complete turns fill the remainder; older turns are dropped with no
  hidden rolling summary.
- FR-59. Material retrieval: once intent is clear, the tutor may search and request bounded excerpts from ready,
  file-backed, approved course-wide material and ready, file-backed private material owned by the active student
  in the session's course. MIA authorizes every retrieval — a model tool request never grants access. Per
  response: ≤3 retrieval rounds, ≤8 excerpts, each ≤4,000 code points and ≤16 KiB. Search is local and
  index-free with normalized Unicode terms and deterministic ranking. MIA records material as used only when
  content actually reaches the tutor model.
- FR-60. Student messages are ≤8,000 code points and ≤32 KiB. Each session has at most one generating response
  and one queued message; excess submissions are rejected until a slot frees. A queued message starts
  automatically after the current response reaches a terminal state, with any preserved partial text becoming
  conversation context. Responses stream incrementally; streamed text is persisted at bounded intervals so a
  crash loses at most the latest small window.
- FR-61. Client disconnect does not cancel generation; reconnecting resumes the same stream without text loss or
  a duplicate model response. The student may stop a generating response (provider call cancelled, received text
  preserved, response marked interrupted) or a queued one before provider work starts. Provider failure preserves
  partial text, marks the response failed, and is shown to the student — never retried automatically.
- FR-62. Explicit retry of a failed or interrupted response is allowed only while the session is active and
  idle; it creates a new response linked to the same message without rewriting history. Completed sessions
  accept no new messages or retries but still serve idempotent replays.
- FR-63. Completing a session atomically queues summary and follow-up generation covering strengths, weaknesses,
  and suggested next steps. The student sees their own summaries. Assigned supervisors see active-session status
  (including the session's start and last-activity instants) but can read chat messages only after completion; then they may inspect the full chat and private material
  used, and correct only the summary and follow-up (attributed and audited; chat immutable). Supervisors may
  request summary regeneration only when a completed session has no summary, no queued or running summary job,
  and a terminally failed earlier attempt; corrections are never silently overwritten. When used material was
  deleted before summarization read its identity, the summary omits that material's name and type.
- FR-64. On startup MIA resumes queued responses and marks stranded generating responses failed with a safe
  restart code — it never re-issues an uncertain provider request. Graceful shutdown starts no queued work, gives
  running generation up to 30 seconds, then cancels and marks it failed; queued work is preserved for startup.

### 6.11 Mentoring

- FR-65. Mentoring is the last resource: the AI tutor must first attempt clarifying questions, alternative
  explanations, examples, guided exercises, and relevant authorized material, and may suggest mentoring only when
  those fail.
- FR-66. New mentoring suggestions and requests require at least one mentor assigned to the student in the active
  course; with none, all new student-facing mentoring entry points are disabled while existing records stay
  visible and cancellable. The per-student `mentoring_requests_allowed` flag independently gates new requests
  without touching assignments or existing work.
- FR-67. Assignment flow: any assigned supervisor adds a registered mentor to the course and assigns course
  mentors to students. Assignments are immediate, need no mentor acceptance, and mentors cannot reject or remove
  them. A mentor needs both course and student assignment before receiving that student's mentoring work, and
  receives only the mentoring topic — never uploads or chat history.
- FR-68. Requests are created unassigned (optionally with a proposed time); an assigned course supervisor selects
  one of the student's assigned mentors before response or scheduling continues. The assigned mentor responds,
  confirms or changes the schedule, and provides meeting details or an external HTTPS meeting link (metadata
  only, never fetched or validated).
- FR-69. Cancellation: student or assigned supervisor cancels an unscheduled request; student or assigned mentor
  cancels a future scheduled session. Rescheduling by student or assigned mentor is direct, without acceptance,
  recording actor and previous/new times. Only the assigned mentor marks a session completed, and only after its
  scheduled time has begun. No attendance or no-show tracking exists.
- FR-70. Removing a mentor drops their student assignments in that course and returns their open work to
  supervisor triage with mentor, times, and meeting details cleared — topic and any immutable prior response
  preserved — all in one transaction. Direct reassignment to another assigned mentor is distinct: it preserves
  schedule, meeting details, prior response, and response authorship.
- FR-71. Course deactivation blocks new mentoring requests but not triage, responses, rescheduling, cancellation,
  or completion of existing work.

### 6.12 Text-to-speech

- FR-72. Speech is generated on request for completed tutor responses only (failed or interrupted text is
  ineligible), produced and served as MP3 (≤25 MiB, signature-validated, atomically published). It requires a
  stored ElevenLabs voice ID; without configured ElevenLabs the feature returns a stable unavailable error.
- FR-73. Generated speech is retained for a configured number of days (default 30) from generation; access does
  not
  extend retention; expired speech is deleted and may be regenerated on request. Cached speech is reused only
  while the source response content and requested voice match; concurrent requests share one generation; failed
  generations retry only on explicit request.

### 6.13 Bans

- FR-74. Only student-only accounts can be banned, by a supervisor sharing an assigned course. A ban rejects new
  logins and the next request on existing cookies, but does not cancel provider or background work already in
  flight. Banned accounts cannot request or complete password recovery, indistinguishably (FR-20). A banned
  account must be unbanned before receiving a staff role (FR-13). Unbanning is the same supervisor capability.
  Note: a banned
  student's active tutoring session cannot be finished while the ban lasts, so it blocks course membership
  removal (FR-42) until unban or account deletion (FR-76).

### 6.14 Account deletion and data lifecycle

- FR-75. Operational data is retained until authorized deletion; nothing expires automatically except generated
  speech. There is no data-export feature.
- FR-76. Only administrators delete student-only accounts. An account with any staff role is deleted only by a
  different administrator; deleting the last administrator or a sole course supervisor is rejected. Staff
  deletion atomically removes current assignments, triages open mentoring work, deletes the account and its
  student-owned data, and clears historical actor references. Account deletion proceeds regardless of active
  tutoring sessions; the sessions and their data are deleted with the account (FR-78 semantics).
- FR-77. Student deletion removes all operational data across all courses. Deletions keep only minimal,
  content-free audit records with random de-identified subject fingerprints. Deletion applies to live local data
  only; the operator controls provider-side data and backups, and MIA documents this limitation.
- FR-78. Destructive operations never coordinate with running workers or providers: late results update existing
  rows only; zero-row updates discard results and remove newly published output; nothing is upserted, requeued,
  or retried against deleted targets.

### 6.15 Audit

- FR-79. Administrators access the audit log. Audit events use stable identifiers with bounded, event-specific
  metadata and cover at least authentication failures, throttling, denied mutations, security changes,
  destructive actions, provider/job failures, invitation lifecycle, supervisor corrections, and local operator
  actions. Ordinary denied reads are not individually audited.
- FR-80. Audit content never includes passwords, codes, secrets, tokens, recovery-code values, mobile numbers in
  change events, message content, or deleted material content.

### 6.16 Time and localization

- FR-81. Every API instant uses RFC 3339 UTC with the `Z` suffix; instants are persisted in UTC. Every user has a
  preferred IANA time zone used for display and server-generated communications. One-time appointments are stored
  as single UTC instants and do not move when a user changes time zone; local-time schedules that must survive
  DST changes use separate local-time and IANA-zone fields.

### 6.17 Notifications

- FR-82. The initial release has no workflow notifications; users see current state when using MIA. Invitation
  email, recovery email, MFA codes, and mobile-verification codes are account/security deliveries, not
  notifications.

### 6.18 Background jobs and platform operations

- FR-83. Administrators inspect platform background jobs; supervisors see only safe processing status for
  assigned-course resources, without provider diagnostics. There is no generic job-retry operation; the only
  retry paths are the domain actions of material re-finalization and session-summary regeneration.
- FR-84. Background jobs retry automatically up to 3 attempts with bounded delays for transient failures only.
  Retries use current configuration and code; MIA promises no provider-side idempotency, but guarded commits
  prevent duplicate durable output. Job usage counters are cumulative, unattributed, and operational — not
  billing data.

## 7. Non-Functional Requirements

### 7.1 Security

- NFR-1. Every sensitive resource and field carries an explicit authorization rule; every download and material
  retrieval is authorized; resource IDs and route grouping never authorize anything.
- NFR-2. Existence-hiding is per endpoint: within any public endpoint, responses must not vary by whether the
  referenced account or resource exists or is in scope. Out-of-scope resources behave exactly like unknown
  resources; public password recovery responds identically whether the account is absent, the request is
  rate-limited, or an email was sent.
- NFR-3. Uploads, OCR output, external content, user messages, and model output are untrusted input. Model output
  never authorizes a platform action. Files are signature- and media-type-validated, never executed, and served
  with safe headers; no browser sniffing.
- NFR-4. Passwords are hashed with fixed-parameter Argon2id. Bearer tokens are stored only as SHA-256 digests.
  Secrets are accepted via configuration file or environment only, never CLI flags, and are redacted everywhere.
- NFR-5. The MVP applies no application-layer encryption to stored fields; TOTP secrets and active SMS codes rely
  on private data-directory and backup access. Anyone with storage access is fully trusted, and operators must be
  told so.
- NFR-6. Logs and audit records never contain passwords, hashes, MFA values or secrets, session tokens, API
  credentials, cookies, private prompts, message bodies, provider payloads, or unnecessary personal data.
- NFR-7. Client IPs are derived from `X-Forwarded-For` only when sent by configured trusted proxies, loopback
  peers, or the permission-controlled Unix listener, with bounded right-to-left parsing and safe fallback.
- NFR-8. HTTPS via reverse proxy is mandatory. Static file serving is GET/HEAD only, rejects symlink escapes,
  dotfiles, and listings, applies nosniff/referrer/frame protections and a strict baseline CSP (`default-src
  'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'`, loosened during frontend integration only
  when the frontend demonstrably requires it), and falls back to `index.html` only for
  unresolved non-API routes. Local bootstrap and sole-admin MFA reset are never web-reachable. Invitation and
  reset links are built only from the mandatory configured HTTPS public URL — never derived from request or
  forwarded headers.

### 7.2 Rate limiting and abuse controls

- NFR-9. Every unauthenticated API endpoint is rate limited (global 60 requests/minute per source IP, burst 30,
  plus stricter per-endpoint limits). Authentication-sensitive routes layer IP and account/identifier limits; neither
  dimension alone suffices, and limits never reveal resource existence.
- NFR-10. Confirmed limits include: login 5 failures per username and 30 per IP per rolling 15 minutes with
  progressive delays of 1, 2, and 4 seconds after the second, third, and fourth failure, returned as 429 with
  `Retry-After`; the fifth failure blocks until the rolling window drains — never a permanent lockout.
  Recovery: 3 per identifier and 10 per IP per hour; invitation
  preview/acceptance 10 per token digest and 30 per IP per hour; reset submission 5 per token digest and 20 per
  IP per hour; MFA and mobile verification 10 per account and 30 per IP per 15 minutes atop each challenge's
  5-attempt limit.
- NFR-11. Process limiter state is bounded to 50,000 expiring LRU keys; SMS limits are durable across restarts.
  Rate limits are fixed by MIA, not operator-configurable. The MVP adds no separate authenticated tutoring,
  upload, finalization, or speech limits.

### 7.3 Capacity and bounds

- NFR-12. Fixed AI budgets: 32,000-token tutor input and 2,048-token output per request, with a local tokenizer
  matching the configured model. Summarization uses ≤24,000-token chunks, ≤64 chunks, ≤2 reduction rounds; input
  that cannot be covered fails visibly (`summary_input_too_large`) rather than sampling or truncating.
- NFR-13. All user input, upload, excerpt, message, brief, content, image, and speech sizes are bounded per the
  functional requirements; no path accepts unbounded input. Non-upload request bodies are capped at 1 MiB.

### 7.4 Durability, consistency, and integrity

- NFR-14. SQLite plus the data-directory files form one consistent data set, backed up and restored together.
  Durable writes use WAL with full synchronous mode; files publish by atomic rename tied to their database
  commit; failed commits remove published files.
- NFR-15. Startup order: delete expired speech, fail stranded speech and tutor generation (removing incomplete
  output, never repeating uncertain provider requests), then fail hard if the database references a missing
  source file, processed `content.jsonl`, or unexpired available speech file. Missing avatars and logos are
  normal. Orphan files are reconciled away.
- NFR-16. Streamed tutor output is persisted at least every 16 KiB or 1 second and always before a terminal state
  commit, bounding crash data loss.

### 7.5 Availability and degradation

- NFR-17. MIA is a single-process, single-node system with no HA claim. External provider availability is never a
  startup prerequisite: startup validates provider settings locally, and a provider outage disables or fails only
  dependent operations, visibly — external failures never invent successful results.
- NFR-18. Request-path provider calls (tutor streaming, email, SMS, speech) use fixed deadlines and never retry
  automatically after ambiguous failure; users retry through normal authorized workflows.

### 7.6 Operability

- NFR-19. MIA ships one executable with `serve`, `bootstrap-admin`, and `reset-admin-mfa` subcommands. Every
  database-using command holds an exclusive OS lock on the data directory; offline commands run only while the
  server is stopped and validate only their required configuration subset without contacting providers.
- NFR-20. All configuration is validated at startup; any failure aborts before serving. Database migrations run
  automatically, forward-only, before use. Config changes require restart; SIGHUP only reopens the log file.
- NFR-21. Automated tests must never call paid or production providers.

## 8. External Providers and Data Disclosure

Self-hosting does not mean fully local processing. MIA must document, and this PRD confirms, exactly what leaves
the server. Local deletion never guarantees provider-copy deletion; provider retention is governed by the
operator's agreements.

| Provider | Role | Required? | Data sent |
| --- | --- | --- | --- |
| OpenAI | Tutoring and summaries | Yes | Instructions, messages, turns, material content/excerpts, briefs |
| Mistral | OCR of uploads | Yes | Bounded page content of uploaded PDF/image files |
| SMTP | Invitation/recovery email | Yes | Recipient address, plain-text message with link |
| ClickSend | SMS codes | Optional | Destination number, six-digit code, sender ID |
| ElevenLabs | Text-to-speech | Optional | Completed tutor-response text, voice ID |

MIA never sends passwords, password hashes, MFA secrets, cookies, or API credentials as provider content. Raw
provider payloads are never retained or exposed. DOCX, plain-text, and Markdown files are parsed locally and not
sent to Mistral.

## 9. Operator-Configurable Behavior

Operators tune the following user-visible behaviors within fixed hard caps (see FR-50 for upload details):
single-file and material size limits, page and file counts, decoded-image megapixels, and speech retention days
(1–365, default 30). The chat and job models are configurable; a model without a supported local tokenizer
mapping is a startup error, and OCR uses a fixed model. SMTP transport security is configurable but certificate
verification can never be disabled. The public HTTPS URL used in emailed links is mandatory configuration.

Deliberately fixed and not operator-configurable: rate limits, password and session policies, MFA/SMS policies,
provider timeouts and retries, accepted upload formats, upload hard maxima, and safeguarding behavior.

## 10. Responsible AI and Safeguarding

- MIA is not an emergency service and sends no automated safeguarding alerts. When a student discloses distress,
  the AI tutor responds supportively and non-diagnostically, may encourage contact with a trusted person or local
  emergency services, and must never state or imply that anyone was notified or is monitoring the conversation.
  The message remains in ordinary history under normal completed-session access. Mentoring is not an emergency
  channel; supervisors provide local guidance outside MIA.
- The tutor works only from authorized material; briefs and answers must not invent learning goals, levels,
  locations, or content, and OCR positions are never presented as printed page numbers.
- Human oversight is structural: supervisor-approved material, reviewable completed sessions, correctable
  summaries, and per-course/per-student instruction control. Its known limitation: an active session is visible
  only as status (with start and last-activity instants, FR-63), and completion timing is student-controlled, so
  chat review happens only after the student finishes.
- Enforcement boundary: MIA mechanically enforces the gates it controls — mentoring preconditions (FR-66),
  retrieval authorization (FR-59), brief schema validation with visible failure (FR-49), and all authorization
  rules. Conversational conduct requirements on the AI tutor (FR-65 and the safeguarding rules above) are
  prompt-level steering over stochastic model output; they are not deterministically testable and are verified
  through prompt review rather than automated acceptance tests.
- The operator is responsible for user eligibility and consent; MIA provides no age verification or guardian
  workflow and must not be marketed as doing so.

## 11. Acceptance Scenarios

The completed product must support at least these observable scenarios (condensed from the product contract).
Reference them downstream as AS-1 through AS-32.

1. An administrator creates a course with supervisors; without a supervisor assignment they cannot reach
   student-private material.
2. A supervisor prepares and activates a course only after goals, instructions, language, and approved material
   exist.
3. Revoking material approval immediately blocks student access and tutor retrieval.
4. A student uploads private material, uses it unapproved, and later deletes it without deleting completed chats.
5. Assigned supervisors can inspect that private material and session history; mentors and unrelated users
   cannot.
6. An intent-only session start leads the tutor to discover authorized material, request a bounded excerpt, and
   cite its location.
7. MIA rejects model requests for unapproved, non-ready, cross-course, or other-student material.
8. No second session can start while one is active — including from a second device.
9. Users in different time zones see the same instant rendered in their own zones.
10. Provider and processing failures stay visible; nothing ever looks successful when it was not.
11. No new mentoring request is possible without an assigned mentor; existing triaged work stays visible.
12. Disabling `mentoring_requests_allowed` blocks new requests without cancelling existing work.
13. Mentor removal returns open work to triage without losing the topic or the immutable prior response.
14. Unauthenticated throttling never reveals account or invitation existence.
15. Login failures cause temporary throttling, never permanent lockout.
16. Student deletion removes operational data everywhere; only a content-free audit record remains.
17. Course deletion removes course-scoped data; accounts and other-course data survive.
18. A supervisor corrects a session summary without changing the chat; the correction is audited.
19. A distress message gets supportive guidance with no automated alert and no monitoring claim.
20. There is no public self-registration path.
21. Bootstrap is unreachable remotely and unavailable once an administrator exists.
22. A newly provisioned student must replace the temporary password before anything else.
23. A 12-character composition-free password is accepted; an equally long known-common password is rejected.
24. Browser sessions expire after 30 idle minutes and never exceed 12 hours.
25. One account on two devices still holds only one concurrent tutoring session.
26. Revoking the last material approval blocks new sessions without deactivation; re-approval restores them.
27. An administrator cannot delete an active course or one with an active session.
28. A supervisor sees active-session status but no messages until completion.
29. A tutoring session stays active through extended inactivity and keeps blocking a second session.
30. A student resumes the same active session from another device without creating a second one.
31. Neither supervisors nor administrators can finish a student's active session.
32. A provider failure after partial output preserves the failed response; an explicit retry links a new response
    without rewriting history.

## 12. Success Criteria

Product-owner decision (2026-08-29): the MVP launch criterion is scenario-based verification, not outcome
metrics. The MVP is launch-ready when all 32 acceptance scenarios (AS-1 through AS-32) pass and no critical
security findings are open. Quantitative outcome metrics (adoption, engagement, learning outcomes,
provider-cost per session, and counter-metrics such as over-reliance on mentoring escalation) are deliberately
deferred until real deployments exist, consistent with MIA's no-telemetry, self-hosted posture.

## 13. Open Questions and Deferred Decisions

Confirmed open items from the source documents:

1. The audit action-name allowlist grows with implemented vertical slices; the full list is open.
2. Per-route API contracts (attributes, writable fields, filters, status codes, stable error codes, redaction,
   and field-level grammar details such as consecutive username separators) are open by design; the auth/session
   slice is defined first. `docs/api.md` is a draft and not authoritative.
3. Several MVP restrictions are explicitly candidates for post-MVP revisiting without any committed future
   behavior: stateless cookies, no reset-time cookie revocation, immutable staff email, no reopening ready
   material, no field encryption, student self-profile editing, separate authenticated rate limits.

### Resolved by product-owner decision (2026-08-29)

Previously open items, now folded into the requirements above and into `docs/product-requirements.md`:

- Material-brief per-field count and string limits — defined in FR-49.
- Content Security Policy for static responses — a strict baseline CSP ships with static serving (NFR-8),
  loosened during frontend integration only when the frontend demonstrably requires it.
- Supervisor-edited brief on ready material — never regenerable through any exposed operation; delete and
  recreate the material for a fresh generated brief (FR-49).
- Interrupted tutor responses — eligible for explicit retry exactly like failed ones (FR-62).
- Supervisor visibility into long-running active sessions — active-session status includes the session's start
  and last-activity instants (FR-63); chat review remains completion-gated.
- Fixed per-IP rate limits versus shared school NAT — accepted, documented MVP limitation with no operator
  override; limits stay as specified in NFR-9..NFR-11.
- Non-pending invitation-token states — revoked, faulty, accepted, and unknown tokens are mutually
  indistinguishable to token holders (FR-11).
- Anchor of the 12-hour absolute browser-session lifetime — completion of the final login stage, for all login
  shapes (FR-22).
- Summary feeding the next session's tutor context — the supervisor-corrected version when one exists (FR-57).
- Mentor "minimal identity" field set — username, name, nickname, and avatar (§4).

## 14. Assumptions

The first and third assumptions apply document-wide and appear only here; the journey assumption is additionally
tagged inline where it applies (§5).

- [ASSUMPTION] The PRD's primary audiences are the backend implementation team, the separate frontend team, and
  future contributors; it therefore preserves exact confirmed limits rather than rounding them.
- [ASSUMPTION] Journey protagonists and narrative details in section 5 are illustrative framing over documented
  steps, not new behavior.
- [ASSUMPTION] "Launch-grade" here means the self-hosted MVP release described by the repository, not a hosted or
  multi-tenant offering.
- Everything else in this document is sourced from the repository's confirmed product documentation; where the
  documentation is silent, the behavior is undecided rather than assumed.

## 15. Related Documents

- `docs/product-requirements.md` — authoritative product behavior contract (source of this PRD).
- `docs/api.md` — API layout draft (not authoritative for behavior).
- `docs/server-configuration.md`, `docs/data-dir.md`, `docs/architecture.md`, `docs/jobs.md`,
  `docs/tutoring-sessions.md` — operational and design drafts.
- `addendum.md` (this workspace) — technical depth deliberately kept out of the PRD: tech stack, storage layout,
  job mechanics, API conventions, provider deadlines, and cross-document tensions for the architecture phase.
