# Tutoring sessions

Audience: product designers and developers. This document describes the current
tutoring-flow design. The [product requirements](product-requirements.md) remain
authoritative for user-visible behavior and authorization boundaries.

## Chain of instructions

A tutoring session starts by sending the following information to the AI tutor
in this order:

1. Base AI tutor instructions from `<data-dir>/llm-instructions/init.md`.
2. Course brief with name, description, curriculum, learning goals, AI tutor
   instructions, and language.
3. Student brief with nickname, year of birth, language, country, and AI tutor
   instructions.
4. The identity and brief of authorized course-wide or student-private material
   selected by the student. Complete extracted content is included only for
   ready, file-backed material when it contains at most 8,000 Unicode code points
   and 32 KiB and fits within MIA's fixed input limit; otherwise MIA supplies
   bounded relevant excerpts. Link-only material provides no source content. This
   item is omitted when no material is selected.
5. Summary and follow-ups of the previous completed session for the same student
   in the same course. When a supervisor has corrected them, the corrected
   version is used.

## Material context and retrieval

The student may select one or more primary materials when starting a session.
The initial context identifies each student-selected material by ID, name, type,
material brief, and content outline. This lets the AI tutor understand statements
such as "I want to use textbook XYZ" or "I have a question about worksheet
Foo."

Selecting material is optional. A student may start with only an intent, such as
"Let's learn vocabulary." Once the intent is clear, the AI tutor can search for
and choose suitable material available to that student. MIA authorizes the
selection before returning content.

MIA does not send the complete content of every available material at session
start. It includes complete selected content only at or below 8,000 Unicode code
points and 32 KiB and only when the content fits the 32,000-token input budget.
Otherwise, it sends bounded relevant excerpts.

The AI tutor can request additional material during the session. MIA provides
tools that let it:

- search student-selected material and other material available to the student;
- request a bounded excerpt from a specific material, chapter, or section.

One response performs at most three retrieval rounds and receives at most eight
excerpts. An excerpt contains at most 4,000 Unicode code points and 16 KiB. The
combined result remains within the model-input budget.

Every request is authorized by MIA. Retrievable source content belongs to the
session's course and is either ready, file-backed, approved course-wide material
or ready, file-backed private material owned by the active student. Course
activity is required when starting the session, not while it continues after
deactivation. A request made by the model never grants access by itself.

Search and excerpt results include the material identity, chapter, and section
when available. The AI tutor should use this information when referring the
student to another source, for example, "Workbook XYZ, chapter 7.3 explains this
topic." It must not present an OCR page position as the printed page number.

MIA records a material as used only when complete small content or an authorized
excerpt is returned to the tutor model. Search-only candidates do not count. It
performs bounded streaming search over authorized `content.jsonl` files. Search
NFC-normalizes and Unicode-case-folds terms, tokenizes on Unicode letter/number
boundaries, ranks by matched-term count and frequency, and tie-breaks by material
ID, file ID, and segment sequence. No term match returns no result.

One excerpt may include at most one adjacent segment on each side while the
combined result remains within 4,000 code points and 16 KiB. Overlapping excerpts
are deduplicated. The MVP has no retrieval index and does not upload material to
a provider-managed file store.

One request reserves at most 32,000 input tokens and 2,048 output tokens. MIA uses
a local tokenizer matching the configured model. It always retains required
instructions and the current student message, then adds the newest complete
conversation turns that fit. It omits older turns without generating a rolling
summary.

## Response Streaming

MIA consumes the OpenAI Responses API stream in an operation that is independent
of the browser connection. It translates provider events into MIA state and
Server-Sent Events rather than forwarding raw provider events.

The provider must return the first event within 30 seconds, continue producing an
event at least every 60 seconds, and finish within ten minutes. A timeout fails
the response while preserving text already received. MIA does not automatically
retry the uncertain provider request.

After creating a student message, the frontend connects to the tutor response's
SSE route. MIA first sends a snapshot of current content and state, followed by
new text deltas and one terminal event. It persists generated text in bounded
batches after 16 KiB of new UTF-8 output or one second, whichever occurs first,
and always before committing a terminal state.

Subscriber registration and snapshot capture occur atomically under
per-response synchronization. Later deltas enter the subscriber queue, so a
connection cannot miss text in the transition from snapshot to live delivery.

If a bounded persistence update affects zero rows, MIA marks the synchronized
in-memory response stale, closes every subscriber queue, and discards later
provider events. It delivers and persists no further content or terminal event.
The provider request may finish or reach its existing deadline; deletion does not
add cross-goroutine cancellation signaling.

Each subscriber queue is bounded to 64 events or 256 KiB. A slow subscriber that
exceeds either bound is disconnected without affecting generation or other
subscribers and can reconnect for a fresh snapshot.

Disconnecting the browser removes only that SSE subscription. It does not cancel
the OpenAI operation. A reconnect to the same tutor response receives a fresh
snapshot and continues with new deltas without creating another provider
response. An explicit student interruption uses the interruption API, cancels
the provider operation, and preserves text already received.
If cancellation cannot be confirmed, MIA keeps the response interrupted,
discards late events, and logs only a sanitized cancellation failure.

Provider failures preserve partial text and produce a sanitized failed state.
Tool calls and provider payloads remain internal to MIA. Every SSE connection is
authorized for the requesting user and tutor response.

An idle stream sends an SSE comment heartbeat every 15 seconds. Each write has a
30-second deadline. Heartbeats do not count as authenticated session activity.

## Tutor work queue

A session has at most one generating response and one queued student message.
The queued response begins after the current response reaches any terminal state
and uses preserved partial text as conversation context. Further message
submissions and session completion are rejected while their required slot is not
available.

The student may interrupt queued work before it starts. The immutable student
message remains, its response becomes interrupted, and MIA makes no provider
request. Retrying a failed or interrupted response is allowed only while the
session is idle and creates its sole queued response.

At startup, queued work remains safe to dispatch. A response left in generating
state is failed with a safe restart code because MIA cannot know whether its
provider request ran. During graceful shutdown MIA starts no queued work, gives
active generation 30 seconds to finish, then cancels and fails what remains.

The owner-only current-work read is the authoritative browser view of this
queue. One database snapshot distinguishes idle, generating, queued, combined
generating-and-queued, and completed states. It includes immutable identities
for current work, remaining queue capacity, and independent eligibility for
submit, queue, Stop, retry, reconnect, and finish. Eligible reconnect work
identifies the immutable response SSE target. The browser does not reconstruct
these answers from transcript pages, elapsed time, SSE events, or failed
mutations.

Optional retained message-request and response identities let this read
reconcile ambiguous outcomes. Stop success is bound to the requested immutable
response; automatic handoff may expose another response as current work without
changing the stopped response's terminal outcome. Reads, polling, and reconnects
never start provider work or refresh browser-session idle expiry.

Explicit retries carry a canonical UUID v4 request ID bound to the target
message. Identical replay returns the existing linked response, including after
session completion; changed payload reuse conflicts. A new retry still requires
an active idle session.

## Starting a tutoring session

A student can conduct only one session at a time across all courses. Starting
another session fails with an instruction to complete the active session first.

The authenticated current-account active-session read is the authoritative way
to find that session across courses. It returns the owned active session with its
course ID and name, or a successful JSON:API `data: null` when none exists. It
does not refresh browser-session idle expiry. Students use the same read after
sign-in, reload, browser or network interruption, device change, an ambiguous
creation response, or an active-session conflict. They do not enumerate courses
or automatically replay creation.

Course deactivation blocks replacement session creation but does not hide or end
an existing active session. The discovery read continues to return it until the
owning student finishes it. Authentication expiry, loss of student authorization,
and a lookup dependency failure remain distinct from normal absence. A database
lookup failure returns the shared safe HTTP `500 internal_error` response.

## Student-private material

Material uploaded by a student is visible to that student, the AI tutor, and
supervisors assigned to the course. Assigned supervisors can inspect the source,
its generated brief, and the completed session's full chat history. It does not
require supervisor approval and must not be exposed to mentors, other students,
unrelated supervisors, or administrators who are not assigned as supervisors.
Student-private material remains available to its uploading student but cannot
be converted into course-wide material for all students. Its source content is
usable only after processing succeeds and while it remains ready and file-backed.

Material selected by an active tutoring session cannot be deleted. Normal
deletion rules apply after the session is completed.

If the student later deletes private material used in a completed session, MIA
removes the source and generated brief and prevents future retrieval. Existing
chat messages and completed-session summaries remain unchanged. Quotations from
the deleted material may remain without a retrievable source.

## Mentoring escalation

Mentoring is the last resource for an educational difficulty. Before suggesting
it, the AI tutor must make a meaningful effort using clarifying questions,
alternative explanations, examples, guided exercises, and relevant authorized
material.

The AI tutor may suggest mentoring only if at least one mentor is assigned to the
student in the active course and `mentoring_requests_allowed` permits a new
request. If no mentor is assigned, MIA disables all student-facing mentoring
features. Disabling new requests does not cancel existing requests or scheduled
sessions.

## Safeguarding boundary

MIA is not an emergency service and tutoring sessions are not monitored in real
time. If a message suggests immediate danger, self-harm, abuse, or another
safeguarding concern, the AI tutor responds supportively and directs the student
to a trusted person or appropriate local emergency service. Supervisors provide
local safeguarding guidance outside MIA.

MIA does not automatically notify supervisors, mentors, guardians, emergency
services, or anyone else. It must not imply that an alert was sent or that a
person will respond. Mentoring is not an emergency channel. The message remains
in the ordinary chat history and may be reviewed later under the normal
completed-session access rules.
