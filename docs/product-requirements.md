# Product requirements

## Purpose and status

This document defines MIA's confirmed product behavior. It describes what users
must be able to do and which access boundaries the product must preserve. It
does not define package layout, database implementation, or other architecture
unless a product-facing contract requires a specific choice.

Audience: product owners, designers, operators, and developers. This document is
authoritative for confirmed product behavior.

## Contents

- [Product goal](#product-goal)
- [Scope](#scope)
- [Product terminology](#product-terminology)
- [Authorization principles](#authorization-principles)
- [Roles and capabilities](#roles-and-capabilities)
- [Account registration](#account-registration)
- [Course lifecycle](#course-lifecycle)
- [Material](#material)
- [Tutoring sessions](#tutoring-sessions)
- [Mentoring](#mentoring)
- [Product notifications](#product-notifications)
- [Time and scheduling](#time-and-scheduling)
- [External services](#external-services)
- [Data and deployment](#data-and-deployment)
- [Data lifecycle](#data-lifecycle)
- [Security and privacy requirements](#security-and-privacy-requirements)
- [Acceptance scenarios](#acceptance-scenarios)

## Product goal

MIA provides personalized AI tutoring aligned with a student's current classes
and exams. Unlike a platform with its own predefined curriculum, MIA uses the
curriculum, learning goals, educational approach, and material selected by the
supervisors responsible for a course.

Improved marks are an intended benefit, not a guaranteed outcome.

## Scope

MIA provides:

- course creation and preparation;
- course and student-specific AI tutor instructions;
- processing and supervised publication of course-wide material;
- private student uploads for individual tutoring;
- AI tutoring sessions based on authorized material;
- progress summaries and follow-up suggestions;
- supervisor oversight;
- requests for human mentoring;
- optional text-to-speech for AI tutor responses.

MIA does not provide:

- predefined courses, curricula, or learning material;
- a separate authorization role for a person's relationship to a course outside
  MIA;
- unrestricted access by administrators merely because they have a global role;
- automatic authority for actions suggested by a model;
- a guarantee of academic results.

## Product terminology

### AI tutor

The model-driven tutoring behavior. The AI tutor is not a user role and cannot
authorize platform actions.

### Administrator

A user with named global platform capabilities. Administrator status alone does
not grant access to student-private material or course-scoped student records.

### Supervisor

A user assigned responsibility for a course. Supervisor authority applies only
to assigned courses.

### Mentor

A user assigned to support a specific student in a specific course. Mentor
authority applies only to that assignment.

### Student

A user who joins courses and receives tutoring.

### Operator

The person or organization that installs MIA, starts the server, configures its
external services, and makes it available to users. The operator is not a MIA
user role.

The operator is responsible for lawful operation, deciding who may use the
service, obtaining any required consent, and complying with applicable local,
institutional, and safeguarding policies. The MIA project and its authors
provide self-hosted software; they do not operate deployments created by others.

MIA does not provide guardian accounts, guardian access, age verification, or a
guardian-consent workflow. Operators handle these responsibilities outside MIA.
This does not remove MIA's requirements for secure controls, privacy-preserving
defaults, and accurate documentation.

## Authorization principles

- Authorization considers the action, user role, course assignment, student
  assignment, resource ownership, and material scope.
- A user with multiple roles receives the union of permissions that apply in
  the relevant scope.
- Any administrator, supervisor, or mentor role makes the account staff for
  profile management, password and MFA recovery, bans, and deletion. Student
  administration operations apply only to student-only accounts.
- A global role does not broaden course or student scope.
- MIA must not reveal whether an out-of-scope student, session, upload,
  invitation, or other sensitive resource exists.
- Every sensitive resource and field requires an explicit access rule.
- Usernames are unique case-insensitively. Email addresses are unique
  case-insensitively when present.

## Account identity

- A username contains 3 through 32 ASCII characters. It starts and ends with an
  ASCII letter or digit; interior characters may also be `.`, `_`, or `-`.
- MIA preserves username letter case for display and compares usernames using
  their ASCII lowercase form.
- An email address contains at most 254 ASCII characters after surrounding
  whitespace is removed. MIA accepts a practical RFC mailbox subset: an unquoted
  local part, including `+`, followed by a DNS-style domain. It rejects display
  names, comments, quoted local parts, domain literals, and malformed dots.
- MIA preserves the trimmed email spelling for display and compares email
  addresses using the ASCII lowercase form of the complete address.
- Every account requires a username, password, preferred language, country, and
  preferred time zone. Administrators, supervisors, and mentors additionally
  require a verified email address. Other profile fields remain optional.
- MIA stores a preferred language as a canonical BCP 47 tag and rejects an
  unspecified or private-use-only language.
- MIA stores a country as an uppercase ISO 3166-1 alpha-2 code.
- MIA accepts `UTC` or a named IANA time-zone identifier available in its embedded
  time-zone database. It rejects `Local` and numeric fixed offsets.
- Optional names contain at most 100 Unicode code points; optional nicknames
  contain at most 24. MIA trims surrounding whitespace and rejects control
  characters. A non-empty value contains at least one code point.
- An optional year of birth is an integer from 1900 through the current UTC year.
  It is self-reported profile data, not age verification.
- Optional student-specific AI tutor instructions contain at most 4,000 Unicode
  code points and 16 KiB of valid UTF-8. MIA normalizes line endings to LF and
  treats whitespace-only instructions as absent.
- A mobile number uses E.164 form: `+`, a first digit from 1 through 9, and 7
  through 14 further digits. MIA does not accept or infer regional formats.
- After field-specific trimming, MIA stores an empty or whitespace-only optional
  text value as absent. Required text fields reject empty values.
- Except for passwords and chat messages, multiline profile and descriptive text
  must be valid UTF-8. MIA normalizes CRLF and CR to LF, trims surrounding
  Unicode whitespace, and rejects NUL and controls other than tab and newline.
  Passwords and chat messages are neither trimmed nor normalized.

## Roles and capabilities

### Administrator capabilities

An administrator can:

- create and manage course records;
- assign supervisors to courses;
- invite new administrators or grant the administrator role to registered staff;
- invite new supervisors or grant the supervisor role to registered staff;
- permanently delete user accounts subject to the account and relationship
  lifecycle rules;
- supervise platform background jobs;
- access the audit log.

An administrator who needs supervisor capabilities must also be assigned as a
supervisor to the relevant course.

### Supervisor capabilities

Within an assigned course, a supervisor can:

- prepare and edit course content;
- upload and delete course-wide material;
- review and correct generated material briefs;
- approve course-wide material and revoke approval;
- activate a prepared course;
- provision student accounts and add students to the course;
- invite mentor accounts, grant the mentor role to registered staff, and assign
  mentors to the course;
- assign and reassign course mentors to students;
- inspect student-private uploads and generated material briefs;
- review completed tutoring sessions and full chat histories, and correct their
  summaries and follow-ups;
- view student profiles and edit non-security profile fields for any student-only
  account that shares an assigned course;
- set a temporary password for a student-only account's recovery;
- ban or unban a student-only account.

Granting the supervisor role also grants the student role. After an assigned
course is active, a supervisor may add their own account through the ordinary
existing-student membership operation and test the course through ordinary
tutoring sessions. MIA has no separate preview mode and no inactive-course test
session.

### Mentor capabilities

Within an explicit course and student assignment, a mentor can:

- receive the topic of a mentoring request;
- respond to the request;
- schedule a personal mentoring session.

A mentor cannot access student-private uploads, generated briefs for those
uploads, or tutoring chat histories.

### Student capabilities

A student can:

- view their own profile;
- view joined courses;
- download approved material from joined courses;
- conduct tutoring sessions;
- upload private material for use during tutoring;
- delete their own private material;
- view their own strengths, weaknesses, and follow-up summaries;
- request mentoring when the course and student settings allow it.

A student-only account cannot change any profile field. Assigned supervisors
manage profile fields only for student-only accounts, including avatars and
mobile numbers. Accounts with any staff role use staff self-service rules.

### User and roster visibility

- Every user can view their own account.
- Every authenticated account can read a server-authored release-one capability
  catalog with explicit global, course, student, and own-resource scope. The
  catalog reflects current role, assignment, membership, ban, and account state
  on each explicit non-refreshing check. Empty and unavailable scope is explicit,
  and each operation still authorizes independently.
- Assigned supervisors can view students and staff relationships in their
  assigned courses.
- Mentors see only minimal identity — username, name, nickname, and avatar —
  for students explicitly assigned to them.
- Administrators can page and exactly filter a minimal global account directory
  and inspect a target by opaque user ID. These reads expose only username,
  account class and state, permanent roles, supported action eligibility, and
  viewer-safe consequence identifiers. Email, other profile fields, security
  data, and course, tutoring, or mentoring content are excluded.
- Students cannot view a course roster.
- Administrators see account and relationship metadata needed for global
  administration, but that role alone does not grant private course-content
  access.

### Staff self-service profiles

An administrator, supervisor, or mentor can change their own name, nickname,
language, country, preferred IANA time zone, verified mobile number, avatar, and
text-to-speech voice. Username, email, roles, ban state, password state, and
other security fields are not writable through the generic profile operation.
Staff email addresses are immutable in the MVP.

### Avatar images

- Avatar upload accepts only signature-validated JPEG and PNG source images.
- The encoded source is limited to 10 MiB, 40 decoded megapixels, and 10,000
  pixels in either dimension. Animated or malformed images are rejected.
- MIA applies JPEG orientation, strips metadata, preserves aspect ratio, and
  resizes so neither output dimension exceeds 512 pixels.
- MIA stores one non-animated PNG. It never serves the untrusted source image.
- Avatar download requires authorization to view the corresponding user profile
  and never broadens profile visibility.

### Supervisor-managed student profiles

Any supervisor who shares an assigned course with a student-only account can edit
that student's non-security profile fields:

- name and nickname;
- year of birth;
- email address and mobile number;
- preferred IANA time zone;
- avatar;
- language and country;
- student-specific AI tutor instructions;
- text-to-speech voice, which only an assigned supervisor sharing a course with
  the student may set, change, or clear;
- whether the student may request mentoring.

This authority affects the student's global profile, not only the shared course.
Every change is audited. It does not grant permission to change roles, MFA,
password state, ban state, or course and mentor assignments. Password resets,
bans, and assignments remain separate actions with their own authorization
rules.
Accounts with any staff role are not writable through supervisor-managed student
profile operations.

### Mobile-number verification

- A staff user's requested self-service mobile-number change does not update the
  account immediately.
- MIA sends a confirmation code by SMS to the new number.
- MIA updates the account only after the staff user submits the correct code.
- A code expires 30 minutes after issuance and is single-use. Verification
  attempts and code resends must be bounded.
- Sending another mobile-verification code requires a 60-second cooldown and is
  limited to five sends per hour and ten sends per day.
- Send limits apply independently to both the account and destination mobile
  number.
- Every provider call counts toward the cooldown, hourly, and daily send limits,
  including a failed delivery attempt.
- Five incorrect submissions invalidate the code. A replacement remains subject
  to the resend cooldown and hourly and daily limits.
- An incorrect or expired code leaves the current mobile number unchanged.
- After a successful change, MIA sends no notification to the previous mobile
  number.
- Changing the profile mobile number does not move an active SMS MFA factor. The
  factor remains bound to its previously verified destination until the user
  completes an explicit MFA replacement.
- Successful profile-mobile change or removal deletes any pending SMS enrollment
  or replacement. Active SMS factors remain bound to their enrolled snapshots.
- If SMS delivery is not configured or fails, self-service mobile-number changes
  are unavailable and the current number remains unchanged.
- A staff user may remove the verified profile mobile through an authenticated
  operation that requires no SMS delivery. Removal clears `users.mobile`,
  invalidates pending mobile-verification challenges, leaves an active SMS MFA
  destination unchanged, and is audited without recording the number.
- Confirmation codes are stored only in private SQLite. They are never returned
  by the API or written to logs or audit content.
- An assigned supervisor changes a student's mobile number through the ordinary
  student profile operation. The number is immediately verified; `null` clears
  it. The change atomically invalidates pending mobile-verification challenges
  and pending SMS factors for that student and does not move an active SMS MFA
  factor. Audit records only that the field changed, never either number.

## Account registration

- MIA has no public sign-up endpoint or self-registration workflow.
- Administrator, supervisor, and mentor registration is invitation-only after
  the first-administrator bootstrap.
- Student accounts are provisioned by assigned supervisors.
- Only an actor authorized for the intended role and scope can initiate account
  creation or registration.
- Administrators, supervisors, and mentors must have a verified email address.
- Accepting an invitation delivered to that address verifies it.
- A new administrator, supervisor, or mentor chooses their username and password
  while accepting the invitation. The inviter does not create or receive a
  temporary password.
- The inviter supplies only the intended email address. The invited role comes
  from the authorized invitation action. Invitations have no course or student
  scope.
- Invitations register new accounts only. Creating one for an email already used
  by a registered account is rejected; the authorized actor uses direct role
  assignment for that user instead.
- A new administrator, supervisor, or mentor completes their own required profile
  fields during acceptance.
- Before acceptance, an unauthenticated invitation page shows only that it is a
  MIA invitation and the invited role.
- The preview does not expose the inviter's profile, intended email address,
  user records, or other course data.
- For token holders, public invitation preview and acceptance treat revoked,
  faulty, accepted, and unknown tokens identically: one generic
  invalid-invitation response that reveals no lifecycle state.
- Student email remains optional.
- Administrator, supervisor, and mentor invitations do not expire. They remain
  pending until accepted or explicitly revoked unless definite initial delivery
  failure makes them faulty.
- A definite initial SMTP delivery failure instead makes an invitation terminally
  faulty, records a sanitized failure code and fault time, and invalidates its
  bearer token. A faulty invitation cannot be accepted or resent. An authorized
  deletion physically removes it and writes a content-free audit event.
- An SMTP timeout is treated as ambiguous operational success. The invitation
  remains pending and usable, MIA logs a sanitized error, and it does not retry
  automatically. Authorized invitation views expose the state and sanitized
  failure code; MIA sends no separate fault notification.
- Invitation email is English-only plain-text UTF-8 with sanitized headers, one
  recipient in `To`, and the configured sender. It contains only the confirmed
  role and no HTML part.
- Invitation links use `/invitation#token=<uuid>`. The frontend removes the
  fragment with `history.replaceState` before making an API request.
- A definite resend failure also makes the invitation faulty. A timeout during
  resend leaves the newly rotated token pending and usable under the same
  ambiguous-success rule.
- Invitations are single-use. Successful acceptance permanently consumes the
  invitation and prevents further acceptance attempts.
- Any administrator can revoke a pending administrator or supervisor invitation.
- Any supervisor may create a mentor invitation. Only its inviting supervisor or
  an administrator can view, resend, or revoke it or delete it after it becomes
  faulty.
- The same actors authorized to revoke an invitation may delete it after it
  becomes faulty.
- Revocation takes effect immediately, prevents acceptance, and is audited.
- The invitation DELETE operation revokes and retains a pending invitation,
  physically deletes a faulty invitation, and rejects accepted or already revoked
  invitations without changing them.
- Resending a pending invitation invalidates its previous token, creates a new
  token, and sends it to the same intended email address.
- Token replacement is audited. Every earlier link becomes unusable immediately.
- Authorized invitation reads expose the latest delivery as queued, delivered,
  ambiguous, or failed, with a nullable sanitized outcome code and authoritative
  UTC attempt time. Timeout and other uncertainty never claim delivery and never
  trigger automatic retry.
- An authorized invitation detail read returns a strong resource-specific ETag.
  State-dependent DELETE requires the reviewed value in `If-Match`; missing or
  stale preconditions change nothing and require a fresh read and confirmation.
  Successful DELETE identifies whether the pending invitation was retained as
  revoked or the faulty invitation was physically deleted.
- Invitation acceptance always creates a new account and its invited permanent
  role. An email uniqueness conflict rejects acceptance without consuming the
  invitation.
- Supervisor invitation acceptance creates both the permanent supervisor and
  student roles in the same transaction.
- Accepting a mentor invitation creates the account and permanent mentor role. It
  creates no course or student assignment.

### Direct role assignment

- A supervisor resolves a prospective mentor-role target only by one complete
  opaque user ID through a focused CSRF-protected preflight. MIA provides no
  partial, prefix, fuzzy, batch, browse, autocomplete, suggestion, pagination,
  or result-count behavior and stores no durable resolution record.
- A successful preflight returns only the immutable user ID, username, nullable
  display name, current mentor-grant state, and a strong validator covering the
  exact identity, account class and state, verified-email eligibility, and
  mentor-role state. It excludes email, mobile, unrelated roles, assignments,
  courses, security state, and administrator metadata.
- Preflight permits ten attempts per supervisor account and 30 per trusted
  source IP in a rolling hour. Well-formed unknown IDs, student-only, unverified,
  banned, deleted, inaccessible, otherwise ineligible, and throttled targets
  return the same fixed unavailable response without target-specific
  `Retry-After`, rejected identity, reason, or retained target-derived limiter
  state. Throttling is audited without the submitted target ID.
- A preflight document whose `data.attributes.user_id` is missing, null,
  non-string, or not a canonical MIA user ID returns `422 auth_invalid_request`
  before target lookup or dedicated throttling. Well-formed unknown, hidden, and
  ineligible IDs use the fixed unavailable outcome.
- A supervisor mentor-role grant requires the exact reviewed validator and
  atomically rechecks supervisor authority, target identity and eligibility,
  account and ban state, verified email, and current mentor role. Missing or
  stale preconditions grant nothing and require a fresh preflight. Clients
  reconcile uncertain outcomes through another preflight and never
  automatically replay the grant.
- An authorized actor grants an additional permanent role only to a registered
  staff account identified by user ID. A student-only account cannot receive its
  first staff role through direct assignment; new staff accounts use invitations.
- An administrator may grant the administrator or supervisor role. Any supervisor
  may grant the mentor role.
- A role assignment takes effect immediately and requires no approval or action
  by the affected user.
- Granting the supervisor role atomically grants the student role when absent.
- A banned account must be explicitly unbanned before any staff role assignment.
- Existing assignment is idempotent. Administrator, supervisor, and mentor roles
  cannot be removed independently from the account.
- Every staff role assignment requires the account to have a verified email.

### Account and administrator lifecycle

- Only an administrator can permanently delete a student account. A supervisor
  can remove course membership but cannot delete the account globally.
- An account with any administrator, supervisor, or mentor role can be deleted
  only by a different administrator. Its student role does not change that rule.
- Administrator, supervisor, and mentor roles are permanent once granted. Course
  supervisor assignments, course mentor assignments, and student-mentor
  assignments are removable relationships, not global roles.
- Staff deletion is rejected when the target is the last administrator or the
  sole supervisor of any course. Otherwise it automatically removes current
  course and mentor assignments, applies ordinary open-work triage, and deletes
  the complete account and its student-owned data.
- The different-administrator, last-administrator, sole-supervisor, cleanup,
  triage, and account-deletion checks occur in one transaction.
- Retained domain history clears direct references to the deleted staff actor.
  It need not preserve the actor's identity; the deletion audit record retains
  only its ordinary de-identified fingerprint.

### Mentor course and student assignments

- Any supervisor assigned to a course may add a registered user with the mentor
  role to that course.
- The course assignment takes effect immediately. It requires no mentor
  acceptance, and the mentor cannot reject or remove it.
- An assigned supervisor may assign a course mentor to any student in that
  course. The student assignment also takes effect immediately without mentor
  acceptance. The mentor cannot reject or remove either assignment.
- A mentor must be assigned to both the course and student before receiving that
  student's mentoring work or data.
- Any supervisor assigned to the course may remove a course or student mentor
  assignment. The affected-work rules below apply.

### Student account provisioning

- Students are not required to have an email address.
- A supervisor creates a student account, chooses its username and temporary
  initial password, and adds it to an assigned course.
- The temporary password must satisfy the applicable password policy.
- The supervisor provides the credentials to the student outside MIA.
- The student must replace the temporary password at first login before using
  any other authenticated feature.
- After replacement, the temporary password no longer grants access.
- An existing student account is added to the course instead of creating a
  duplicate account.
- Adding an existing student requires the complete username. MIA provides no
  global student search or autocomplete to supervisors.
- The existing-account operation accepts only the username and rejects password
  or profile fields. The account must already have the student role; course
  enrollment never grants a role implicitly.
- Unknown usernames and accounts without the student role produce the same safe
  failure without disclosing account details.
- An assigned supervisor adds the existing student directly; MIA has no pending
  membership or student-acceptance workflow.
- Adding an existing current member is idempotent and returns the existing
  membership without changes.
- A student removed earlier may rejoin the active course as a new membership.
  Deleted course-scoped data is never restored.

### Student password recovery

- Any supervisor sharing an assigned course with a student-only account can
  initiate password recovery.
- The supervisor chooses a new temporary password that satisfies the password
  policy.
- Setting the temporary password immediately invalidates every existing browser
  cookie for the student.
- The student must log in again with the temporary password and replace it before
  using any other authenticated feature.
- The temporary password is never logged, included in audit content, or exposed
  after the supervisor submits it.
- The reset and subsequent password replacement are audited without recording
  either password.

### Staff and administrator password recovery

- Supervisors, mentors, and administrators recover forgotten passwords through
  a complete username submitted to the public recovery operation. MIA sends the
  link to the account's verified email address.
- MIA sends a single-use reset link that expires 30 minutes after issuance.
- Public recovery responses do not reveal whether the submitted username exists.
- MIA persists the challenge before attempting SMTP delivery. A new request does
  not invalidate earlier links; every unexpired, unused link remains valid until
  one reset succeeds. A successful reset invalidates all remaining challenges.
- Definite SMTP failure invalidates the newly created reset challenge. SMTP
  timeout leaves it usable, logs a sanitized error, and is not retried.
- Banned accounts cannot request or complete password recovery. The public
  response remains indistinguishable from other outcomes.
- Recovery email is English-only plain-text UTF-8 with no HTML part. Its frontend
  link uses `/password-reset#token=<uuid>`, and the frontend removes the fragment
  before making an API request.
- Successful password reset does not revoke other stateless browser cookies in
  the MVP. They remain valid until their normal expiry.
- Reset request, completion, and failure events are audited without storing the
  link token or password.

### Password policy

- Passwords contain at least 12 and at most 128 Unicode code points and must be
  valid UTF-8. Their encoded form contains at most 512 bytes.
- MIA allows spaces, Unicode, and password-manager-generated values.
- MIA does not require particular character classes, such as uppercase letters,
  digits, or symbols.
- MIA rejects known-common passwords using a local check that does not disclose
  the candidate password to an external service.
- The local check uses MIA's bundled SecLists top-100,000 common-password list and
  compares the unchanged valid UTF-8 password exactly, without trimming,
  normalization, case folding, or mutation rules.
- MIA does not silently trim or alter a submitted password.
- The policy applies to permanent and supervisor-chosen temporary passwords.
- Operators cannot configure weaker password requirements.

### Authentication session lifetime

- The MVP uses a signed and encrypted stateless browser cookie. MIA keeps no
  server-side session records.
- An authenticated browser session expires after 30 minutes without an
  authenticated user action.
- Ordinary authenticated requests, automatic polling, SSE establishment and
  reconnection, server-sent heartbeats, and background provider work do not
  reset the inactivity timer. Only the explicit authenticated Continue working
  operation resets it, capped by the original 12-hour maximum.
- A session expires no later than 12 hours after authentication, regardless of
  activity. The 12-hour maximum anchors at completion of the final login stage —
  the moment the full authenticated cookie is created — not at the initial
  password verification.
- After either timeout, the user must authenticate again.
- The 12-hour maximum is not extended by session activity.
- Logout clears the authentication cookie and rotates the signed browser marker
  in the current browser. A delayed ordinary authenticated or Continue working
  response cannot restore usable authentication because it does not issue a
  matching marker.
- A delayed login, MFA-completion, or password-change response admitted before
  logout may restore authentication because it reissues a matching cookie and
  marker pair. Clients reconcile this outcome through session discovery.
- MIA has no server-side browser-session or per-browser revocation records and
  does not provide session listing or remote-session revocation in the MVP.

### Concurrent sessions

- One account may have authenticated browser sessions on multiple devices at the
  same time.
- Each session has its own inactivity and maximum-lifetime timers.
- Account ban and deletion take effect on the next request because MIA reloads
  current account state instead of trusting it from the cookie.
- Only accounts with the student role and no staff role can be banned. A ban
  rejects new logins and the next request made with an existing cookie; MIA has
  no server-side cookie revocation registry.
- A ban does not cancel tutor generation, speech generation, jobs, or provider
  calls already in flight. They finish or reach their existing deadline.
- An already established SSE stream may continue after a ban. Student deletion
  closes delivery when the next persistence attempt finds that its target no
  longer exists; the provider call itself continues to its deadline.
- The one-active-tutoring-session rule applies across all of a student's browser
  sessions and devices.

### Login stages

- Login always verifies the password first.
- When the account has active MFA, successful password verification creates a
  restricted `mfa` cookie stage. That stage permits only verification or
  recovery-code consumption for the bound challenge, resend when the bound
  challenge uses SMS, and logout.
- After MFA, an account with `must_change_password` enters a restricted
  `password-change` cookie stage. Without MFA, successful password verification
  enters this stage directly.
- The `password-change` stage permits only password replacement and logout.
- Restricted stages expire after 30 minutes, do not refresh, and are rotated at
  every successful transition.
- MIA creates a full authenticated cookie only after every required stage is
  complete. Completing password replacement starts a new authenticated-session
  lifetime.

### MFA enforcement

- MFA is optional for every user and role.
- Administrators, supervisors, mentors, and students may use password-only
  authentication when MFA is not enabled on their account.
- MIA does not impose role-based MFA enrollment.

### MFA methods

- MIA supports time-based one-time passwords from authenticator applications.
- TOTP uses SHA-1, six digits, a 30-second period, and a 20-byte random secret
  encoded as uppercase unpadded Base32. Verification accepts the current time
  step and one adjacent step in either direction.
- The authenticator issuer is `MIA (<hostname>)`, using the normalized hostname
  from `main.public_url`; the account label is the username.
- MIA permits SMS MFA enrollment or replacement when ClickSend is configured and
  the user has a verified profile mobile. Login challenges for an active SMS
  factor continue using its immutable enrolled destination even after the profile
  mobile changes or is removed.
- SMS MFA and mobile-verification codes are uniformly generated six-digit decimal
  strings from `000000` through `999999`; leading zeros are significant.
- An SMS MFA code expires 30 minutes after issuance and is single-use.
- SMS MFA enrollment and challenge resends use the same 60-second cooldown,
  five-per-hour limit, and ten-per-day limit, applied to both account and
  destination number.
- Every enrollment or challenge SMS send attempt counts toward those limits
  regardless of delivery outcome.
- Five incorrect SMS MFA submissions invalidate the code.
- New SMS enrollment or replacement is unavailable when either prerequisite is
  missing.
- MFA enrollment is not active until the user verifies the selected factor.
- A pending enrollment expires after 30 minutes. Expiry removes its pending
  secret or SMS code without changing an existing active factor.
- Five incorrect verification submissions delete the pending enrollment.
- Resending a pending SMS enrollment sends its existing code without extending
  expiry or resetting failed attempts.
- An account has at most one active MFA method: TOTP or SMS.
- Replacing the method requires verification of the new factor before it becomes
  active. The existing factor remains active until replacement succeeds.
- Creating an enrollment while a factor is active creates a pending replacement.
  Activation consumes the required current-password and MFA-management proof in
  the same transaction that replaces the old factor.
- Disabling or replacing active MFA requires the current password and a fresh
  verification using the current factor or one recovery code. Lost-factor cases
  use the authorized reset workflows instead.
- An SMS factor is bound to the verified mobile number captured at enrollment.
  Profile changes do not alter that destination.
- Five incorrect TOTP or SMS submissions invalidate the current MFA challenge.
- Login MFA challenges expire after 30 non-refreshing minutes. Separate login
  attempts may hold concurrent challenges; completing one consumes only that
  challenge.
- A TOTP factor stores its last successfully used time step. Acceptance updates
  it atomically, and the same step cannot satisfy another challenge.
- Resending an SMS challenge sends the same code without extending expiry or
  resetting its accumulated failure count.
- Confirming MFA for disable or replacement returns one random opaque
  `mfa-management` proof. MIA stores only its SHA-256 digest and binds it to the
  user and action. It expires after five minutes and is consumed atomically with
  one MFA mutation.
- A password, MFA configuration, ban-state, or account-state change deletes all
  outstanding MFA challenges and sensitive-action proofs for that user.

### MFA recovery codes

- Enabling MFA issues ten one-time recovery codes. Each code contains 16 random
  characters from the unambiguous Crockford Base32 alphabet and is displayed as
  four groups of four characters.
- MIA displays the plaintext codes only once and stores only non-reversible
  SHA-256 digests. Verification ignores display hyphens and ASCII letter case.
- If the activation response is lost, MIA cannot display that code set again. The
  user replaces the complete MFA factor to invalidate it and receive a new set.
- Each code can satisfy one MFA challenge and is permanently consumed after
  successful use.
- Replacing the MFA factor invalidates every unused recovery code from the old
  factor and issues ten codes for the new factor. MIA has no standalone recovery
  code regeneration operation.
- Recovery codes, their hashes, and submitted values are never logged or written
  to audit content.

### Student lost-factor recovery

- If a student-only account loses the active factor and all recovery codes, any
  supervisor sharing an assigned course with the student can perform a separate
  MFA reset.
- The reset removes the active MFA method and invalidates all recovery codes.
- The reset invalidates every existing browser cookie for the student and
  requires a fresh login followed by password replacement.
- MFA reset and subsequent password replacement are audited without recording
  secrets.

### Staff and administrator lost-factor recovery

- A supervisor, mentor, or administrator who has lost the active factor and all
  recovery codes requires an MFA reset by a different administrator.
- A user cannot approve their own MFA reset.
- An account with any administrator, supervisor, or mentor role always uses the
  staff reset path.
- The reset removes the active MFA method, invalidates all recovery codes,
  increments the account's security generation to invalidate every existing
  browser cookie, and requires a fresh login followed by password replacement.
- Every action is audited without recording secrets.
- If exactly one administrator account exists and it has lost both MFA and all
  recovery codes, the operator can perform a local server-only recovery action.
- Local recovery resets MFA, invalidates recovery codes, increments the account's
  security generation to invalidate every existing browser cookie, and requires
  a fresh login followed by password replacement.
- Local recovery is not exposed through a public web route.

### First administrator

- The operator creates the first administrator through a one-time local
  bootstrap action. The action uses either an interactive terminal dialogue or a
  complete noninteractive invocation with username, verified email, language,
  country, time zone, and a one-line password file.
- Bootstrap is available only while no administrator exists and is never exposed
  as a public web registration flow.
- Bootstrap requires a configured absolute data directory. A password file is a
  readable regular file containing one non-empty password line with an optional
  final LF or CRLF; all other password bytes remain unchanged.
- After the transaction commits, bootstrap confirms the username and the fixed
  SQLite path under the configured data directory. It never prints the password.
- After the first administrator is created, bootstrap is disabled and further
  administrators follow the authorized account-management workflow.

## Course lifecycle

A course has one of two states: inactive or active. MIA has no separate archived
state. A newly created course is inactive. Course names are globally unique.
MIA trims and NFC-normalizes the display name, applies Unicode case folding,
NFC-normalizes the folded value, and stores that key for uniqueness. SQLite
`NOCASE` is not used.

### Creation

Only an administrator creates a course. Creation means adding the course record
to MIA and assigning one or more supervisors in the same operation and SQLite
transaction. Every initial assignee must already hold the supervisor role. If
validation or any write fails, no course or assignment is created. Creation does
not make the course available to students.

### Course logo

- A course may have one optional logo.
- An administrator or a supervisor assigned to the course may upload, replace,
  or remove it.
- Logo upload uses the avatar image pipeline: signature-validated JPEG or PNG,
  at most 10 MiB, 40 decoded megapixels, and 10,000 pixels per source dimension.
- MIA applies orientation, strips metadata, preserves aspect ratio, fits the logo
  within 512 by 512 pixels, and stores one non-animated PNG.
- Anyone authorized to view the course may download its logo. Logo access never
  broadens course visibility.
- Course deletion removes the logo. MIA never serves the untrusted source image.

### Preparation

An assigned supervisor prepares the course by defining its learning goals,
curriculum context, AI tutor instructions, language, and course-wide material.

Course-wide material is processed and reviewed separately. At least one ready,
file-backed course-wide material must be approved before activation.

### Activation

An assigned supervisor activates a prepared course. Activation allows
supervisors to add students and allows students to start tutoring.

The course must have learning goals, AI tutor instructions, a valid language, and
at least one approved, ready, file-backed course-wide material before it can be
activated.

### Loss of approved material

- If an active course has no approved, ready, file-backed course-wide material,
  it remains active but students cannot start new tutoring sessions.
- Existing active sessions may finish, but revoked material is unavailable for
  subsequent retrieval.
- Approving course-wide material automatically permits new sessions again; the
  course does not require reactivation.

### Deactivation authority

- Any supervisor assigned to an active course can deactivate it.
- Administrator status alone does not grant deactivation authority; an
  administrator must also be assigned as a supervisor to that course.
- Course deactivation is audited.
- Deactivation immediately prevents students from starting new tutoring
  sessions in the course.
- Tutoring sessions already active at deactivation may finish and retain access
  to material that remains approved.
- An assigned supervisor can reactivate the course only when the activation
  requirements are satisfied.

### Browser readiness and destructive confirmation

- An authorized course read derives current readiness from source records rather than a maintained counter and separately
  reports activation and new-session eligibility.
- Assigned supervisors receive stable prerequisite blockers and only links they are authorized to follow. Students receive
  no preparation details, supervisor identities, or preparation links.
- Before course deletion, supervisor removal, or membership removal, the authorized actor reviews the exact target and its
  server-authored eligibility and consequences. The mutation requires that representation's strong ETag.
- The mutation atomically rechecks authorization, lifecycle guards, and the validator. A missing or stale validator changes
  no course data and requires the actor to review current state again.

### Course relationships

- Every existing course has at least one assigned supervisor.
- Only an administrator can remove a supervisor assignment. Removing the last
  supervisor is forbidden; staff account deletion requires a replacement first.
- A supervisor can add a student only to an assigned active course. Deactivation
  preserves existing memberships but blocks new ones.
- Any supervisor assigned to the course can remove a student from it.
- Student removal is rejected while that student has an active tutoring session
  in the course. Only the student can finish that session.
- Removing a student permanently deletes all data owned by that student in the
  course, including private material and files, tutoring sessions and chats,
  summaries and follow-ups, generated speech, mentoring records, mentor
  assignments, and related jobs.
- Student removal preserves the user account, global profile, roles, and data in
  other courses. It retains only one minimal, content-free audit event for the
  removal.
- Course-student removal and all database deletion effects occur atomically.
  Filesystem deletion follows the managed-file deletion contract.
- Removal does not wait for running workers or provider calls. Late attempts use
  the shared stale-result rule in the data-lifecycle section.

## Material

### Material scopes

Every material has exactly one immutable scope:

- **Course-wide material** belongs to a course and may become available to every
  student in that course.
- **Student-private material** belongs to its uploading student within a course.
  It is available as tutoring material for that student but does not become
  course-wide material available to all students.

Material names are unique within a course. MIA trims and NFC-normalizes the
display name, applies Unicode case folding, NFC-normalizes the folded value, and
stores that key for uniqueness. SQLite `NOCASE` is not used.
Every file in one material uses the same supported file format.
Website and YouTube link-only material is course-wide and created by an assigned
supervisor. Student-private material requires at least one uploaded source file.
Material selected by an active tutoring session cannot be deleted. The ordinary
deletion rules apply after the session is completed.

### Course-wide material

- course-wide means available to all students joining the course
- Supervisors assigned to the course manage course-wide material.
- A generated material brief is a draft that a supervisor can review and
  correct.
- Course-wide material has a separate, revocable approval flag.
- Approval requires a ready material and a non-empty brief. The approval action
  itself records the assigned supervisor's review; there is no separate reviewed
  state.
- A supervisor can grant or revoke approval subject to those requirements.
- Only approved course-wide material is visible to course students. Its source
  content is retrievable by the AI tutor only while it is also ready and
  file-backed.
- Revoking approval removes the material from subsequent student access and AI
  tutor retrieval.
- Changing the validated brief content of approved course-wide material
  atomically revokes approval and clears its approval attribution. A no-op brief
  submission does not revoke approval. The transaction writes content-free brief
  correction and approval-revocation audit effects. The revised brief requires
  explicit approval after review.

### Student-private material

- A student can upload material such as homework, worksheets, or exams.
- Student-private material requires no supervisor approval.
- Its identity and brief are available to its owner, the AI tutor, and supervisors
  assigned to the course. Its source content is retrievable only while the
  material is ready and file-backed.
- Assigned supervisors can inspect the source files, generated material brief,
  and completed session's full chat history.
- Mentors, other students, unrelated supervisors, and administrators who are not
  assigned as supervisors have no access.
- Student-private material remains usable by its uploading student but cannot be
  converted into course-wide material for all students.

### Deleting student-private material

- A student can delete private material they uploaded.
- Deletion removes the source files and generated material brief.
- Deletion removes material-owned jobs without waiting for running workers or
  provider calls.
- Deleted material is unavailable for future viewing, search, retrieval, and
  tutoring sessions.
- Existing chat messages and completed-session summaries are preserved, even if
  they discuss or quote the deleted material.
- Quoted or derived chat content may therefore remain without a retrievable
  source. This orphaned historical content is accepted behavior.
- A tutoring-session-summary job is not material-owned. If material was deleted
  before that job reads its identity, the generated summary omits the deleted
  material's name and type.
- MIA preserves a non-content audit record that the deletion occurred. The audit
  record must not contain the deleted source, generated brief, or extracted
  content.
- Deletion behavior for provider-held data and backups remains subject to the
  data-lifecycle policy.

### Material processing

File-backed material starts in a draft state. The uploader may add or remove
files while it is a draft and then explicitly finalize the material. Finalization
accepts only a draft or authorized retryable failed material. In one transaction,
MIA validates and freezes the complete file set, changes every file and the
material to processing, and inserts exactly one queued extraction job per file.
Any failure rolls back every state change and job insertion. A uniqueness
constraint prevents duplicate active extraction work.

- PDF, PNG, and JPEG use Mistral OCR.
- DOCX, UTF-8 text, and Markdown are validated and extracted locally with bounded
  parsers. These formats are not sent to Mistral.
- Successful material-summary completion validates and stores the brief, verifies
  that every file is processed, marks the job succeeded, and changes the material
  to ready in one transaction.
- Any terminal extraction or material-summary failure marks the job and material
  failed and cancels remaining material-owned jobs in one transaction. MIA never
  uses a partially processed material.
- A failed material may return to draft when its uploader removes or replaces a
  source file. Processing and ready materials have immutable file sets.
- After automatic attempts are exhausted, the authorized uploader may finalize a
  failed material again with unchanged or corrected files. This creates fresh
  material-processing jobs without exposing a generic job retry.
- A ready material cannot be reopened in the MVP. Correcting its source requires
  deleting and recreating the material.

Course-wide website and YouTube metadata may be finalized without a source file
only when an assigned supervisor provides a non-empty brief. An approved
link-only material is visible to students, but it does not satisfy course
activation or new-session readiness and provides no retrievable source content
to the AI tutor. Finalization validates the URL, absence of files, and brief and
changes the material directly from draft to ready in one transaction without
creating extraction or summary jobs.

After readable content is available, MIA generates one grounded material brief.
The brief is a version-1 object that requires `version`, `summary`, `subjects`,
`learning_goals`, `sections`, and `warnings`; `educational_level` is nullable.
Each section requires `sequence`, nullable `label`, `title`, `description`, and
`search_terms`. Unknown fields are rejected. The validated encoded object is at
most 256 KiB, and that encoded cap remains binding at maximum field counts.
String limits count Unicode code points and apply after the standard multiline
text normalization. The per-field limits are:

- `summary`: required, 1–4,000 code points and at most 16 KiB;
- `subjects`: 1–20 items, each 1–100 code points;
- `learning_goals`: 0–30 items, each 1–300 code points;
- `sections`: 0–500 entries;
- section `title`: required, 1–200 code points;
- section `label`: nullable, 1–100 code points when present;
- section `description`: required, 1–500 code points;
- section `search_terms`: 0–10 terms, each 1–50 code points;
- `warnings`: 0–20 items, each 1–500 code points;
- `educational_level`: nullable, 1–100 code points when present.

It contains:

- a concise overall description and purpose;
- the educational level when stated by the source;
- main subjects and concepts;
- learning goals explicitly supported by the source;
- a chapter, section, worksheet, or exercise outline;
- chapter and section labels when available;
- short topic descriptions and search terms;
- warnings for incomplete, unreadable, ambiguous, or poor-quality content.

For large material, the brief contains a concise overall summary and a
section-level outline rather than one long narrative.

The brief must not invent learning goals, educational levels, source locations,
or content. A job that cannot produce a trustworthy brief fails visibly.

MIA must not present OCR page positions as printed page numbers. OCR output may
not preserve the pagination of the original material, so source references use
material, chapter, and section labels.

A supervisor-edited brief on ready material is never regenerable through any
exposed operation; deleting and recreating the material is the only path to a
fresh generated brief. Brief generation in any remaining path, such as
re-finalization of a failed material, must not silently overwrite supervisor
edits. Student-private briefs inherit the source material's access rules and
require no review or approval.

Each source file has one normalized `content.jsonl`. Every line is a strict
version-1 object with positive contiguous `sequence`, nullable `chapter_label`,
nullable `section_label`, and `text`; unknown fields are rejected. Both the total
decoded segment text and encoded JSONL are limited to 512 MiB per material.

Complete material and session summaries use canonical order and source chunks of
at most 24,000 tokens, with at most 64 chunks and two reduction rounds. Material
files sort by ascending creation time and then ascending immutable file ID,
followed by segment sequence; chats use message sequence. Each model call has a
2,048-token output limit. Chunking prefers JSONL
segment or message boundaries; an oversized unit is split at a Unicode-safe token
boundary while retaining source identity. Reduction greedily combines ordered
summaries up to 24,000 input tokens per call. MIA checks full coverage before the
first provider call and fails visibly with `summary_input_too_large` rather than
sampling or truncating. Intermediate summaries remain in memory. A retry starts
from the beginning using current configuration and processing code and retains
only cumulative usage and sanitized failure state.

### Upload limits

- Upload file-size, material-size, and page-count limits are configurable by the
  server operator.
- MIA ships safe default limits.
- Operators may lower the defaults or raise them only up to fixed MIA safety
  maxima.
- MIA rejects configuration above a hard maximum and rejects oversized uploads
  before sending content to an external provider.
- No configuration can make upload sizes or page counts unbounded.
- The default maximum size of one uploaded file is 100 MiB.
- The hard maximum size of one uploaded file is 512 MiB.
- The default maximum combined size of all files in one material is 200 MiB.
- The hard maximum combined size of one material is 512 MiB.
- The default maximum page count of one material is 1,000 pages.
- The hard maximum page count of one material is 2,000 pages.
- Each PDF page and each JPEG or PNG file counts as one page. DOCX, text, and
  Markdown are non-paged and remain subject to their byte and expansion limits.
- The maximum number of source files in one material defaults to and cannot
  exceed 200. Operators may lower it.
- The default decoded-image limit is 40 megapixels per JPEG or PNG. Operators may
  raise it only to 100 megapixels. MIA also rejects an image wider or taller than
  20,000 pixels.
- A DOCX archive may contain at most 10,000 entries, expand to at most 512 MiB in
  total, contain no entry larger than 100 MiB, and have no entry with an expansion
  ratio above 100:1.
- MIA splits OCR work into provider-compatible requests without bypassing the
  material-level limits.
- An original filename must be valid NFC UTF-8. MIA removes Unix and Windows path
  components, trims surrounding whitespace, and rejects an empty basename, NUL,
  controls, bidi controls, `/`, and `\`. The basename is limited to 255 Unicode
  code points and 1 KiB and is never used as a filesystem path.

### Malware boundary

- MIA does not integrate with or require a malware scanner.
- Uploaded files are untrusted and MIA must never execute them, their macros,
  scripts, or embedded active content.
- MIA validates file signatures and detected media types rather than trusting
  filenames or client-provided content types.
- Downloads use safe content-disposition and content-type headers and prevent
  browser content sniffing.
- MIA does not claim that an accepted or approved file is malware-free.
- The operator is responsible for deciding whether additional infrastructure or
  procedures are required for malware scanning.

### Accepted upload formats

MIA accepts only:

- PDF;
- JPEG, using `.jpg` or `.jpeg`;
- PNG;
- UTF-8 plain text using `.txt`;
- UTF-8 Markdown using `.md`;
- Office Open XML documents using `.docx` without macros.

MIA rejects archive files, executables, HTML, SVG, macro-enabled Office files,
and every format not explicitly listed. A filename extension alone never makes a
file acceptable.

MIA rejects encrypted or password-protected PDF and DOCX files. It does not
request, accept, store, or forward document passwords. The uploader must provide
an unprotected copy.

DOCX extraction disables entities and external relationships. It extracts the
main document, tables, headers, footers, footnotes, endnotes, and inserted tracked
changes. It ignores comments, deleted tracked changes, hidden text, macros,
embedded objects, and active content.

Before OCR, a bounded PDF parser establishes page count and rejects malformed
cross-references, encryption, embedded files, JavaScript, launch actions, and
excessive pages. Mistral receives page content only, never active attachments.

If any file in a multi-file material is corrupt, malformed, or cannot be
processed, MIA marks the whole material faulty and prevents approval or tutoring
use. Successful intermediate results may be retained for retry, but MIA does not
present the material as complete or silently exclude the failed file. The
uploader must remove or replace the failed file before processing can succeed.

### Copyright responsibility

- The uploader is responsible for having any rights or permissions required to
  use uploaded material.
- MIA does not request an attestation, require license metadata, validate
  ownership, or assess whether content use is lawful.
- MIA accepts content regardless of copyright status when it satisfies the
  technical upload and authorization requirements.
- The operator is responsible for establishing and enforcing any institutional
  copyright policy outside MIA.

### External links

- Website and YouTube material stores an external link as metadata only.
- MIA does not fetch, scrape, download, preview, or follow redirects for an
  external material URL on the server.
- A link alone provides no source content to OCR, material-summary, retrieval, or
  tutoring operations.
- An uploader must provide a separate supported file when the linked content
  should be available to the AI tutor.
- Model output cannot cause MIA to request an external URL.
- External material links must use HTTPS. MIA rejects HTTP and every other URL
  scheme.
- Links must be ASCII and no longer than 2,048 bytes. They cannot contain user
  credentials. MIA lowercases the host and preserves path, query, fragment, and a
  normal explicit port.
- A YouTube material link must use `youtube.com`, `www.youtube.com`,
  `m.youtube.com`, or `youtu.be`.
- Multiple materials may use the same external URL. MIA does not canonicalize
  URLs for uniqueness; material-name uniqueness remains authoritative.

## Tutoring sessions

### Starting a session

- Session creation includes a browser-generated canonical lowercase UUID v4
  request identifier scoped to the student.
- While the created session remains active, replaying the same identifier with
  the same course and selected material set returns that session. Different
  creation content is a conflict.
- After that session is completed, every reuse of its creation request identifier
  is a conflict.
- Session-creation request-ID guarantees last only while the session row remains
  retained. Authorized account, course, or membership deletion may cascade the
  session and deletes that idempotency history; MIA stores no tombstone.
- A student starts a session in one joined, active course.
- A student can have only one active tutoring session across all courses.
- The student may optionally select one or more primary materials.
- The student may instead start with only an intent, such as "Let's learn
  vocabulary."
- Any student-selected course-wide material must be approved.
- Every selected material with source content must be ready and file-backed.
- Any student-selected private material must belong to the active student and the
  session's course. It requires no approval.
- Link-only material may supply authorized identity and brief context but has no
  retrievable source content.
- When no primary material is selected, the AI tutor can discover suitable
  authorized material after the session intent becomes clear.
- Tutoring sessions have no inactivity timeout and do not expire automatically.
- An inactive session remains the student's one active session until it is
  explicitly finished.
- Students cannot abandon or discard an active session. Finishing is their only
  action for ending it.
- Only the student who owns the active session can finish it. Supervisors and
  administrators cannot force completion.
- After authentication, the student can resume the same active session and
  conversation after logout, browser closure, network interruption, or device
  change.
- One authenticated non-refreshing read discovers the student's active session
  across all courses and returns its session identity plus authorized course ID
  and name. It returns a successful explicit null when no active session exists.
  The read remains authoritative after course deactivation, creation conflict,
  or an ambiguous creation response; clients neither enumerate courses nor
  automatically replay creation. A database lookup failure returns the shared
  safe HTTP `500 internal_error`, not the successful no-session representation.

### Initial context

At session start, the AI tutor receives:

1. Global AI tutor instructions.
2. The course brief and course-specific instructions.
3. The student brief and student-specific instructions.
4. The identity, material brief, and content outline of any material selected by
   the student.
5. The previous completed session's summary and follow-up for the same student
   in the same course, when available. When a supervisor has corrected them, the
   corrected version is used.

MIA does not send the complete content of every available material at session
start. It may include complete selected-material content only when it contains at
most 8,000 Unicode code points and 32 KiB and fits within the total input limit.

One tutor request has a fixed 32,000-token model-input budget and a 2,048-token
output limit. MIA uses a tokenizer matching the configured model before sending
the request. Required instructions and the current message take priority. The
remaining budget includes the newest complete conversation turns that fit; older
turns are omitted without a hidden rolling summary.

### Material retrieval

- The AI tutor can search student-selected material and other material available
  to the active student.
- Once the session intent is clear, the AI tutor can choose suitable working
  material from authorized search results.
- The AI tutor can request bounded excerpts from a material, chapter, or
  section.
- Retrievable source content belongs to the session's course and is either ready,
  file-backed, approved course-wide material or ready, file-backed private
  material owned by the active student. Course activity is required at session
  start, not while an existing session continues after deactivation.
- MIA authorizes every retrieval request. A model tool request never grants
  access by itself.
- MIA records every material actually used during the session.
- A material counts as used only when MIA returns its complete small content or
  an authorized excerpt to the tutor model. Search-only candidates do not count.
- Retrieval streams authorized `content.jsonl` segments without a persisted
  index. Search NFC-normalizes and Unicode-case-folds query terms, tokenizes on
  Unicode letter and number boundaries, and ranks by matched-term count and
  frequency. Ties use material ID, file ID, and segment sequence; no matching
  term means no result.
- One response performs at most three retrieval rounds and receives at most eight
  excerpts. Each excerpt contains at most 4,000 Unicode code points and 16 KiB
  and must fit the total model-input budget.
- An excerpt may include at most one adjacent segment on each side when the
  combined result remains within those limits. Overlapping excerpts are
  deduplicated.
- Retrieval results identify the material, chapter, and section when available.
- The AI tutor should cite the material, chapter, or section when directing a
  student to another source. It must not claim an OCR page position is the
  printed page number.

### Response delivery

- A student message contains at most 8,000 Unicode code points and 32 KiB of valid
  UTF-8.
- A tutoring session has at most one generating tutor response and one queued
  student message. A further message is rejected until the queue slot is free.
- The owning student can read one atomic, authoritative current-work
  representation. It explicitly distinguishes idle, generating, queued,
  generating with queued work, and completed state; identifies the immutable
  generating response and queued message and response; reports remaining queue
  capacity; and gives independent server-authored eligibility for submit, queue,
  Stop, retry, reconnect, and finish. Reconnect eligibility identifies the
  immutable response whose SSE stream can be resumed.
- Current-work reconciliation may bind a retained message request ID or immutable
  response ID. It returns matching committed work in the same snapshot or an
  explicit null match. Browsers use this after conflicts, disconnects, and
  transport-ambiguous submit, retry, Stop, or finish outcomes rather than
  automatically replaying unsafe requests.
- A queued message starts automatically after the generating response completes,
  fails, or is interrupted. Preserved partial response text is part of the
  conversation context when generation starts.
- The student may interrupt a queued response before provider work starts. MIA
  preserves the student message, marks its response interrupted, and makes no
  provider request.
- Session completion is rejected while a response is generating or queued.
- AI tutor responses appear incrementally while they are generated.
- The product must not wait for the complete model response before displaying
  available text to the student.
- MIA delivers incremental tutor-response events to the browser using
  Server-Sent Events as defined by the current API design.
- A client disconnect does not cancel response generation.
- MIA preserves output received from the provider while the student is
  disconnected.
- MIA persists streamed output after either 16 KiB of new UTF-8 text or one
  second, whichever occurs first, and always before committing a terminal state.
- After reconnecting, the student receives the preserved output and continues
  the same response stream without starting a duplicate model response.
- Establishing or reconnecting a stream cannot lose text generated between the
  initial snapshot and live delivery.
- If generation finishes while disconnected, the completed response is
  available when the student resumes the session.
- The student can stop a response while it is being generated without ending
  the tutoring session.
- MIA cancels the provider operation, preserves received text, and marks the
  response as interrupted rather than complete.
- If provider cancellation cannot be confirmed, MIA keeps the interrupted state,
  stops accepting output, discards late events, and logs only a sanitized
  cancellation failure.
- MIA does not automatically retry an intentionally stopped response. The
  student can continue with another message or explicitly retry the interrupted
  response.
- Each student-message submission includes a client-generated request
  identifier scoped to the tutoring session. It is a canonical lowercase UUID v4.
- Retrying the same identifier and content returns the existing message and
  response operation without creating another provider request, including after
  session completion. This read-only replay does not create or change data.
- A request that would create a new message is allowed only while the tutoring
  session is active.
- Reusing an identifier with different content is rejected as a conflict.
- Message request-ID guarantees last only while the message and session data are
  retained. Authorized account, course, or membership deletion may cascade that
  data and removes the history without a tombstone.
- If the provider fails after returning partial output, MIA preserves the text
  and marks the response as failed and incomplete.
- MIA shows the failure to the student and does not retry automatically.
- A response retry is allowed only while the session has no generating or queued
  work and remains active. The retry becomes the sole queued response for the
  existing message. Completed sessions reject new message creation and response
  retry but still return retained identical message replays.
- On startup, MIA resumes queued responses but marks every response stranded in
  generating state as failed with a safe restart code. It never recreates an
  uncertain provider request automatically.
- During graceful shutdown, MIA starts no queued response. It allows current
  generation to finish for up to 30 seconds, then cancels and marks remaining
  generation failed. Queued responses remain queued for startup recovery.
- The student can explicitly retry a failed or interrupted response. A retry
  creates a new response linked to the same student message and its failed or
  interrupted response without deleting or rewriting either.
- Each explicit retry carries a canonical lowercase UUID v4 request ID bound to
  its target. An identical replay returns the existing linked response even
  after completion; reuse for another target is a conflict. Its history is
  retained only with the owning response and session data.

### Completion and oversight

- While a session is active, assigned supervisors can see its status but cannot
  read its chat messages. Active-session status includes the session's start
  instant and its last-activity instant.
- Completing a session queues creation of a session summary and follow-up.
- Queue creation and session completion occur atomically.
- Authorized completed-session reads explicitly report summary lifecycle as
  queued, generating, automatic-retry-scheduled, generated,
  supervisor-corrected, terminal-failure, or unavailable. This lifecycle is
  independent from nullable summary content and generic job-list visibility.
- Summary lifecycle exposes only its authoritative state-change instant, current
  read instant, and scheduled retry instant when applicable. It exposes no job
  identity, attempt details, provider payload, or generic job-retry action.
- The result outlines strengths, weaknesses, and suggested next steps.
- The student can view their progress summary.
- An assigned supervisor can correct only the summary and follow-up of a completed
  session. The update records supervisor attribution and one content-free audit
  event and marks queued or running summary work cancelled in the same
  transaction. An in-flight provider call may continue, but its result cannot
  commit over the correction.
- If a completed session has no summary, no summary job is queued or running, and
  at least one earlier summary job failed terminally, an assigned supervisor can
  request summary generation again. Eligibility is server-authored separately
  from lifecycle, is false for students and every other state, and is rechecked
  atomically by the action. MIA never silently overwrites a supervisor correction.
- Chat history is immutable. Correcting a summary does not rewrite the chat.
- Supervisors assigned to the course can inspect completed sessions, including
  full chat histories and student-private material used in the session.
- Mentors cannot access tutoring chat histories.

## Mentoring

- Mentoring is the last resource for an educational difficulty. Before
  suggesting it, the AI tutor must make a meaningful effort using clarifying
  questions, alternative explanations, examples, guided exercises, and relevant
  authorized material.
- The AI tutor may suggest mentoring only when those attempts do not resolve the
  student's difficulty.
- At least one mentor must be assigned to the student in the active course for
  new mentoring suggestions or requests to be available.
- When no mentor is assigned, MIA disables new suggestions and requests.
  Existing mentoring records remain visible and cancellable while assigned
  supervisors restore an assignment.
- `mentoring_requests_allowed` independently controls whether the student can
  create a new request. When it is false, MIA does not offer or suggest creating
  a new request. It does not remove mentor assignments or cancel existing
  requests or scheduled sessions.
- A student-facing eligibility read evaluates current course activity,
  membership, `mentoring_requests_allowed`, and a qualifying mentor assignment
  atomically. Disabled results do not identify which private setting or
  assignment caused the result. A new request repeats all checks in its
  transaction; an earlier eligibility read is never authorization.
- The mentor receives the mentoring topic, not private uploads or tutoring chat
  history.
- A new mentoring request is unassigned even when one or more mentors are
  assigned to the student. An assigned course supervisor selects one of the
  student's assigned mentors before mentor response or scheduling work continues.
- An assigned supervisor may directly replace the mentor on an open mentoring
  record with another mentor assigned to the student. Only the mentor reference
  changes: proposed and scheduled times, meeting details, prior response, and
  immutable response authorship are preserved.
- A student may include a proposed appointment time when creating a mentoring
  request. After supervisor assignment, the mentor can respond, confirm or change
  the schedule, and provide meeting details.
- Mentoring sessions take place outside MIA. MIA provides no live mentor chat,
  audio, or video channel.
- The mentor supplies meeting instructions or an external HTTPS meeting link.
- MIA stores and communicates the schedule and meeting instructions but does not
  fetch or validate the external service's content.
- The student or an assigned course supervisor can cancel an unscheduled request.
- The student or assigned mentor can cancel a future scheduled session.
- Cancellation records the actor and time. Participants and assigned supervisors
  see the updated state when viewing the mentoring session.
- The student or assigned mentor can reschedule a future mentoring session
  directly without acceptance by the other participant.
- Rescheduling records the actor, previous time, and new time and immediately
  updates the state visible to the other participant and assigned supervisors.
- MIA does not track attendance or no-shows for external mentoring sessions and
  applies no no-show status or penalty.
- Only the assigned mentor can mark a session completed, and only after its
  scheduled time has begun.
- Completion eligibility is viewer-specific and based on current assignment,
  schedule, lifecycle, account scope, and server time. When the assigned mentor
  is too early, MIA may return the authoritative UTC instant for another explicit
  check, but browser time never authorizes completion. Completion repeats every
  check atomically and stale or ambiguous results are reconciled from the current
  mentoring session without automatic replay.
- Course deactivation blocks new mentoring requests but does not stop triage,
  responses, rescheduling, cancellation, or completion of existing work.

## Product notifications

- MIA sends no ordinary workflow notifications in the initial release.
- Users see current course, material, tutoring, mentoring, and job state when
  they use MIA.
- MIA provides no notification subscriptions, quiet hours, or workflow SMS.
- Invitation email, password-recovery email, MFA codes, and mobile-verification
  codes are required account or security delivery and are not product
  notifications.

### Mentor removal and triage

- An assigned supervisor may remove any mentor assignment, including the last.
  New student-facing mentoring requests remain disabled until another mentor is
  assigned.
- Removing a mentor from a course drops all of that mentor's student assignments
  in the course.
- Open mentoring work assigned to a removed mentor returns to supervisor triage:
  MIA clears the current mentor, proposed and scheduled times, and meeting
  details while preserving the request topic and any immutable mentor response.
- This clearing rule applies to mentor removal, not direct reassignment of an
  open mentoring record.
- Mentor removal, assignment deletion, open-work triage, and audit events occur
  in one transaction.
- Before mentor removal, MIA returns the current consequences with a strong
  validator covering the assignment and affected open work. Removal requires the
  exact reviewed validator; a missing or stale precondition performs no mutation.
- Completed mentoring history remains, but deletion of its mentor or closure
  actor may leave historical actor fields absent.

## Time and scheduling

- Every API field representing an instant uses RFC 3339 in UTC with the `Z`
  suffix in requests and responses.
- MIA accepts at most nine RFC 3339 fractional digits and normalizes persisted and
  returned instants to exactly six fractional digits.
- Clients convert local input to UTC before sending it. MIA rejects instant
  values with a non-zero numeric offset.
- Every user has a preferred IANA time zone. A student-only account cannot change
  it.
- Any supervisor who shares an assigned course with a student-only account can
  set that student's preferred time zone under the supervisor-managed profile
  policy.
- Clients display instants in the viewer's preferred time zone.
- MIA uses the recipient's preferred time zone for server-generated
  communications.
- A one-time appointment is stored as one UTC instant and does not move when a
  user changes their preferred time zone.
- A local-time schedule uses separate local-time and IANA time-zone fields when
  its meaning must survive daylight-saving changes.

## External services

- OpenAI provides AI tutoring and model-driven background processing.
- Mistral provides OCR for supported uploaded documents.
- SMTP is required for outgoing email.
- ClickSend supports optional SMS features.
- ElevenLabs supports optional text-to-speech.
- MIA must identify what data is sent to each provider. Self-hosting must not be
  presented as keeping all processing local.
- External failures must be visible and must not cause MIA to invent successful
  results.
- Background retries may repeat an ambiguous paid provider operation. Lease and
  guarded-commit rules prevent duplicate committed output, but MIA does not
  guarantee provider-side idempotency or identical requests across retries,
  configuration changes, restarts, or upgrades.
- MIA does not enforce monetary budgets or provider-account spending limits.
- MIA exposes available operational usage and provider quota failures without
  claiming that usage data is an authoritative billing total.
- Background-job usage is an unattributed, model-agnostic cumulative counter. It
  may combine attempts made with different current configurations and cannot be
  used to identify which model incurred particular usage.
- The operator configures provider quotas, plans, billing controls, and alerts.
  MIA's own upload and rate limits continue to apply.
- For interactive tutoring, OpenAI receives tutor instructions, relevant course
  and student context, the current student message, and only the newest complete
  conversation turns that fit the fixed request budget. It may also receive
  authorized material briefs, selected content, retrieval excerpts, and
  same-course previous-session summaries when needed.
- For tutoring-session summarization, OpenAI receives the complete chat from the
  completed session and the names and types of material used. MIA processes the
  complete chat in deterministic bounded chunks and reduction rounds without
  sampling or silent truncation.
- Mistral receives bounded PDF or image page content only for OCR. SMTP receives
  recipient and sender addresses and English plain-text invitation or recovery
  content. ClickSend receives the destination number, SMS message or code, and
  configured sender. ElevenLabs receives completed tutor-response text and the
  selected voice ID.
- MIA sends no raw password, password hash, MFA secret, browser cookie, or API
  credential as provider content. Provider retention is governed by the
  operator's provider agreements. Local deletion does not guarantee deletion of
  provider copies; the operator controls provider-side deletion.

The default OpenAI model for tutoring and background jobs is
`gpt-5.6-terra`. A configured model must support the Responses API and the tools
required by its workload.

### Generated speech retention

- Speech generation is available only for a completed tutor response. Preserved
  text from failed or interrupted responses is not eligible.
- MIA requests, stores, and serves generated speech as MP3 with media type
  `audio/mpeg`.
- The server configuration defines generated-speech retention in days.
- The default retention is 30 days from generation.
- Accessing generated speech does not extend its retention.
- MIA deletes expired generated speech. It may generate the audio again on
  request when text-to-speech is available.
- Cached speech is reused only while the completed tutor-response content and
  requested voice match. Concurrent requests reuse one generation operation. An
  authorized retry transitions the same failed cache record back to generating.
- At startup, after expired-speech cleanup, MIA changes every speech row stranded
  in generating state to failed with a sanitized restart code and removes any
  associated partial or published MP3. It does not repeat the uncertain provider
  request automatically; a later authorized request uses the ordinary retry
  transition on that row.
- Speech generation requires an explicitly stored ElevenLabs voice ID containing
  at most 128 printable ASCII characters. Absence returns a stable validation
  error; MIA provides no provider voice discovery. Only an assigned supervisor
  sharing a course with a student-only account may set, change, or clear that
  student's voice. Students cannot select or edit it themselves.
- One generated MP3 is limited to 25 MiB. MIA validates its MP3 signature and
  complete bounded response, writes a mode-`0600` temporary file, publishes by
  atomic rename, and removes partial output after failure.

## Data and deployment

- MIA is a self-hosted Linux server used through a browser.
- The backend/API server is distributed as one dependency-free binary.
- The frontend is maintained and distributed separately.
- The backend serves the installed frontend from a configured document root.
- Static serving accepts GET and HEAD only, serves regular files, rejects symlink
  escapes, dotfiles, and directory listings, and falls back to `index.html` only
  for unresolved non-API GET or HEAD routes.
- Static responses use `X-Content-Type-Options: nosniff`, a restrictive
  `Referrer-Policy`, frame denial, and a strict baseline Content Security Policy
  (`default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors
  'none'`) shipped with static serving. The policy may be loosened during
  frontend integration only when the frontend demonstrably requires it.
- SQLite is the database.
- Uploaded and generated files are stored in the data directory.
- The database and data directory form one consistent data set and must be
  backed up and restored together.
- Startup first removes expired generated-speech rows and files, then marks
  stranded generation failed and removes incomplete output. It next logs an error
  and exits when SQLite references a missing source file, normalized
  `content.jsonl`, or unexpired available generated-speech file. It does not
  repair or downgrade those remaining states automatically. All required-file
  validation succeeds before startup removes any filesystem orphan. Avatar and
  course-logo absence is normal because their fixed-path presence alone determines
  availability.
- The configured base data directory must exist. MIA creates missing required
  subdirectories at startup.
- HTTPS through a reverse proxy is mandatory.
- Every public and authenticated API response uses `Cache-Control: no-store`,
  including JSON, errors, bodyless responses, images, downloads, audio,
  normalized-content streams, and SSE. Authenticated images have no cache or
  validator exception. Reverse proxies must preserve or strengthen this policy,
  disable API storage and replay, and avoid buffering SSE.
- API error bodies are complete valid UTF-8 JSON:API documents no larger than
  65,536 bytes. They contain only stable safe registry fields and never expose
  secrets, credentials, cookies, personal data, internal paths, prompts, message
  bodies, or provider payloads. An oversized error is replaced with bounded
  generic text while preserving its mapped status, stable code, and
  existence-hiding behavior; MIA never byte-truncates an error document.

## Data lifecycle

### Retention

- MIA retains operational data until an authorized deletion occurs.
- MIA does not automatically expire profiles, course data, material, chats,
  session summaries, mentoring records, or audit events.
- Generated speech is the explicit exception and follows its configured
  retention period.
- MIA provides no user data-export feature.

### Student deletion

- Deleting a student-only account deletes the user account and all operational
  data belonging to that student across all courses. Accounts with any staff role
  use the staff-deletion workflow.
- This includes profile data, memberships, student-private material and briefs,
  tutoring sessions, chat history, session summaries and follow-ups, mentoring
  records, generated speech, and pending invitations or challenges.
- Deletion does not coordinate with running goroutines or provider calls. Those
  calls finish or reach their existing deadline. Late commits may update only an
  existing target row. If a guarded update affects zero rows, the attempt marks
  any synchronized in-memory response stale, closes subscriber queues, discards
  later provider events and results, removes newly published output, and stops.
  It must not upsert deleted data, requeue work, or retry the provider request.
- MIA preserves only a minimal, content-free audit record of the deletion.
- The retained audit record contains the event type, time, acting user, and an
  random de-identified subject reference. It contains no profile data, message
  content, material content, or direct subject identifier.
- Deletion generates one random UUID v4 fingerprint for the deleted identity and
  uses it in retained audit records. MIA preserves no mapping from that
  fingerprint to the deleted ID.

### Course deletion

- Only an administrator can permanently delete a course.
- Assigned supervisors can deactivate but cannot delete a course.
- The course must be inactive and have no active tutoring sessions before
  deletion.
- If either condition is not satisfied, MIA rejects deletion without changing
  the course or its sessions.
- Deleting a course deletes the course and all data scoped to it.
- This includes course-wide and student-private material, generated briefs,
  memberships and assignments, tutoring sessions and chats, summaries and
  follow-ups, mentoring records, jobs, and generated speech.
- User accounts remain, together with data belonging to other courses.
- MIA preserves only a minimal, content-free audit record of the deletion.
- Course deletion does not wait for running workers or provider calls. Late
  attempts follow the same zero-row stale-result rule as student deletion.

### Destructive-operation concurrency

Student-only account deletion, staff account deletion, course deletion,
course-membership removal, direct material deletion, and their cascades do not
coordinate with running workers or provider calls. Database deletion atomically
removes targets and owned jobs. Late attempts may update existing rows only. A
zero-row guarded update discards the result, removes output newly published by
that attempt, and never upserts, requeues, or repeats the provider request.
Independent active-session and course-state deletion guards still apply.

### External copies and backups

- MIA deletion applies only to its live SQLite database and data directory.
- MIA does not request or guarantee deletion from external providers or
  operator-managed backups.
- The operator is responsible for provider-side data and backup retention and
  for preventing restoration of data that should remain deleted.
- MIA must document this limitation and the data sent to each provider.

### Corrections

- Student-only accounts cannot correct their own profile fields in the MVP.
- Assigned supervisors can correct non-security student profile fields,
  generated material briefs, tutoring-session summaries, and follow-ups.
- Changing an approved course-wide material brief atomically revokes approval and
  requires explicit review and reapproval. An unchanged validated brief is a
  no-op.
- Every supervisor correction is audited.
- Chat histories are immutable.

## Security and privacy requirements

- Treat student records, chats, assessments, private uploads, and contact details
  as sensitive data.
- The MVP relies on private SQLite, data-directory, and backup access rather than
  application-layer encryption for stored fields. Operators must treat anyone
  with storage access as fully trusted.
- Apply authorization to every sensitive resource and field.
- Keep uploaded and generated files outside the public frontend root.
- Authorize every download and material retrieval.
- Never expose password hashes, MFA secrets, session tokens, provider
  credentials, private prompts, or unrestricted provider payloads.
- Treat uploads, OCR output, external content, user messages, and model output as
  untrusted input.
- Model output cannot authorize a platform action.
- External operations require bounded inputs, timeouts, cancellation, and
  bounded retries.
- Startup does not contact external providers. A provider outage disables or
  fails only operations that need that provider; it does not prevent MIA from
  serving other features.
- Request-path email, SMS, speech, and streamed tutor operations do not retry
  automatically after an ambiguous failure. Users may retry through the normal
  authorized workflow and rate limits.
- Automated tests must not call paid or production providers.

### Safeguarding and emergencies

- MIA is not an emergency service and does not provide real-time human
  monitoring.
- MIA does not automatically notify supervisors, mentors, guardians, emergency
  services, or any other person when a message suggests immediate danger,
  self-harm, abuse, or another safeguarding concern.
- The AI tutor responds supportively, avoids diagnosis, and encourages the
  student to contact a trusted person or appropriate local emergency service.
- Supervisors provide local emergency and safeguarding guidance outside MIA. MIA
  does not store or display operator-configured safeguarding text.
- MIA must not state or imply that an alert was sent or that a person will
  respond.
- Mentoring is an educational support feature and must not be presented as an
  emergency or safeguarding channel.
- The message remains part of the normal tutoring history and may be seen later
  by an assigned supervisor under the ordinary completed-session access rules.
  This is not an emergency notification or response guarantee.

### Rate limiting

- Every unauthenticated API endpoint is rate limited.
- MIA applies a global token bucket of 60 unauthenticated requests per minute per
  source IP with a burst capacity of 30. Stricter endpoint limits also apply.
- Authentication and recovery limits consider both source IP and the relevant
  account, username, invitation, or challenge identifier. Neither dimension is
  sufficient by itself.
- Login, password reset, invitation lookup and acceptance, registration, MFA
  enrollment verification and resend, MFA challenge verification and resend, and
  mobile-number verification and resend require dedicated limits.
- Login failures use progressive delays and temporary throttling rather than a
  permanent account lockout that an attacker could use for denial of service.
- Login permits five failed attempts per normalized username and 30 failed
  attempts per source IP in a rolling 15-minute window. It applies retry delays
  of one, two, and four seconds after failures two, three, and four. MIA returns
  `429` with `Retry-After` during a delay instead of sleeping in the handler.
- The fifth username failure blocks further attempts until fewer than five
  failures remain in the rolling window. A successful login clears that
  username's failure and backoff state but does not clear source-IP failures.
- Password recovery permits three requests per normalized submitted identifier
  and ten per source IP in a rolling hour. The public response is identical when
  an account is absent, a limit applies, or email is sent.
- Public invitation preview and acceptance permit ten attempts per submitted
  token digest and 30 per source IP in a rolling hour.
- Password-reset submission permits five attempts per token digest and 20 per
  source IP in a rolling hour. Password-policy validation failures count but do
  not consume an otherwise valid reset token.
- MFA and recovery-code verification permit ten attempts per account and 30 per
  source IP in a rolling 15-minute window, in addition to each challenge's
  five-attempt limit.
- Mobile-number verification permits ten attempts per account and 30 per source
  IP in a rolling 15-minute window, in addition to each challenge's five-attempt
  limit.
- Rate-limited responses use HTTP `429 Too Many Requests`, a stable API error
  code, and `Retry-After` when a retry time is known. Public password-recovery
  requests are the exception and retain their indistinguishable success response.
- Rate-limit behavior must not reveal whether a username, email address, mobile
  number, invitation, or other sensitive identifier exists.
- Client-IP limits use only addresses derived through trusted proxy transports.
- Loopback TCP peers and permission-controlled Unix listeners are trusted proxy
  transports. MIA accepts only a bounded `X-Forwarded-For` chain and falls back
  to the direct peer or one local Unix-socket identity when the chain is invalid.
- Limiter state is bounded so arbitrary identifiers cannot exhaust memory or
  persistent storage.
- Process-level limiter state holds at most 50,000 keys. MIA removes expired keys
  first and then evicts least-recently-used keys when necessary. Entries expire
  after their final applicable window.
- Limits are fixed by MIA and are not operator-configurable. Error responses
  must not disclose internal thresholds when doing so would weaken abuse
  controls.
- Fixed per-IP limits are shared by every user behind one NAT address, such as a
  school network; classroom-scale bursts can temporarily throttle logins or
  recovery from that address. This is an accepted, documented MVP limitation
  with no operator override.
- Security-relevant throttling is audited without recording passwords, MFA or
  SMS codes, tokens, or complete request bodies.
- The MVP has no separate rate limits for authenticated tutoring, upload,
  material-finalization, or speech operations. Their fixed input, concurrency,
  upload, and provider deadlines still apply.
- Unauthorized requests retain ordinary caller and IP limits but create no
  limiter key derived from a hidden course, student, subject, or resource.
- Audit action names are stable dotted identifiers with bounded, event-specific
  typed metadata. The initial allowlist grows with implemented vertical slices
  and covers authentication failure, throttling, denied mutation, security
  change, destructive action, and provider or job failure.
- Ordinary denied reads, including private-material and audit-log probes, are not
  persisted as individual audit events.

## Acceptance scenarios

The completed product must support at least these observable scenarios:

1. An administrator creates a course, assigns supervisors, and cannot access
   student-private data without a supervisor assignment.
2. A supervisor prepares and activates a course after its required content and
   approved material are present.
3. Revoking course-material approval prevents subsequent student access and AI
   tutor retrieval.
4. A student uploads private material, uses its ready, file-backed content in
   tutoring without an approval step, and later deletes its source and brief
   without deleting the completed chat.
5. An assigned supervisor can inspect that private material and the completed
   session history, while a mentor and unrelated users cannot.
6. A student starts with only "Let's learn vocabulary." The AI tutor discovers
   suitable authorized material, requests a bounded excerpt, and cites its
   location.
7. MIA rejects a model request for unapproved or non-ready course-wide material,
   cross-course material, or private material that is non-ready, not file-backed,
   or owned by another student.
8. A student cannot start a second session while another session is active.
9. Two users in different time zones see the same appointment instant converted
   to their own preferred time zones.
10. A provider or processing failure remains visible and does not create a
    successful-looking summary or material state.
11. MIA offers no new mentoring request when the student has no assigned mentor
    in the active course, while existing triaged work remains visible.
12. Disabling `mentoring_requests_allowed` blocks new requests without cancelling
    existing requests or scheduled sessions.
13. Removing a mentor returns affected open mentoring work to supervisor triage
    without losing its topic or prior immutable response.
14. Repeated unauthenticated requests are throttled without revealing whether
    the submitted account or invitation exists.
15. Repeated login failures produce temporary throttling rather than a permanent
    account lockout.
16. Deleting a student removes that student's operational data across all
    courses while preserving only a content-free audit record.
17. Deleting a course removes its course-scoped data without deleting associated
    user accounts or their other-course data.
18. A supervisor corrects a generated session summary without changing the chat
    history, and MIA audits the correction.
19. A safeguarding-related message receives supportive guidance without sending
    an automated alert or claiming that a person is monitoring the conversation.
20. A user cannot self-register through a public workflow.
21. A remote user cannot access first-administrator bootstrap, and bootstrap is
    unavailable after the first administrator exists.
22. A newly provisioned student must replace the temporary password before using
    any other authenticated feature.
23. MIA accepts a 12-character password without composition-rule characters and
    rejects a known-common password of the same length.
24. An active browser session expires after 30 minutes of inactivity and cannot
    remain valid for more than 12 hours after authentication.
25. The same account can be authenticated on two devices, while a student still
    cannot start a second concurrent tutoring session.
26. Revoking the last course-wide material approval blocks new sessions without
    deactivating the course; approving material permits them again.
27. An administrator cannot delete an active course or a course with an active
    tutoring session.
28. An assigned supervisor can see that a tutoring session is active but cannot
    read its messages until completion.
29. A tutoring session remains active after extended inactivity and continues to
    block creation of a second session.
30. A student resumes the same active session from another authenticated device
    without creating a second session.
31. A supervisor or administrator cannot finish a student's active tutoring
    session.
32. A provider failure after partial output preserves the failed response and
    allows an explicit linked retry without rewriting history.
