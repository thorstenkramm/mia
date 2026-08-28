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

## Roles and capabilities

### Administrator capabilities

An administrator can:

- create and manage course records;
- assign supervisors to courses;
- invite additional administrators and remove another administrator's role;
- invite supervisors to MIA;
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
- provision student accounts, add students to the course, and invite mentors;
- assign and reassign mentors to students;
- inspect student-private uploads and generated material briefs;
- review completed tutoring sessions and full chat histories;
- view student profiles and edit non-security profile fields for any student who
  shares an assigned course;
- set a temporary student password for account recovery;
- ban or unban a student.

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
- upload an avatar;
- request a change to their mobile number and confirm the new number by SMS;
- view joined courses;
- download approved material from joined courses;
- conduct tutoring sessions;
- upload private material for use during tutoring;
- delete their own private material;
- view their own strengths, weaknesses, and follow-up summaries;
- request mentoring when the course and student settings allow it.

A student cannot change any other profile field. In particular, students cannot
change their email address, name, preferred time zone, roles, instructions, or
account state.

### Supervisor-managed student profiles

Any supervisor who shares an assigned course with a student can edit that
student's non-security profile fields:

- name and nickname;
- year of birth;
- email address and mobile number;
- preferred IANA time zone;
- avatar;
- language and country;
- student-specific AI tutor instructions;
- text-to-speech voice;
- whether the student may request mentoring.

This authority affects the student's global profile, not only the shared course.
Every change is audited. It does not grant permission to change roles, MFA,
password state, ban state, or course and mentor assignments. Password resets,
bans, and assignments remain separate actions with their own authorization
rules.

### Student mobile-number verification

- A requested mobile-number change does not update the account immediately,
  whether initiated by the student or a supervisor.
- MIA sends a confirmation code by SMS to the new number.
- MIA updates the account only after the student submits the correct code.
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
- If SMS delivery is not configured or fails, self-service mobile-number changes
  are unavailable and the current number remains unchanged.
- Confirmation codes are stored only in private SQLite. They are never returned
  by the API or written to logs or audit content.

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
- The inviter supplies only the intended email address. The invited role and,
  for a mentor, course scope come from the authorized invitation action.
- A new administrator, supervisor, or mentor completes their own required profile
  fields during acceptance.
- Before acceptance, an unauthenticated invitation page shows only that it is a
  MIA invitation, the invited role, and the course name for a mentor invitation.
- The preview does not expose the inviter's profile, intended email address,
  user records, or other course data.
- Student email remains optional.
- Administrator, supervisor, and mentor invitations do not expire. They remain
  pending until accepted or explicitly revoked.
- Invitations are single-use. Successful acceptance permanently consumes the
  invitation and prevents further acceptance attempts.
- Any administrator can revoke a pending administrator or supervisor invitation.
- Any supervisor currently assigned to the course can revoke a pending mentor
  invitation for that course.
- Revocation takes effect immediately, prevents acceptance, and is audited.
- Resending a pending invitation invalidates its previous token, creates a new
  token, and sends it to the same intended email address.
- Token replacement is audited. Every earlier link becomes unusable immediately.
- If the intended person already has a MIA account, they authenticate that
  account before accepting the invitation.
- Acceptance adds the invited role and course scope to the existing account and
  does not create a duplicate.
- Accepting a mentor invitation makes the account eligible as a mentor in the
  course but does not assign the mentor to a student. Assignment remains a
  separate supervisor action.

### Account and administrator lifecycle

- Only an administrator can permanently delete a student account. A supervisor
  can remove course membership but cannot delete the account globally.
- A supervisor, mentor, or administrator account can be deleted only by a
  different administrator. An account with any staff role follows this rule even
  if it also has the student role.
- Course responsibilities, mentor assignments, and other protected relationships
  must satisfy their removal rules before staff account deletion.
- Only a different administrator can remove an administrator role. Self-removal
  is forbidden.
- MIA never removes or deletes the last administrator. The check and role removal
  or account deletion occur in one transaction.

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

### Student password recovery

- Any supervisor sharing an assigned course with the student can initiate
  password recovery.
- The supervisor chooses a new temporary password that satisfies the password
  policy.
- Setting the temporary password immediately restricts every existing browser
  session to password replacement and logout.
- The student must replace the temporary password at the next login before using
  any other authenticated feature.
- The temporary password is never logged, included in audit content, or exposed
  after the supervisor submits it.
- The reset and subsequent password replacement are audited without recording
  either password.

### Staff and administrator password recovery

- Supervisors, mentors, and administrators recover forgotten passwords through
  their verified email address.
- MIA sends a single-use reset link that expires 30 minutes after issuance.
- Public recovery responses do not reveal whether the submitted account or email
  address exists.
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
- MIA does not silently trim or alter a submitted password.
- The policy applies to permanent and supervisor-chosen temporary passwords.
- Operators cannot configure weaker password requirements.

### Authentication session lifetime

- The MVP uses a signed and encrypted stateless browser cookie. MIA keeps no
  server-side session records.
- An authenticated browser session expires after 30 minutes without an
  authenticated user action.
- Each successful authenticated HTTP request, including establishment of an SSE
  connection, resets the inactivity timer. Server-sent heartbeats and background
  provider work do not.
- A session expires no later than 12 hours after authentication, regardless of
  activity.
- After either timeout, the user must authenticate again.
- The 12-hour maximum is not extended by session activity.
- Logout clears the cookie in the current browser. MIA does not provide session
  listing or individual remote-session revocation in the MVP.

### Concurrent sessions

- One account may have authenticated browser sessions on multiple devices at the
  same time.
- Each session has its own inactivity and maximum-lifetime timers.
- Account ban and deletion take effect on the next request because MIA reloads
  current account state instead of trusting it from the cookie.
- The one-active-tutoring-session rule applies across all of a student's browser
  sessions and devices.

### Login stages

- Login always verifies the password first.
- When the account has active MFA, successful password verification creates a
  restricted `mfa` cookie stage. That stage permits only completion of the bound
  MFA challenge and logout.
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
- MIA supports SMS one-time passwords when ClickSend is configured and the user
  has a verified mobile number.
- SMS MFA and mobile-verification codes are uniformly generated six-digit decimal
  strings from `000000` through `999999`; leading zeros are significant.
- An SMS MFA code expires 30 minutes after issuance and is single-use.
- SMS MFA resends use the same 60-second cooldown, five-per-hour limit, and
  ten-per-day limit, applied to both account and destination number.
- Five incorrect SMS MFA submissions invalidate the code.
- SMS MFA is unavailable when either prerequisite is missing.
- MFA enrollment is not active until the user verifies the selected factor.
- A pending enrollment expires after 30 minutes. Expiry removes its pending
  secret or SMS code without changing an existing active factor.
- Five incorrect verification submissions delete the pending enrollment.
- An account has at most one active MFA method: TOTP or SMS.
- Replacing the method requires verification of the new factor before it becomes
  active. The existing factor remains active until replacement succeeds.
- Disabling or replacing active MFA requires the current password and a fresh
  verification using the current factor or one recovery code. Lost-factor cases
  use the authorized reset workflows instead.
- An SMS factor is bound to the verified mobile number captured at enrollment.
  Profile changes do not alter that destination.
- Five incorrect TOTP or SMS submissions invalidate the current MFA challenge.

### MFA recovery codes

- Enabling MFA issues ten one-time recovery codes. Each code contains 16 random
  characters from the unambiguous Crockford Base32 alphabet and is displayed as
  four groups of four characters.
- MIA displays the plaintext codes only once and stores only non-reversible
  SHA-256 digests. Verification ignores display hyphens and ASCII letter case.
- Each code can satisfy one MFA challenge and is permanently consumed after
  successful use.
- Generating a replacement set invalidates every unused code from the previous
  set.
- Recovery codes, their hashes, and submitted values are never logged or written
  to audit content.

### Student lost-factor recovery

- If a student loses the active factor and all recovery codes, any supervisor
  sharing an assigned course with the student can perform a separate MFA reset.
- The reset removes the active MFA method and invalidates all recovery codes.
- MIA requires password replacement. Existing browser cookies are immediately
  restricted to password replacement and logout.
- MFA reset and subsequent password replacement are audited without recording
  secrets.

### Staff and administrator lost-factor recovery

- A supervisor, mentor, or administrator who has lost the active factor and all
  recovery codes requires an MFA reset by a different administrator.
- A user cannot approve their own MFA reset.
- An account with any administrator, supervisor, or mentor role always uses the
  staff reset path, including an account that also has the student role.
- The reset removes the active MFA method, invalidates all recovery codes,
  and requires password replacement at next login. Existing browser cookies are
  restricted to password replacement and logout.
- Every action is audited without recording secrets.
- If exactly one administrator account exists and it has lost both MFA and all
  recovery codes, the operator can perform a local server-only recovery action.
- Local recovery resets MFA, invalidates recovery codes, restricts existing
  browser cookies, and requires password replacement at next login.
- Local recovery is not exposed through a public web route.

### First administrator

- The operator creates the first administrator through a one-time local
  bootstrap action.
- Bootstrap is available only while no administrator exists and is never exposed
  as a public web registration flow.
- After the first administrator is created, bootstrap is disabled and further
  administrators follow the authorized account-management workflow.

## Course lifecycle

A course has one of two states: inactive or active. MIA has no separate archived
state. A newly created course is inactive. Course names are globally unique
case-insensitively.

### Creation

Only an administrator creates a course. Creation means adding the course record
to MIA and assigning one or more supervisors. Creation does not make the course
available to students.

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

## Material

### Material scopes

Every material has exactly one immutable scope:

- **Course-wide material** belongs to a course and may become available to every
  student in that course.
- **Student-private material** belongs to its uploading student within a course.
  It is available as tutoring material for that student but does not become
  course-wide material available to all students.

Material names are unique within a course using case-insensitive comparison.
Every file in one material uses the same supported file format.
Website and YouTube link-only material is course-wide and created by an assigned
supervisor. Student-private material requires at least one uploaded source file.

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
- Only approved course-wide material is visible to course students or available to
  the AI tutor during their sessions.
- Revoking approval removes the material from subsequent student access and AI
  tutor retrieval.

### Student-private material

- A student can upload material such as homework, worksheets, or exams.
- Student-private material requires no supervisor approval.
- It is available to its owner, the AI tutor, and supervisors assigned to the
  course.
- Assigned supervisors can inspect the source files, generated material brief,
  and completed session's full chat history.
- Mentors, other students, unrelated supervisors, and administrators who are not
  assigned as supervisors have no access.
- Student-private material remains usable by its uploading student but cannot be
  converted into course-wide material for all students.

### Deleting student-private material

- A student can delete private material they uploaded.
- Deletion removes the source files and generated material brief.
- Deleted material is unavailable for future viewing, search, retrieval, and
  tutoring sessions.
- Existing chat messages and completed-session summaries are preserved, even if
  they discuss or quote the deleted material.
- Quoted or derived chat content may therefore remain without a retrievable
  source. This orphaned historical content is accepted behavior.
- MIA preserves a non-content audit record that the deletion occurred. The audit
  record must not contain the deleted source, generated brief, or extracted
  content.
- Deletion behavior for provider-held data and backups remains subject to the
  data-lifecycle policy.

### Material processing

File-backed material starts in a draft state. The uploader may add or remove
files while it is a draft and then explicitly finalize the material. Finalization
atomically freezes the file set, changes the material to processing, and queues
the required extraction work once.

- PDF, PNG, and JPEG use Mistral OCR.
- DOCX, UTF-8 text, and Markdown are validated and extracted locally with bounded
  parsers. These formats are not sent to Mistral.
- Successful extraction of every file and generation of a trustworthy brief
  changes the material to ready.
- Any required extraction or brief failure changes the material to failed. MIA
  never uses a partially processed material.
- A failed material may return to draft when its uploader removes or replaces a
  source file. Processing and ready materials have immutable file sets.
- A ready material cannot be reopened in the MVP. Correcting its source requires
  deleting and recreating the material.

Course-wide website and YouTube metadata may be finalized without a source file
only when an assigned supervisor provides a non-empty brief. An approved
link-only material is visible to students, but it does not satisfy course
activation or new-session readiness and provides no retrievable source content
to the AI tutor.

After readable content is available, MIA generates one grounded material brief.
The brief contains:

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

Regeneration must not silently overwrite supervisor edits. Student-private
briefs inherit the source material's access rules and require no review or
approval.

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
- A YouTube material link must use a recognized YouTube HTTPS host.

## Tutoring sessions

### Starting a session

- A student starts a session in one joined, active course.
- A student can have only one active tutoring session across all courses.
- The student may optionally select one or more primary materials.
- The student may instead start with only an intent, such as "Let's learn
  vocabulary."
- Any student-selected course-wide material must be approved.
- Any student-selected private material must belong to the active student and
  course.
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

### Initial context

At session start, the AI tutor receives:

1. Global AI tutor instructions.
2. The course brief and course-specific instructions.
3. The student brief and student-specific instructions.
4. The identity, material brief, and content outline of any material selected by
   the student.
5. The previous session's summary and follow-up, when available.

MIA does not send the complete content of every available material at session
start. It may include the complete extracted content of a small selected
material only when it fits within MIA's fixed input limit.

### Material retrieval

- The AI tutor can search student-selected material and other material available
  to the active student.
- Once the session intent is clear, the AI tutor can choose suitable working
  material from authorized search results.
- The AI tutor can request bounded excerpts from a material, chapter, or
  section.
- The authorized set consists only of approved material in the active course and
  student-private material owned by the active student.
- MIA authorizes every retrieval request. A model tool request never grants
  access by itself.
- MIA records every material actually used during the session.
- Retrieval results identify the material, chapter, and section when available.
- The AI tutor should cite the material, chapter, or section when directing a
  student to another source. It must not claim an OCR page position is the
  printed page number.

### Response delivery

- AI tutor responses appear incrementally while they are generated.
- The product must not wait for the complete model response before displaying
  available text to the student.
- MIA delivers incremental tutor-response events to the browser using
  Server-Sent Events as defined by the current API design.
- A client disconnect does not cancel response generation.
- MIA preserves output received from the provider while the student is
  disconnected.
- After reconnecting, the student receives the preserved output and continues
  the same response stream without starting a duplicate model response.
- If generation finishes while disconnected, the completed response is
  available when the student resumes the session.
- The student can stop a response while it is being generated without ending
  the tutoring session.
- MIA cancels the provider operation, preserves received text, and marks the
  response as interrupted rather than complete.
- MIA does not automatically retry an intentionally stopped response. The
  student can continue with another message.
- Each student-message submission includes a client-generated request
  identifier scoped to the tutoring session.
- Retrying the same identifier and content returns the existing message and
  response operation without creating another provider request.
- Reusing an identifier with different content is rejected as a conflict.
- If the provider fails after returning partial output, MIA preserves the text
  and marks the response as failed and incomplete.
- MIA shows the failure to the student and does not retry automatically.
- The student can explicitly retry. A retry creates a new response linked to the
  same student message and failed response without deleting or rewriting either.

### Completion and oversight

- While a session is active, assigned supervisors can see its status but cannot
  read its chat messages.
- Completing a session queues creation of a session summary and follow-up.
- The result outlines strengths, weaknesses, and suggested next steps.
- The student can view their progress summary.
- An assigned supervisor can correct a generated session summary and follow-up.
  Every correction is audited.
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
  any student-facing mentoring feature to be available.
- When no mentor is assigned, MIA disables mentoring suggestions, requests,
  responses, and scheduling. Supervisor assignment management remains available.
- `mentoring_requests_allowed` independently controls whether the student can
  create a new request. When it is false, MIA does not offer or suggest creating
  a new request. It does not remove mentor assignments or cancel existing
  requests or scheduled sessions.
- The mentor receives the mentoring topic, not private uploads or tutoring chat
  history.
- A student may include an appointment time when creating a mentoring request.
  Otherwise, the assigned mentor can add it later. The mentor can respond to the
  request, and either participant can reschedule an open mentoring session.
- Mentoring sessions take place outside MIA. MIA provides no live mentor chat,
  audio, or video channel.
- The mentor supplies meeting instructions or an external HTTPS meeting link.
- MIA stores and communicates the schedule and meeting instructions but does not
  fetch or validate the external service's content.
- The student or assigned mentor can cancel a future mentoring session.
- Cancellation records the actor and time. Participants and assigned supervisors
  see the updated state when viewing the mentoring session.
- The student or assigned mentor can reschedule a future mentoring session
  directly without acceptance by the other participant.
- Rescheduling records the actor, previous time, and new time and immediately
  updates the state visible to the other participant and assigned supervisors.
- MIA does not track attendance or no-shows for external mentoring sessions and
  applies no no-show status or penalty.

## Product notifications

- MIA sends no ordinary workflow notifications in the initial release.
- Users see current course, material, tutoring, mentoring, and job state when
  they use MIA.
- MIA provides no notification subscriptions, quiet hours, or workflow SMS.
- Invitation email, password-recovery email, MFA codes, and mobile-verification
  codes are required account or security delivery and are not product
  notifications.

### Mentor unassignment and reassignment

- A supervisor cannot unassign the student's last mentor.
- When several mentors are assigned, unassigning one requires the supervisor to
  select a replacement mentor.
- The replacement must be assigned to the same student and course as part of or
  before the unassignment.
- All open mentoring sessions assigned to the removed mentor transfer atomically
  to the selected replacement.
- Completed mentoring history remains attributed to the mentor who handled it.

## Time and scheduling

- Every API field representing an instant uses RFC 3339 in UTC with the `Z`
  suffix in requests and responses.
- Clients convert local input to UTC before sending it. MIA rejects instant
  values with a non-zero numeric offset.
- Every user has a preferred IANA time zone. A student cannot change it.
- Any supervisor who shares an assigned course with a student can set that
  student's preferred time zone under the supervisor-managed profile policy.
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
- MIA does not enforce monetary budgets or provider-account spending limits.
- MIA exposes available operational usage and provider quota failures without
  claiming that usage data is an authoritative billing total.
- The operator configures provider quotas, plans, billing controls, and alerts.
  MIA's own upload and rate limits continue to apply.

The default OpenAI model for tutoring and background jobs is
`gpt-5.6-terra`. A configured model must support the Responses API and the tools
required by its workload.

### Generated speech retention

- The server configuration defines generated-speech retention in days.
- The default retention is 30 days from generation.
- Accessing generated speech does not extend its retention.
- MIA deletes expired generated speech. It may generate the audio again on
  request when text-to-speech is available.
- Cached speech is reused only while the source chat message and requested voice
  match. A changed source or voice invalidates the cache.

## Data and deployment

- MIA is a self-hosted Linux server used through a browser.
- The backend/API server is distributed as one dependency-free binary.
- The frontend is maintained and distributed separately.
- The backend serves the installed frontend from a configured document root.
- SQLite is the database.
- Uploaded and generated files are stored in the data directory.
- The database and data directory form one consistent data set and must be
  backed up and restored together.
- The configured base data directory must exist. MIA creates missing required
  subdirectories at startup.
- HTTPS through a reverse proxy is mandatory.

## Data lifecycle

### Retention

- MIA retains operational data until an authorized deletion occurs.
- MIA does not automatically expire profiles, course data, material, chats,
  session summaries, mentoring records, or audit events.
- Generated speech is the explicit exception and follows its configured
  retention period.
- MIA provides no user data-export feature.

### Student deletion

- Deleting a student deletes the user account and all operational data belonging
  to that student across all courses.
- If the account has additional roles, deletion still removes the entire account
  and every role.
- This includes profile data, memberships, student-private material and briefs,
  tutoring sessions, chat history, session summaries and follow-ups, mentoring
  records, generated speech, and pending invitations or challenges.
- MIA preserves only a minimal, content-free audit record of the deletion.
- The retained audit record contains the event type, time, acting user, and an
  irreversible or de-identified subject reference. It contains no profile data,
  message content, material content, or direct subject identifier.

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

### External copies and backups

- MIA deletion applies only to its live SQLite database and data directory.
- MIA does not request or guarantee deletion from external providers or
  operator-managed backups.
- The operator is responsible for provider-side data and backup retention and
  for preventing restoration of data that should remain deleted.
- MIA must document this limitation and the data sent to each provider.

### Corrections

- Students correct only the self-service profile fields defined in this
  document.
- Assigned supervisors can correct non-security student profile fields,
  generated material briefs, tutoring-session summaries, and follow-ups.
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
  verification and resend, and mobile-number verification and resend require
  dedicated limits.
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
  code, and `Retry-After` when a retry time is known.
- Rate-limit behavior must not reveal whether a username, email address, mobile
  number, invitation, or other sensitive identifier exists.
- Client-IP limits use only addresses derived from explicitly trusted proxy
  configuration.
- Limiter state is bounded so arbitrary identifiers cannot exhaust memory or
  persistent storage.
- Process-level limiter state holds at most 50,000 keys. MIA removes expired keys
  first and then evicts least-recently-used keys when necessary. Entries expire
  after their final applicable window.
- Limits are fixed by MIA and are not operator-configurable. Error responses
  must not disclose internal thresholds when doing so would weaken abuse
  controls.
- Security-relevant throttling is audited without recording passwords, MFA or
  SMS codes, tokens, or complete request bodies.
- Authenticated operations that consume substantial local or provider resources
  have separate user- and course-scoped limits. This includes tutoring messages,
  uploads, OCR, AI jobs, and text-to-speech.

## Acceptance scenarios

The completed product must support at least these observable scenarios:

1. An administrator creates a course, assigns supervisors, and cannot access
   student-private data without a supervisor assignment.
2. A supervisor prepares and activates a course after its required content and
   approved material are present.
3. Revoking course-material approval prevents subsequent student access and AI
   tutor retrieval.
4. A student uploads private material, uses it in tutoring without an approval
   step, and later deletes its source and brief without deleting the completed
   chat.
5. An assigned supervisor can inspect that private material and the completed
   session history, while a mentor and unrelated users cannot.
6. A student starts with only "Let's learn vocabulary." The AI tutor discovers
   suitable authorized material, requests a bounded excerpt, and cites its
   location.
7. MIA rejects a model request for unapproved, cross-course, or another
   student's private material.
8. A student cannot start a second session while another session is active.
9. Two users in different time zones see the same appointment instant converted
   to their own preferred time zones.
10. A provider or processing failure remains visible and does not create a
    successful-looking summary or material state.
11. MIA exposes no student-facing mentoring feature when the student has no
    assigned mentor in the active course.
12. Disabling `mentoring_requests_allowed` blocks new requests without cancelling
    existing requests or scheduled sessions.
13. A supervisor cannot remove the last assigned mentor and must select a
    replacement when removing one of several mentors.
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
