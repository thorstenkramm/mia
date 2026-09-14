# API Design

Audience: frontend and backend developers. This document explains product
rationale, authorization boundaries, and workflows that span API routes. The
[OpenAPI 3.2 contract](../api-doc/openapi.yaml) is the source of truth for
implemented paths, methods, media types, schemas, status codes, stable errors,
security requirements, and request bounds. The
[product requirements](product-requirements.md) remain authoritative for product
behavior, authorization, and security boundaries.

## Contents

- [Conventions](#conventions)
- [Frontend and local commands](#frontend-and-local-commands)
- [Authentication and recovery](#authentication-and-recovery)
- [Invitations](#invitations)
- [Current user and MFA](#current-user-and-mfa)
- [User administration](#user-administration)
- [Courses and relationships](#courses-and-relationships)
- [Materials and files](#materials-and-files)
- [Tutoring sessions and messages](#tutoring-sessions-and-messages)
- [Generated speech](#generated-speech)
- [Mentoring](#mentoring)
- [Jobs and audit](#jobs-and-audit)
- [Open decisions](#open-decisions)

## Conventions

### Transport contract

Implemented transport details are defined only in
[`api-doc/openapi.yaml`](../api-doc/openapi.yaml). JSON resources use JSON:API;
raw images, multipart uploads, downloads, normalized-content streams, generated
audio, and tutor-response event streams use their operation-specific media
types. Unknown routes and unsupported methods reach ordinary API `404` or `405`
handling before JSON:API `Accept` negotiation.

Every public or authenticated API response carries `Cache-Control: no-store`,
including JSON, errors, bodyless success, images, downloads, generated audio,
normalized-content streams, and SSE. Authenticated images have no validator or
`304` caching exception. API error bodies use the JSON:API media type, contain
only stable safe registry fields, and are emitted as complete valid UTF-8 JSON no
larger than 65,536 bytes. Oversized definitions are replaced with generic safe
text while preserving the mapped status and stable code; responses are never
byte-truncated.

### Identifiers and time

- Resource IDs are opaque and clients never derive authorization from them.
- Every instant uses RFC 3339 UTC with the `Z` suffix.
- Clients convert display-local values to UTC before sending an instant.
- One-time mentoring appointments use `scheduled_for` as a UTC instant.

### Optional text

- After field-specific trimming, empty or whitespace-only optional text is
  represented as JSON `null` and persisted as SQL null.
- Clients clear a writable optional text field with JSON `null` or an empty value.
- Required text fields reject empty or whitespace-only values.
- Except for passwords and chat messages, multiline profile and descriptive text
  is valid UTF-8. MIA normalizes CRLF and CR to LF, trims surrounding Unicode
  whitespace, and rejects NUL and controls other than tab and newline.

### Pagination and filtering

Growing collections use bounded deterministic pagination without calculating an
exact total by default. Each implemented operation's allowed parameters, bounds,
and response links are defined in OpenAPI. Bounded unpaginated collections reject
all query parameters rather than silently accepting pagination or filters.

### Included relationships

- No relationship is included by default.
- Each route explicitly allowlists includable direct relationships.
- A request may include at most three relationships. Nested include paths are not
  supported in the MVP.
- Unsupported, nested, duplicate, or excessive includes are validation errors;
  MIA does not silently ignore them.

### Errors and resource hiding

- Errors use the JSON:API shapes and stable codes defined in OpenAPI.
- Authentication-sensitive public responses do not reveal account existence.
- An out-of-scope sensitive resource returns the same public result as an
  unknown resource.
- Rate-limited responses use HTTP `429 Too Many Requests` and include
  `Retry-After` when known. Public password-recovery requests instead retain the
  same success response used for absent accounts and accepted delivery.
- Authentication-sensitive limit responses do not identify whether the IP,
  account, submitted identifier, token, or challenge caused the limit.
- Progressive login backoff returns immediately with `429`; handlers never sleep
  to enforce a retry delay.
- Unsupported sparse fieldsets, client-selected sorting, unlisted filters, and
  unknown query parameters return a stable validation error. MIA does not ignore
  them.
- Hidden-resource responses retain ordinary caller and IP limiting but create no
  limiter key derived from an unauthorized resource. Denied reads are not
  individually audited.
- Protocol failures rejected before domain handling do not count as login or
  domain attempts and do not create domain-denial audit events. Valid credential,
  authorization, and domain failures retain their documented audit behavior.

### Authorization

Every operation authorizes the action, role, course assignment, student
assignment, ownership, and resource state. Route grouping and possession of an
ID are never sufficient authorization.

### Browser session and CSRF

The MVP uses Gorilla `CookieStore` and custom shared CSRF middleware. For HTTPS,
the signed and encrypted `__Host-mia_session` cookie contains only the user ID,
login stage, stage-specific challenge ID when needed, security generation,
authentication time, idle expiry, absolute expiry, and a browser-generation
binding. The independently signed `__Host-mia_browser` cookie carries that
browser generation. Both are `Secure`, `HttpOnly`, `SameSite=Lax`, have path
`/`, and have no `Domain` attribute. Each authentication transition persists the
browser-generation cookie through the bound session's absolute lifetime, so a
browser restart does not invalidate an otherwise unexpired session. Ordinary
requests and Continue working do not reissue the browser-generation cookie.

Every authenticated request reloads current account, role, assignment, ban, and
password-gate state from SQLite. Authorization never trusts those values from the
cookie. A cookie whose security generation differs from the current user row is
cleared and rejected. Ordinary authenticated requests, automatic polling, SSE
establishment, reconnection, and server-sent events never reissue the cookie or
advance its idle expiry. The frontend deliberately calls
`POST /api/v1/auth/session-continuations` after the user chooses Continue working;
only that operation advances the idle expiry, capped by the original 12-hour
absolute expiry. Authenticated responses expose both authoritative deadlines in
`Mia-Session-Idle-Expires-At` and `Mia-Session-Absolute-Expires-At` headers.

MIA's shared CSRF middleware uses Fetch Metadata checks and double-submit token
validation for unsafe methods. The HTTPS `__Host-mia_csrf` cookie is `Secure`,
`SameSite=Lax`, host-only, uses path `/`, and is readable by the frontend rather
than `HttpOnly`. The frontend sends its value in `X-CSRF-Token` when token
validation is required. A loopback-HTTP `main.public_url` uses the non-Secure
`mia_session`, `mia_browser`, and `mia_csrf` names only with a loopback TCP
listener. MIA supports same-origin browser access only in the MVP and does not
enable CORS.

`GET /api/v1/auth/session` always issues or refreshes anonymous CSRF state. Every
unsafe public endpoint, including login, invitation preview and acceptance, and
recovery, requires the matching cookie and header. Cross-site Fetch Metadata is
rejected; missing Fetch Metadata is accepted only with a valid CSRF token. MIA
rotates CSRF state after completed login, logout, and every login-stage
transition, invalidating the old value immediately.

Session discovery returns the authoritative `anonymous`, `mfa`,
`password-change`, or `authenticated` stage. It returns no profile or role data;
the `mfa` stage exposes only its opaque challenge ID. Invalid, stale, banned, or
deleted session state is cleared and returned as anonymous. Logout also rotates
the current browser-generation cookie. Delayed ordinary authenticated and
Continue working responses do not issue a matching marker and therefore cannot
restore usable authentication after logout. A delayed login, MFA-completion, or
password-change response admitted before logout reissues a matching cookie pair
and may restore authentication. The frontend treats logout as incomplete while
such a transition is outstanding and reconciles a later response through session
discovery. Other browsers remain valid; MIA keeps no server-side browser-session
or per-browser revocation records and offers no remote session revocation.

## Frontend and local commands

MIA serves regular frontend files for GET and HEAD outside `/api`, rejects
symlink escapes, dotfiles, and directory listings, and uses `index.html` fallback
only for unresolved non-API GET or HEAD routes. Unknown API paths never fall back
to frontend HTML. Static responses use `X-Content-Type-Options: nosniff`, a
restrictive referrer policy, frame denial, and a strict baseline CSP
(`default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors
'none'`), loosened during frontend integration only when the frontend
demonstrably requires it.

The first-administrator bootstrap has no HTTP route. With the server stopped, the
operator runs `mia bootstrap-admin` locally in exactly one of two modes. The
prompt mode requires terminal input and output and prompts for username, email,
language, country, time zone, password, and password confirmation with masked
password feedback. The complete flag-file mode requires `--username`, `--email`,
`--language`, `--country`, `--time-zone`, and `--password-file`, and works from a
terminal or without terminal input/output. The password file must be a readable
regular one-line file with an optional final LF or CRLF; all other password bytes
are preserved. The command requires a configured absolute `main.data_dir` and
prints the username and fixed SQLite path only after its transaction commits.

The command validates the normal account and password rules, verifies that no
administrator exists inside the bootstrap transaction, creates the user and
administrator role, records the local-operator audit event, and commits. It makes
no database change if validation fails, an administrator already exists, or any
write fails. The supplied administrator email is marked verified because the
local operator creates the account directly.

Sole-administrator MFA recovery also has no HTTP route. With the server stopped,
the operator runs the interactive `mia reset-admin-mfa` command locally. The
command refuses unless exactly one administrator exists and that account has MFA
state to reset. It displays the identified administrator and requires the
operator to type its exact displayed username before making changes.

`mia serve` and both local commands acquire the exclusive
`main.data_dir/mia.lock` OS lock before opening SQLite. Local commands refuse
while the server or another database-using command holds it.

Inside one transaction, the command rechecks that exactly one administrator
exists, ends active and pending factors, invalidates recovery codes and MFA
challenges, requires password replacement, and records the local-operator audit
event. It makes no database change if confirmation is withheld, a precondition
fails, or any write fails. Current browser cookies become restricted to password
replacement and logout when MIA next checks account state.

## Authentication and recovery

- `POST /api/v1/auth/login`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/session`
- `POST /api/v1/auth/session-continuations`
- `POST /api/v1/auth/password-changes`
- `POST /api/v1/auth/password-recovery-requests`
- `POST /api/v1/auth/password-resets`
- `POST /api/v1/auth/mfa-challenges/{id}/verifications`
- `POST /api/v1/auth/mfa-challenges/{id}/resends`
- `POST /api/v1/auth/mfa-challenges/{id}/recovery-code-consumptions`
- `POST /api/v1/auth/mfa-management-proofs`

### Authentication transport

The concrete login, logout, password-change, recovery, reset, and MFA request and
response contracts are defined under the Authentication tag in OpenAPI. Responses
never expose credentials, password hashes, recovery tokens, roles, or profile
data. The `auth-sessions` resource is a singleton view whose ID is the
authenticated account ID, including during restricted login stages.

Password recovery intentionally returns the same empty response for unknown,
banned, student-only, invalid, accepted, and throttled usernames. Dedicated
recovery throttling therefore sends no `Retry-After` and cannot become an
account-existence oracle. Password reset never creates a browser session; an
invalid or unusable token has one uniform public result, while a password-policy
failure leaves an otherwise valid token usable.

### Password blocklist provenance

MIA embeds `Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt` from
`danielmiessler/SecLists` commit `f025490a4bc7bd1d6cd36c3b834631acd615ff28` (source timestamp
`2026-08-30T11:13:41Z`). The embedded snapshot is upstream MIT-licensed. It is compared against valid submitted
password bytes exactly; MIA does not trim, normalize, case-fold, or generate password mutations.

Login and password recovery accept a complete username only. Login may return an
MFA challenge instead of a complete authenticated session. Public recovery
responses are account-enumeration safe, including for banned accounts. Banned
accounts cannot request or complete recovery. A new request does not invalidate
earlier links; successful reset invalidates every remaining challenge. Successful
reset does not revoke other stateless browser cookies in the MVP. A reset link
expires after 30 minutes, is single-use, and uses
`/password-reset#token=<uuid>`. The frontend removes the fragment before its API
request.

Login cookie stages are:

- `mfa`, which permits only verification or recovery-code consumption for the
  matching challenge, resend when that challenge uses SMS, and logout;
- `password-change`, which permits only password replacement and logout;
- `authenticated`, which permits normal authenticated routes subject to
  authorization.

Restricted stages expire after 30 minutes and do not refresh. Password
verification creates `mfa` before `password-change` when both are required. MIA
rotates the cookie after MFA, password replacement, and completed login. Login
MFA verification, recovery-code consumption, and SMS resend require both the
matching challenge ID and the bound `mfa` cookie stage; a challenge ID alone
grants no authority. Sensitive-action verification instead requires a fully
authenticated cookie for the same user.

MFA enrollment expires after 30 minutes, and five incorrect submissions delete
the pending enrollment. A pending SMS enrollment resend sends the existing code
without extending expiry or resetting failures. TOTP provisioning uses issuer
`MIA (<main.public_url hostname>)` and the username as account label. Successful
activation returns ten recovery codes once. Disabling or replacing active MFA
requires the current password and a fresh current-factor or recovery-code proof.
POST enrollment creates a pending replacement when an active factor exists.
Replacement activation atomically consumes the password and MFA-management proof.
DELETE cancels a pending enrollment or, with the same proof requirements,
disables an active factor. If an activation response containing recovery codes is
lost, the codes cannot be retrieved; complete factor replacement is the recovery
path. An active SMS factor retains its enrolled destination when the profile
mobile changes. Successful profile-mobile change or removal deletes pending SMS
factors.
Login MFA challenges also expire after 30 non-refreshing minutes. Separate login
attempts may have concurrent challenges, and completion consumes only the one
used. A TOTP time step can succeed only once per factor. Login SMS challenge
resend has the same preservation behavior.

Successful current-factor confirmation returns one opaque MFA-management proof.
It is valid for five minutes and one MFA disable or replacement. The mutation
consumes it atomically. Password, MFA, ban-state, or account-state changes
invalidate all outstanding MFA challenges and management proofs for the user.

### MFA transport

OpenAPI defines the concrete enrollment, verification, resend, recovery-code,
and management-proof resources. Successful activation shows exactly ten
single-use recovery codes once; they cannot be retrieved again. A management
proof is an opaque five-minute bearer value for one factor disable or replacement
and is distinct from its non-secret JSON:API resource ID.

### Mobile-verification lifecycle

`GET /api/v1/users/me/mobile-change-challenges` is the non-refreshing, current-account reconciliation read. It returns
one per-account state resource whose lifecycle is `active`, `expired`, `invalidated`, `completed`, `absent`, or
`unavailable`. The resource exposes only the opaque challenge ID, original server-authored expiry, resend eligibility,
and the next resend instant when known. It never returns the destination, code, failed submissions, limiter dimensions,
or delivery counts.

Challenge creation, successful verification, and successful resend return the same representation as an immediate read.
Resend keeps the original code, expiry, and accumulated verification-failure count. A 60-second cooldown uses the stable
`user_mobile_resend_cooldown` error; hourly or daily exhaustion uses the generic `rate_limited` error. Both include
`Retry-After`, make no provider request, and disclose neither whether the account or destination dimension caused the
denial nor the configured thresholds. A provider failure is not retried automatically; the client reconciles through the
GET operation.

## Invitations

OpenAPI defines the authenticated invitation-management and public preview and
acceptance operations. Public operations submit invitation tokens in request
bodies so tokens do not appear in access-log paths.

Administrator, supervisor, and mentor invitations are single-use, do not expire,
and normally remain pending until accepted or revoked. Definite initial SMTP
failure makes one faulty and unusable; an SMTP timeout leaves it pending and
usable, logs a sanitized error, and is not retried. Authorized reads expose the
lifecycle state, latest delivery state, sanitized delivery code, and authoritative
UTC attempt time. `queued` means delivery was admitted but has not produced a
recorded result; `delivered` is confirmed SMTP acceptance; `ambiguous` with
`invitation_delivery_timeout` or `invitation_delivery_ambiguous` does not claim
delivery; and `failed` with `invitation_delivery_rejected` accompanies the
terminal faulty state. Faulty invitations can
only be deleted. To
token holders, preview and acceptance treat revoked, faulty, accepted, and
unknown tokens identically with one generic invalid-invitation response. Links
use `/invitation#token=<uuid>` and the frontend removes the fragment before its
API request. Student accounts do not use invitations. Invitations contain no
course or student scope. Acceptance always creates a new account and its invited
permanent role. Creating or accepting an invitation whose email is already used
by a registered account is rejected; role changes use the user-ID operation
below. Any supervisor may create a mentor invitation; only its inviting
supervisor or an administrator may view, resend, revoke, or delete it.
Supervisor invitation acceptance atomically creates both supervisor and student
roles.

`GET /invitations/{id}` returns a strong resource-specific `ETag`. The
state-dependent `DELETE /invitations/{id}` requires that exact value in
`If-Match`. A missing precondition returns `428 invitation_precondition_required`;
a malformed, wrong-resource, or stale value returns
`412 invitation_precondition_failed`. Both leave the invitation unchanged and
require a fresh read and confirmation.

For a pending invitation DELETE
atomically revokes the invitation, clears its token, records revocation
attribution, and writes the revocation audit event. For a faulty invitation it
physically deletes the row and writes a content-free deletion audit event.
Accepted and already revoked invitations reject DELETE without changing state.
The same actor authorization governs pending revocation and faulty deletion.
Success returns an `invitation-deletions` command result whose `effect` is
`revoked` or `deleted`, so a confirmed revoke can never silently become physical
deletion and a confirmed faulty deletion can never become revocation.

## Current user and MFA

OpenAPI defines current-profile, MFA, mobile, and avatar transport details. For
the singleton `/users/me` alias, PATCH identity is the authenticated account ID,
not the literal string `me`.

`GET /users/me/capabilities` is the authoritative, non-refreshing navigation
projection for the current account. It returns the complete release-one action
catalog on every check. Each action has an explicit availability flag and
global, course-ID, student-ID, paired course-and-student, and own-resource scope fields; unavailable and
empty scopes are represented rather than omitted. The projection is recomputed
from current roles, assignments, memberships, and account state. It guides shell
and entry-scope visibility but never authorizes a later operation.

Field-level authorization still applies to `PATCH /users/me`. Student-only
accounts cannot mutate their profiles. Administrators, supervisors, and mentors
may change their own name, nickname, language, country, time zone, avatar, and TTS
voice. Mobile-change challenge routes manage their verified mobile number.
Username, email, roles, ban state, password state, and security fields remain
outside generic profile PATCH.

`PATCH /users/{id}` updates documented supervisor-managed fields of a student-only
account. Only a supervisor assigned to a course shared with that student may set,
change, or clear the student's ElevenLabs TTS voice. Students cannot use this
operation or select a voice through `/users/me`.

DELETE on the current user's mobile clears the verified profile number and
pending mobile-verification challenges and pending SMS factors without SMS
delivery. It leaves an active SMS MFA factor's enrolled destination unchanged and
writes a content-free audit event. New SMS enrollment requires a verified profile
mobile; login challenges use the active factor's destination snapshot. MIA
exposes no standalone recovery-code regeneration route; replacing the MFA factor
invalidates its old codes and returns the new factor's codes once.

Avatar upload accepts bounded JPEG or PNG rather than JSON:API. The source is at
most 10 MiB, 40 decoded megapixels, and 10,000 pixels per dimension. MIA applies
orientation, strips metadata, fits the image within 512 by 512 pixels without
changing its aspect ratio, and stores PNG.

Avatar download is represented by an authorized URL in the user resource; it
never exposes an internal filesystem path. Download authorization is identical
to profile-view authorization for that user and responses use `image/png`, safe
content headers, and `Cache-Control: no-store` without validators or `304` reuse.

Current-profile responses deliberately omit the mobile number. Mobile challenge
responses never return the destination or verification code. Raw avatar upload
uses JPEG or PNG rather than multipart form data; the normalized stored and
downloaded representation remains PNG.

## User administration

The implemented operations are listed in OpenAPI. `GET /users` gives
administrators a bounded global account collection ordered by normalized
username and opaque ID. It supports only exact normalized username, account
class, permanent role, and account-state filters. `GET /users/{id}` returns the
minimal reviewed target: ID, username, class, state, roles, server-authored
eligibility, and consequence identifiers. Neither read returns email, other
profile fields, security state, or course, tutoring, and mentoring content.

Account detail carries a strong `ETag` covering target state, actor authority,
role-grant prerequisites, administrator count, and sole-supervisor protection.
Administrator grants of the administrator or supervisor role and account
deletion require that exact value in `If-Match`. Missing and stale validators
return `428` and `412` respectively without mutation. The transaction rechecks
authorization and all effect-driving state before role coupling, cleanup, and
audit commit.

These user-administration operations do not create a generic administrator override. Each operation
enforces its role, course, student, and protected-field rules. Student
provisioning and course membership use the course routes below.

`GET /api/v1/users/me/mfa` is the authoritative reload-time MFA overview. It
returns the active factor ID and method, a live pending enrollment ID, method,
expiry and replacement relation, plus separate server-authored eligibility for
enroll, continue, cancel, disable, replace, verify, and SMS resend. It never
returns a destination, TOTP provisioning data, codes, recovery codes, proofs, or
stored secret material. Expired pending enrollment is represented as absent.

`POST /api/v1/users/{id}/mfa-resets` performs lost-factor recovery. A supervisor
must share an assigned course with a student-only target; a different
administrator is required for every staff target. Missing, out-of-scope,
ineligible, and self-targeted accounts use one hidden-target response. An
authorized target with no active factor returns the documented stale-state
conflict. Success returns only the committed account class, password gate, MFA
state, and invalidated-session effect. Every successful reset increments security
generation, invalidates all existing target cookies, and requires a fresh login
followed by password replacement. A layered source-IP and actor-account limiter
rejects excess attempts without evaluating the target. The request is never
replayed automatically after an ambiguous transport result; the target account's
MFA overview after sign-in is the authoritative reconciliation read.
Sole-administrator recovery remains available only through `reset-admin-mfa` and
has no HTTP route.

Every user can retrieve self. Assigned supervisors can list students and staff
relationships in assigned courses. Mentors receive only minimal identity —
username, name, nickname, and avatar — for assigned students. The student role alone grants no roster. Administrators can
list account and relationship metadata needed for administration, not private
course content.

Creating an invitation with role `administrator` requires an administrator.
Direct role assignment identifies a registered staff account by user ID, applies
immediately, and requires no affected-user approval. The target must already have
a staff role and verified email; student-only accounts cannot be promoted. New
staff accounts use invitations. An administrator may grant the administrator or
supervisor role; any supervisor may grant the mentor role. Granting supervisor
atomically grants student when absent. Banned targets are rejected until
explicitly unbanned. Repeated assignment is idempotent. Administrator, supervisor,
and mentor roles cannot be removed.
`DELETE /users/{id}` requires an administrator for a student-only account and a
different administrator for any account with a staff role. Staff deletion rejects
the last administrator and a sole supervisor of any course. Otherwise it removes
current assignments, triages affected open mentoring work, deletes the complete
account and student-owned data, and clears retained historical actor references
in one transaction.

An assigned supervisor may manage only a student-only account through
`PATCH /users/{id}`, including setting or clearing its immediately verified mobile
number. The transaction invalidates pending mobile challenges and leaves an
active SMS factor at its enrolled destination. Ban routes apply only to
student-only accounts. A ban rejects the next request but does not cancel work
already in flight. Student-only and staff account deletion do not coordinate with
running provider calls. Late writes update existing rows only; a zero-row guarded
update discards the result, removes newly published output, closes synchronized
response subscribers, and never recreates or requeues deleted state.
Setting a student-only account's temporary password or resetting its MFA
increments the user's security generation in the same transaction. This
invalidates every existing cookie for the student. Staff accounts always use the
staff recovery paths. A fresh student login enters the `password-change` stage
after password verification and any required MFA stage.

## Courses and relationships

Implemented course operations and their schemas are listed in OpenAPI. Mentor
assignment operations remain planned behavior until their route family is
implemented and added to that contract.

`POST /courses` requires one or more initial supervisor relationships. Every
referenced user must already hold the supervisor role. MIA validates the complete
request and creates the inactive course and all initial assignments in one
transaction; any invalid relationship or failed write rolls back the operation.
The supervisors collection adds only later assignments. Listing course
supervisors uses the standard `page[limit]` and `page[offset]` pagination.

Creation carries initial supervisors and validates the complete relationship set
atomically. Assigned supervisors can edit descriptive course information. Names
are trimmed, NFC-normalized,
limited to 200 Unicode code points and 800 bytes, and globally unique by the
shared folded key. Only administrators may rename a course; assigned supervisors
manage its descriptive and tutoring fields. Description is limited to 4,000 code points and 16 KiB;
curriculum, learning goals, and AI tutor instructions are each limited to 16,000
code points and 64 KiB. Non-null language values are canonical BCP 47 tags.
`logo_url` is null unless the fixed course-logo path currently contains a logo.

Course detail is the authoritative readiness reconciliation read. It derives one of
`inactive_incomplete`, `inactive_activatable`, `active_accepting`, or `active_not_accepting` directly from current course,
assignment, material, and session-owner state. Activation and new-session eligibility are separate. Assigned supervisors
receive stable preparation blockers and authorized preparation links. Joined students can read their active courses but
receive neither preparation fields, supervisor identities, nor preparation-blocker links.

Adding a student accepts either an existing student relationship or the fields
needed for supervisor provisioning. It requires an active course and is not a
public registration workflow.

The operation explicitly selects new-account or existing-account mode. No
identity defaults are inferred. Temporary credentials are accepted only for
provisioning and are never returned. Existing-account mode accepts a complete
username, performs no search, and requires an existing student role.
Unknown and non-student usernames return the same safe not-found response. An
existing membership returns that membership idempotently. A clean rejoin creates
a new membership and restores no deleted course data.

The students collection is visible only to assigned course supervisors and uses
the standard `page[limit]` and `page[offset]` pagination. Membership resources
carry their own `cst_` ID, username and join instant, plus course and student
relationships. Removal addresses the student user ID in the route and returns
204 on success. A supervisor first reviews the exact membership through its element GET; the returned strong ETag covers
membership, active-session removal state, and every relevant deterministic cascade-impact fact. Those facts invalidate a
stale review when removable course data changes without disclosing affected resource identities to the browser.

Temporary-password and ban operations treat missing, staff, and out-of-scope
targets uniformly. Ban creation and deletion are idempotent.

Only an administrator removes a course supervisor, and the last supervisor
cannot be removed. An assigned supervisor may remove a student only when that
student has no active tutoring session in the course. Removal atomically deletes
all data owned by that student in the course while preserving the account and
other-course data. It does not wait for running workers or provider calls; late
results follow the shared zero-row stale-result rule.

Course deletion, supervisor removal, and membership removal require `If-Match` from the corresponding authorized detail
GET. The strong validator binds the actor, current blockers, action consequences, and every relevant non-disclosed,
deterministic cascade-impact fact for that action. It changes when the reviewed deletion set changes without exposing
affected resource identities. Missing validators return 428; changed validators return 412 without cleanup or audit
mutation. Authorization and lifecycle guards are rechecked in the same transaction, and clients never replay these
destructive operations automatically.

An administrator or assigned supervisor may mutate the course logo. GET uses the
same authorization as viewing the course and returns the normalized PNG with a
`Cache-Control: no-store` policy and no validator-based caching exception. Upload
limits and normalization match avatars.

Mentor removal drops affected student assignments and returns open mentoring work
to supervisor triage in one transaction. Course deletion remains restricted to
an inactive course with no active tutoring sessions. Once allowed, deletion does
not wait for running workers or provider calls; late results follow the shared
zero-row stale-result rule.

`GET|POST /courses/{id}/mentors` lists or assigns existing registered mentors.
DELETE on `/courses/{id}/mentors/{user_id}` removes one. Lists contain user
identifiers and use standard pagination.
An assigned supervisor performs the operation, and it takes effect immediately
without mentor acceptance. A mentor cannot reject or remove the course
assignment. The
`GET|POST|DELETE /courses/{id}/students/{student_id}/mentors[/{user_id}]`
routes similarly list, create, and remove immediate supervisor-controlled
assignments. The selected mentor must already belong to the course.

## Materials and files

OpenAPI defines the implemented material, file, finalization, approval, download,
and normalized-content operations.

File upload uses bounded `multipart/form-data`. MIA validates signatures and
detected media types into a temporary file, atomically renames the validated file
to its deterministic source path, then inserts and commits the file row. A failed
transaction removes the published file, and startup removes source files without
rows. A successful response means publication and row commit both succeeded.
Download responses use the safe media type derived from the material format,
safe content disposition, and `X-Content-Type-Options: nosniff`.

Link material requires a complete supervisor-authored brief at creation. For
source-backed material, the brief is generated after extraction. The server
infers private ownership from the authenticated student and never accepts an
owner field. Brief correction replaces the complete validated brief for eligible
ready course-wide material. File upload accepts one bounded multipart file and
never trusts its declared media type or filename.

The material files collection is exempt from offset pagination: the
per-material file-count hard cap, operator-configurable within fixed MIA hard
caps, keeps the collection small and bounded. It rejects every query parameter,
including pagination parameters, rather than silently ignoring input.

`content` streams authorized normalized `content.jsonl` as
`application/x-ndjson`; it does not construct a large JSON:API document and never
returns an internal path or raw OCR provider response. The normalized-content
record contract belongs to the material content specification rather than this
cross-route narrative.

Course-wide approval is separate from upload and processing. Student-private
material cannot be approved or converted to course-wide material. Changing the
validated brief content of approved course-wide material atomically revokes
approval, clears its attribution, and writes content-free correction and
revocation audit effects; an unchanged brief does not.

Files may change only while a material is draft or retryable failed. In one
transaction, finalization validates and freezes the complete file set, changes
every file and the material to processing, and inserts exactly one extraction job
per file. Any failure rolls back all changes; a uniqueness constraint prevents
duplicate active work. Ready material is immutable and must be deleted and
recreated to change its source. Approval requires ready state and a non-empty
brief; link-only material does not satisfy course readiness.

Link-only course-wide finalization instead validates the URL, absence of files,
and non-empty supervisor-authored brief and changes draft directly to ready in one
transaction without creating extraction or summary jobs.

The finalization route also retries a failed material after automatic attempts
are exhausted, with or without file changes. MIA has no generic job retry route.
Material brief responses use the strict version-1 schema documented in the
product requirements. External URL, original-filename, page-count, PDF, DOCX,
normalized-content, and summary-capacity rules apply before successful state is
reported. File-backed material reports ready only after the summary result
transaction has stored a valid brief, marked its job succeeded, and verified
every file processed.

Material deletion is rejected while the material is selected by an active
tutoring session. It does not otherwise wait for running workers or provider
calls. After session completion, normal deletion rules apply, and stale late
results cannot recreate deleted state.

## Tutoring sessions and messages

- `GET|POST /api/v1/courses/{course_id}/tutoring-sessions`
- `GET|PATCH /api/v1/tutoring-sessions/{id}`
- `POST /api/v1/tutoring-sessions/{id}/completion`
- `POST /api/v1/tutoring-sessions/{id}/summary-generations`
- `GET|POST /api/v1/tutoring-sessions/{id}/messages`
- `POST /api/v1/student-messages/{id}/response-retries`
- `POST /api/v1/tutor-responses/{id}/interruptions`
- `GET /api/v1/tutor-responses/{id}/events`
- `GET /api/v1/tutoring-sessions/{id}/materials`

Session creation requires a canonical lowercase UUID v4 `client_request_id` and
optionally includes selected material relationships. While the resulting session
is active, replaying the same ID and creation payload returns it; different
content is a conflict. After completion, every reuse of that request ID is a
conflict while the session remains retained. Authorized deletion removes its
request-ID history; MIA stores no tombstone. The owning student can complete an
active session; no abandon or staff force-completion route exists.

Selected source material must belong to the session's course and be ready and
file-backed. Course-wide selections also require approval; private selections
require ownership by the active student and no approval. Link-only material may
supply authorized identity and brief context but no retrievable source content.

PATCH permits only an assigned supervisor to update `summary` and `follow_up` on
a completed session. The transaction records supervisor summary attribution and
exactly one content-free correction audit event. Every other session field and
the completed chat remain immutable.

Each student-message creation includes a canonical lowercase UUID v4 request ID
scoped to the session. Repeating the same ID and content returns the existing
message and response operation even after completion; this replay is read-only.
Different content is a conflict. A request that would create a new message
requires an active session. Authorized account, course, or membership deletion
may cascade the owning session and removes this request-ID history without a
tombstone.

Transcript listing is paginated by immutable message-response attempt pairs.
Each `tutoring-turns` resource has the tutor-response ID as its identity and
includes the corresponding student message and one response attempt. This keeps
retry history complete without creating an unbounded nested response list.

A session accepts at most one generating response and one queued message. A
further message returns a conflict. Queued work begins after any terminal result
from current generation. Completion returns a conflict while work is queued or
generating. Response retry is available only when the session is idle.
Completed sessions reject new message creation and response retry but still
return retained identical message replays.

An assigned supervisor may request summary generation manually only for a
completed session with no summary, no queued or running summary job, and at least
one earlier terminally failed summary job.

Assigned supervisors can see active-session status — including the session's
start and last-activity instants — but cannot retrieve messages until
completion. Material retrieval performed by the AI tutor uses internal
authorized application tools, not client-selected arbitrary file paths.
Retrievable content belongs to the session's course and is ready and file-backed.
Course-wide content also requires approval; student-private content requires
ownership by the session student and no approval. Course deactivation does not
remove otherwise authorized content from an existing session.

The session-materials collection reports only material whose content reached the
tutor model; search-only matches and selected identity or brief context are not
included. The owning student can read it during or after the session. An assigned
supervisor can read it only after completion, matching completed-chat access.

### Tutor-response events

`GET /api/v1/tutor-responses/{id}/events` returns a Server-Sent Events stream
with media type `text/event-stream`. Establishing a stream reauthorizes access to
the response. The stream exposes MIA events only; raw OpenAI events, tool calls,
provider errors, and provider payloads are never forwarded.

On every connection, MIA first sends the current content and state:

```text
event: snapshot
data: {"content":"Current persisted text","state":"generating"}
```

Generation start, new text, and terminal state changes use these events:

```text
event: started
data: {"state":"generating"}

event: delta
data: {"text":"next text"}

event: completed
data: {"state":"completed"}

event: interrupted
data: {"state":"interrupted"}

event: failed
data: {"state":"failed","code":"provider_failure"}
```

MIA sends an SSE comment heartbeat every 15 seconds while no event is available.
Each stream write has a 30-second deadline. A browser disconnect closes only that
subscription; it does not cancel response generation. Reconnecting to the same
route receives a fresh snapshot followed by new deltas, so MIA does not need a
persisted per-token event history.

MIA registers the subscriber and captures the current content and state as one
per-response synchronized operation. Deltas created after that point enter the
subscriber queue, preventing a gap between snapshot and live delivery without a
persisted SSE event log. During generation the synchronized in-memory content may
be ahead of the latest bounded persistence batch; persisted content remains the
restart-recovery baseline.

If a bounded persistence update affects zero rows, MIA marks the synchronized
in-memory response stale, closes all subscriber queues, and discards every later
provider event without delivering or persisting it. Deletion alone does not
cancel the provider request; it may finish or reach its existing deadline. MIA
does not add separate cross-goroutine deletion signaling or recreate deleted
state.

Each subscriber queue holds at most 64 events or 256 KiB, whichever is reached
first. Overflow closes only that SSE connection; it never blocks provider
consumption or other subscribers. Reconnection recovers through a fresh snapshot.

The reverse proxy must not buffer this route. MIA flushes complete SSE events and
persists generated text after 16 KiB of new UTF-8 output or one second, whichever
occurs first, and before committing a terminal state.

The interruption route accepts queued and generating responses. A queued
response becomes interrupted without a provider call; a generating response
cancels its provider operation. Startup resumes queued work and marks stranded
generation failed rather than recreating an uncertain provider request.
If cancellation cannot be confirmed, MIA retains the interrupted state, discards
late events, and logs a sanitized failure.

## Generated speech

- `POST /api/v1/tutor-responses/{id}/speech`
- `GET /api/v1/tutor-responses/{id}/speech`

The POST route requests or reuses generation for the user's selected voice. The
GET route returns JSON:API state while generation is pending or failed and audio
with its documented media type when available. Every request reauthorizes access
to the source response.

POST returns `202` with a `generated-speech` resource while shared generation is
pending and `200` when reusing available cache state. GET returns that resource
for `generating` or `failed` state and returns the MP3 body for `available` state.
The concrete schemas, security requirements, bounds, and stable errors are in
OpenAPI.

Only completed tutor responses are eligible. Available audio is MP3 served as
`audio/mpeg`. Concurrent POST requests reuse one cache record and provider
operation. Retrying a failed variant transitions the same record back to
generating; request-path generation never retries automatically.

When ElevenLabs is not configured, both routes return a stable
feature-unavailable error.
Generation also requires a stored voice ID of at most 128 printable ASCII
characters; absence returns a stable validation error. Only an assigned supervisor
sharing a course with the student-only account may set, change, or clear that
voice; students cannot select it themselves. MP3 output is limited to 25 MiB and
is signature-validated before atomic publication.

At startup, after expired-speech cleanup, every cache row stranded in generating
state becomes failed with a sanitized restart code and loses any partial or
published MP3. MIA does not repeat the uncertain ElevenLabs request. A later
authorized POST retries the same row through the normal failed-to-generating
transition. Startup validates all required material and speech files before it
removes either cache's filesystem orphans.

## Mentoring

One resource represents the request, mentor response, optional appointment, and
closure:

- `GET|POST /api/v1/courses/{course_id}/mentoring-sessions`
- `GET|PATCH /api/v1/mentoring-sessions/{id}`
- `GET /api/v1/courses/{course_id}/mentor-students/{student_id}`
- `GET /api/v1/courses/{course_id}/mentor-students/{student_id}/avatar`

Creation is unassigned and may include `proposed_for`. An assigned course
supervisor selects one of the student's assigned mentors through `PATCH`. The
assigned mentor may then respond and set `scheduled_for`. The student or
supervisor may cancel an unscheduled request; the student or assigned mentor may
cancel a future schedule; only the assigned mentor may complete it after the
scheduled time.

The mentor-student routes expose only username, name, nickname, and an optional
avatar URL. Both routes require the current mentor's explicit assignment to that
student in that course. Removing either assignment immediately revokes this
identity and avatar access with the same existence-hiding not-found response.

The supervisor-managed `PATCH /users/{id}` operation currently accepts only
`mentoring_requests_allowed`. It applies globally to the student-only account after shared-course authorization and does
not change or close existing mentoring records.

Topics are limited to 4,000 Unicode code points and 16 KiB, mentor responses to 8,000 code points and 32 KiB, meeting
instructions to 4,000 code points and 16 KiB, and HTTPS meeting URLs to 2,048 bytes. Text follows the ordinary normalized
multiline-text rules. Proposed and scheduled times use strict API UTC instants.

An assigned supervisor may directly replace the current mentor on an open row.
The update preserves proposed and scheduled times, meeting details, prior
response, and immutable response authorship. Mentor removal remains a separate
operation that clears future schedule details and returns open work to triage.

Course deactivation blocks creation but not existing work. Mentor removal clears
the current mentor, proposed and scheduled times, and meeting details on open work
and returns it to supervisor triage.

MIA provides no live mentoring channel. Meeting URLs are HTTPS metadata and are
never fetched by MIA.

## Jobs and audit

- `GET /api/v1/jobs`
- `GET /api/v1/jobs/{id}`
- `GET /api/v1/materials/{id}/jobs`
- `GET /api/v1/tutoring-sessions/{id}/jobs`
- `GET /api/v1/audit-events`

Job usage fields are unattributed, model-agnostic cumulative counters across
attempts. They may span configuration changes and are operational information,
not model-specific attribution or authoritative billing totals.

Administrators can inspect platform jobs. Supervisors receive only the safe
processing status needed for resources in their assigned courses; they do not
receive unrestricted provider diagnostics.

The material-scoped job view is assigned-supervisor-only and omits failure
details and provider usage. OpenAPI defines the administrator and subject-scoped
resource fields and pagination contract.

Audit access is administrator-only through `GET /api/v1/audit-events`. The
newest-first collection uses the standard bounded pagination parameters and
rejects all other query parameters. Audit resources expose only stable action
names, live actor/subject/course identifiers when retained, random deletion
fingerprints, typed content-free metadata, and the event instant. Account
deletion applies one random fingerprint to every retained actor, subject, and
mentor-metadata reference to that identity; no identity mapping is retained.

The first version has no manual job retry route. MIA performs only its bounded
automatic retries.

## Open decisions

Authentication, invitation, current-user, course, material, job, tutoring,
mentoring, generated-speech, account-deletion, and audit transport contracts are
concrete in OpenAPI. New route families must add their transport decisions there;
field-level contracts are not duplicated in this narrative.
