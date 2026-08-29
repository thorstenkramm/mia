# Adversarial Review — Architecture Spine (MIA backend MVP)

- **Target:** `ARCHITECTURE-SPINE.md` (2026-08-29 draft)
- **Method:** construct pairs of feature slices, each built by an independent agent obeying every AD and
  convention to the letter, and show where they still build incompatibly. Every confirmed pair is a hole;
  each hole gets a proposed fix (new or tightened AD/convention).
- **Verdict:** the spine's execution, persistence-discipline, and provider rules are strong, but account-state
  ownership and cross-feature lifecycle orchestration are underdetermined enough that the first three slices
  built independently will not compose. Two critical holes must close before parallel slice work starts.

---

## CRITICAL

### C1 — Nobody owns `users`, `user_roles`, or account security state; three flows create accounts

**The divergence pair (actually a triple):**

- `auth` is mandated as the **first** slice (Design Paradigm, Structural Seed). It cannot log anyone in without
  a `users` table carrying password hash, ban state, `must_change_password`, and `security_generation`. Under
  AD-1 ("one package per bounded feature owning its ... SQL and tables") the auth agent, building alone, is
  *required* to create and own that table.
- The Capability Map assigns "Identity & account data (FR-1..5)" to `identity, user` — but `identity` is a
  validation-only kernel package and `user` is explicitly a *later* slice. The user agent, reading the same map,
  will create its own `users` ownership in its migration.
- The Deferred list postpones the `user` vs `auth` split "when the second slice lands" — i.e., *after* the first
  slice has already been forced to decide it. The deferral is not neutral; it guarantees the first agent decides
  unilaterally and the second agent either violates AD-3 (second owner) or rewrites slice one.

Three account-creation initiators make it worse, each individually AD-3-compliant:

1. **Invitation acceptance** (FR-8/FR-12): `invitation` owns the Tx, calls "the owning package's" create
   function — which package? Auth's? User's (doesn't exist yet)?
2. **Student provisioning** (FR-15): capability map says `course, user`. The course agent needs
   `CreateStudentAccount(tx, ...)` with a temporary password and `must_change_password=1` — a *different*
   creation shape (no email, temp password, immediate role) than invitation's.
3. **`bootstrap-admin`** (FR-7): lives in `cmd/mia`. `cmd/mia` "may import anything" (AD-2), but AD-3 forbids
   direct SQL against a table it doesn't own — and `cmd` owns no tables. An agent implementing bootstrap alone
   will either write direct SQL in `cmd` (AD-3 violation that AD-2's wording invites) or invent a creation API
   in whichever feature package exists at the time.

Also unowned: `user_roles`. FR-12 (invitation writes roles), FR-13 (direct grant — invitation slice or user
slice?), FR-15 (student role at provisioning). The map lists "Invitations & roles" under *both* `invitation`
and `user`. Two agents, two role tables or two writers of one table — both can cite the map.

**Fix — new AD-13 (account-record ownership):**

- `user` is created **in the same change as `auth`** (first slice ships both packages). Delete the Deferred
  bullet; the split is decided now: `user` owns `users`, `user_roles`, and the account security-state columns
  (`security_generation`, `must_change_password`, ban state). `auth` owns only credential-flow tables
  (login/MFA challenges, factors, recovery-code digests, reset challenges, `mfa-management` proofs).
- Exactly one account-creation function exists: `user.CreateAccount(tx, spec)` where `spec` covers all three
  shapes (invited staff, provisioned student, bootstrap admin) including initial roles. `invitation`, `course`,
  and `cmd/mia` all call it inside their own Tx. `cmd` never issues feature SQL.
- Exactly one role-grant function: `user.GrantRoles(tx, ...)`, idempotent per FR-13, enforcing
  supervisor→student coupling and the banned-account rejection internally. FR-13's HTTP surface lives in `user`.

### C2 — Cascade deletion vs. AD-2 acyclicity: `user`/`course` must call into every slice that imports them

**The divergence pair:**

- The **tutoring agent** (also material, mentoring, speech), fully compliant, imports `course` and `user`
  exported APIs: session start checks active-course + membership (FR-54), material checks approval and course
  scope, mentoring checks assignments. AD-2 explicitly permits this.
- The **course agent**, implementing FR-42 (student removal deletes private material, sessions, chats,
  summaries, speech, mentoring records, jobs "in one transaction" per AD-3's pass-the-Tx rule), must call
  `material.DeleteCourseScoped(tx, ...)`, `tutoring.DeleteCourseScoped(tx, ...)`, `mentoring...`, `speech...`.
  So `course` imports tutoring/material/mentoring/speech.
- The **user agent**, implementing FR-76/FR-77 (account deletion across all courses, mentoring triage, clearing
  historical actor references), needs the same imports — while `mentoring` stores actor authorship referencing
  users and imports `user`.

Each agent's import list is individually legal; jointly it is a compile-time cycle
(`tutoring → course → tutoring`, `mentoring → user → mentoring`). AD-2 says "must stay acyclic" but the spine
assigns responsibilities that force a cycle. Independent agents cannot resolve which side inverts — both
directions are AD-sanctioned. FR-76's "clears historical actor references" is worst: `user` may not SQL
mentoring's tables, and mentoring cannot export a user-deletion hook without importing user types.

**Fix — new AD-14 (lifecycle registry):**

- A kernel `lifecycle` package (or extension of `sqlite`) defines two narrow interfaces:
  `CourseScopedDeleter(tx, courseID, userID)` and `AccountScopedDeleter(tx, userID)` (the latter covering data
  deletion *and* actor-reference clearing/triage). Feature packages implement and **register them in `cmd/mia`
  wiring** — registration at the composition root keeps the kernel feature-free (AD-2 intact).
- `course.RemoveStudent` and `user.DeleteAccount` own the Tx and invoke all registered deleters inside it.
- Declare the canonical import direction: `material`, `tutoring`, `mentoring`, `speech` may import `course` and
  `user`; `course` and `user` never import them. `course` may import `user`, never the reverse.

---

## HIGH

### H1 — `security_generation` bump triggers are unenumerated; FR-20 vs FR-19 make "obvious" behavior wrong

Writers scattered across slices: supervisor temporary password (FR-19 — course slice? user slice?), supervisor
student MFA reset (FR-32, auth), staff MFA reset by another admin (FR-32, auth), `reset-admin-mfa`
(`cmd/mia`). Meanwhile FR-20 says a successful self-service staff reset does **not** revoke other cookies, and
FR-24 password replacement "starts a new authenticated-session lifetime" — an agent reading AD-8 alone will
plausibly bump the generation on *any* password change (killing other devices, violating FR-20) or on ban
(making unban behavior diverge between agents). Two slices bumping on different trigger sets both "pass" AD-8.

**Fix — tighten AD-8:** the generation column lives in `users` (per C1 fix) with one exported
`user.BumpSecurityGeneration(tx, userID)`. Enumerate the only callers: FR-19 temporary-password set, FR-32
student MFA reset, FR-32 staff MFA reset, `reset-admin-mfa`. Enumerate explicit non-triggers: self password
change/reset, staff mobile change, ban/unban (ban acts via per-request state reload, not generation).

### H2 — Shared durable SMS send limits have two natural owners

FR-28/FR-35: the 60s/5-per-hour/10-per-day send limits are shared per account *and per destination* across MFA
enrollment resends, login-challenge resends (both `auth`) **and** staff mobile-verification codes (`user`).
NFR-11 makes them durable, so they are a table. AD-3 gives the table one owner — but the spine names none. The
auth agent and user agent will each create a compliant private counter table; both pass review; the product
limit is silently not shared (an attacker gets double the SMS budget, and ClickSend cost doubles).

**Fix — tighten AD-3/AD-10:** one durable `sms_send_log` table owned by a single named package. Recommended:
a thin kernel table beside `provider/clicksend` (AD-3 already allows kernel-owned tables) with one exported
`ReserveSend(tx, accountID, destination) error` gate that every SMS-sending path must call in its transaction.

### H3 — Jobs↔subject contract: terminal-failure callback and subject reference scheme unspecified

AD-5 requires the subject's terminal-failure transition "in the same transaction as the failed job state" —
but `jobs` is a kernel package that may not import `material` or `tutoring` (AD-2) nor touch their tables
(AD-3). So features must register per-job-type handlers; the spine never says so, and two agents will invent
incompatible handler shapes (typed payload structs vs opaque JSON; per-type completion vs generic). Worse,
FR-42 deletes "related jobs" on membership removal: `course` needs delete-by-subject on the jobs table, which
is impossible unless subject references are queryable columns rather than payload JSON — a choice the jobs
agent makes alone.

**Fix — tighten AD-5:** jobs rows carry `subject_type` + `subject_id` columns (queryable); feature packages
register `JobHandler` implementations per job type in `cmd/mia` wiring; the worker invokes the handler's
terminal-failure function with the open Tx; `jobs` exports `DeleteBySubject(tx, type, id)` for lifecycle
cascades (composes with AD-14).

### H4 — Per-request state reload and restricted-stage gating: kernel needs feature data it may not import

AD-8 demands account/role/assignment/ban/password-gate reload from SQLite on **every** request, and restricted
stages that "allow only their enumerated actions." `httpserver` (kernel) owns sessions and routing but cannot
import `user`/`auth` (AD-2) to do the reload, and the enumerated stage-action lists are auth-feature knowledge.
Divergence pair: the auth agent builds a reload middleware inside `auth`; the mentoring agent, building
independently, registers routes on raw `httpserver` with only cookie parsing — every AD is satisfied, and
mentoring silently skips ban/password-gate/generation checks (an authorization hole AD-4 doesn't catch because
AD-4 governs resource fetch, not request admission).

**Fix — tighten AD-8:** `httpserver` defines an `IdentityLoader` interface and a mandatory authenticated-route
registration path that refuses handlers outside it; `user`/`auth` supply the loader at wiring. Route
registration declares the required stage; the default is full `authenticated`, and only `auth` may register
`mfa`/`password-change`-stage routes. No feature registers authenticated routes any other way.

---

## MEDIUM

### M1 — Rate-limiter key construction and count semantics diverge per slice

NFR-9/10 limits key on username (needs ASCII-lowercase canonicalization), token digest, account ID, and IP —
and mix "count failures" (login) with "count requests" (recovery, preview). The limiter lives in `httpserver`,
but keys are built by feature handlers. Two agents produce `login:alice` vs `login:u:ALICE`-style keys and
failure-vs-request counting that each satisfy "rate limiting lives in httpserver."

**Fix — new convention row:** `httpserver` exports named limiter definitions (name, dimensions, window, count
mode: attempts vs failures); features reference definitions by name and pass raw values; the limiter — not the
caller — canonicalizes (username via `identity`, tokens as digests, IPs from the trusted-proxy resolver).

### M2 — Request-ID idempotency is deferred "per slice" — that deferral is the divergence

FR-56's strict idempotency (replay returns resource, same-ID-different-content conflicts, history lives with
owning data) needs a storage-and-comparison pattern. Deferring "idempotent replay details" per slice invites
one slice storing a body digest on the owning row and another building a request-ID side table with
field-by-field comparison — incompatible conflict semantics behind identical route contracts. FR-13/FR-16
natural-key idempotency will also get conflated with request-ID idempotency by at least one agent.

**Fix — new convention row:** request-ID idempotency = `request_id` + SHA-256 canonical-body digest columns on
the owning row (so FR-56's retention-follows-data falls out of AD-3 for free); replay match by digest;
mismatch → one registry conflict code. Natural-key idempotency (FR-13, FR-16) is a separate pattern and never
uses request IDs.

### M3 — "Pass the `*sql.Tx`" has no shared parameter type

AD-3 names `*sql.Tx`, but Go agents habitually define per-package querier interfaces or accept `*sql.DB` with
optional Tx. Two owner APIs — one taking `*sql.Tx`, one taking its own `store.Querier` — cannot participate in
one multi-feature transaction without adapters.

**Fix — tighten AD-3:** the `sqlite` kernel defines the single `Querier`/Tx type all exported owner functions
accept; multi-feature operations compile only against it.

### M4 — Audit subject fingerprints and actor-reference clearing implemented per slice

AD-12's "random one-way fingerprints for deleted subjects" doesn't say who generates them. One agent derives
HMAC-of-ID in `material`, another random-UUIDs in `tutoring`; deletion events for one account become
uncorrelatable, and FR-76's actor-reference clearing (see C2) needs the same fingerprint at every site.

**Fix — tighten AD-12:** only the `audit` kernel generates fingerprints, exporting
`FingerprintForDeletedSubject(tx, subjectID)` that returns a stable random value per deletion operation;
callers never hash or invent their own.

---

## LOW

### L1 — `cases.Fold`+NFC normalization has no canonical home

`course` (course names) and `material` (material names) each implementing fold+NFC risks divergence in
operation order (NFC-then-fold vs fold-then-NFC) and caser options — same name, two uniqueness keys.
**Fix:** put one `identity.NormalizeName` in the kernel; the convention row names it.

### L2 — Error-code and audit-action namespacing unstated

AD-7/AD-12 fix the registries but not naming. Agents will produce `invitation_revoked` vs
`invitations.revoke` vs `invite.deleted`. **Fix:** convention row: `<package>.<entity>.<verb>` (past tense)
for audit actions; `<package>_<condition>` snake_case for error codes; registry additions rejected otherwise.

---

## Counts

| Severity | Count |
| --- | --- |
| Critical | 2 |
| High | 4 |
| Medium | 4 |
| Low | 2 |

## Summary of proposed spine changes

1. **New AD-13:** `user` ships with the first slice and owns `users`/`user_roles`/security state; single
   `CreateAccount` + `GrantRoles` used by invitation, course, and `bootstrap-admin`; delete the user-vs-auth
   Deferred bullet.
2. **New AD-14:** kernel lifecycle registry for course-scoped and account-scoped deletion/actor-clearing;
   fixed feature import direction (`material`/`tutoring`/`mentoring`/`speech` → `course` → `user`, never back).
3. **Tighten AD-8:** enumerate `security_generation` bump triggers and non-triggers; mandatory
   identity-reload/stage-gated route registration through `httpserver` with injected loader.
4. **Tighten AD-5:** queryable job subject columns, wiring-time handler registration, `DeleteBySubject`.
5. **Tighten AD-3:** single kernel Tx/querier type; name the owner of the durable SMS send-limit table.
6. **New convention rows:** named limiter definitions with canonicalization inside the limiter; request-ID
   idempotency as digest-on-owning-row; audit fingerprints only from `audit`; `identity.NormalizeName`;
   error-code and audit-action naming schemes.
