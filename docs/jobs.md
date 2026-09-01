# Jobs

Audience: developers. This document defines the current first-version background
job design. The [product requirements](product-requirements.md) remain
authoritative for user-visible behavior.

MIA performs background tasks to process materials and chats asynchronously. The
`jobs` table acts as the queue and records current job state.

The first version uses one worker that processes one job at a time. When no job is
due, the worker waits one second before polling again.

The implemented queue stores explicit `subject_type`, `subject_id`, `course_id`,
and nullable owner columns. It stores no arbitrary provider payload. Administrators
can inspect full sanitized records through `/api/v1/jobs`; assigned supervisors
can inspect safe material-scoped state through `/api/v1/materials/{id}/jobs`.

## Execution policy

The worker atomically claims one queued job with a two-minute lease and a unique
lease token. It renews the lease every 30 seconds while work continues. Result
commits require the current lease token, so an expired or cancelled attempt
cannot write stale output.

Destructive operations do not coordinate with a running worker or provider call.
Late commits update an existing target only. A zero-row guarded update discards
the result, deletes output newly published by that attempt, and never upserts,
requeues, or retries the provider request.

A job has at most three attempts. Transient timeout, throttling, and provider 5xx
failures retry after one minute and then five minutes. Validation, authorization,
malformed provider output, and other permanent failures do not retry. A valid
provider `Retry-After` increases the normal delay up to one hour; malformed
values are ignored and no provider value can delay a retry longer than one hour.
When no attempt remains, MIA performs the job subject's documented terminal
failure transition in the same transaction as the failed job state.

An OpenAI material- or session-summary request uses a ten-second response-header
timeout and a two-minute total deadline. One Mistral OCR chunk uses a 30-second
response-header timeout and a five-minute total deadline.

Startup treats an expired running lease as an abandoned attempt. It requeues the
job when attempts remain and otherwise performs the subject's terminal-failure
transition. Every attempt uses current server configuration and processing code.
Provider requests have no cross-attempt idempotency guarantee. Logical output
commits only while the attempt owns the live lease, but an ambiguous retry may
repeat paid provider work.

Graceful shutdown stops claiming jobs and gives the running job up to 30 seconds
to finish. MIA then cancels and requeues it when attempts remain; the interrupted
attempt stays counted. With no attempt remaining, it performs the subject's
terminal-failure transition.

## OCR

Finalizing a draft PDF, PNG, or JPEG material queues its OCR work once. The job
sends provider-compatible requests to the Mistral OCR API, validates the
responses, and writes normalized extracted content to the source file's
deterministic `content.jsonl` path. Each strict line contains `version: 1`, a
positive contiguous `sequence`, nullable chapter and section labels, and text.
Unknown fields are rejected. Encoded JSONL and decoded text are each bounded to
512 MiB per material. Raw provider responses are discarded.

The job splits large files into bounded chunks using current processing code. A
retry starts the source again and need not reproduce earlier boundaries or
provider payloads. A file that exceeds a provider limit and cannot be split, such
as a large PNG or JPEG image, is marked faulty. If one file is faulty, the entire
material remains unusable until that file is removed or replaced. MIA never
approves or uses a partial material while silently excluding a failed file.

Finalizing DOCX, UTF-8 text, or Markdown queues bounded local extraction instead.
MIA validates and extracts these formats without sending them to Mistral. DOCX
processing treats its ZIP and XML structures as untrusted input and never runs
macros or embedded content.

DOCX extraction disables entities and external relationships and extracts the
main document, tables, headers, footers, footnotes, endnotes, and inserted tracked
changes. PDF validation rejects encryption, malformed cross-references, embedded
files, JavaScript, and launch actions before sending page content to Mistral.

Finalization accepts only draft or authorized retryable failed material. In one
transaction it validates and freezes the complete source set, changes every file
and the material to processing, and inserts exactly one extraction job per file.
A partial unique index prevents duplicate queued or running extraction work per
file. The last successful extraction queues one material-summary job in the same
transaction. Every terminal extraction failure marks the job and material failed
and cancels all remaining material-owned jobs. Late results from cancelled or
expired leases are discarded.
Deleting a material cascades its jobs without waiting for a running worker. The
ordinary stale-result rule handles any late attempt.

## Material Summary

After every required extraction completes, MIA queues one material-summary job.
The configured job model creates the material brief
using instructions from
`<data-dir>/llm-instructions/jobs/material/<material-type>.md`. The job updates
`materials.brief_json`, `materials.brief_source`, and
`materials.brief_updated_at`. In one result transaction, successful completion
validates and stores the brief, verifies every source file is processed, marks the
job succeeded, and changes the material from processing to ready. A terminal
summary failure marks the job and material failed and cancels all remaining
material-owned jobs.

Website and YouTube links are metadata only and do not trigger server-side
fetching or a material-summary job. A link-only material requires a
supervisor-authored brief. AI-readable source content requires a separately
uploaded supported file.

The output is a material brief used by supervisors and by the AI tutor when it
selects relevant material. It is not an unrestricted prose summary. One brief is
created for each material. It is a strict version-1 object requiring `version`,
`summary`, `subjects`, `learning_goals`, `sections`, and `warnings`, with nullable
`educational_level`. Each section requires `sequence`, nullable `label`, `title`,
`description`, and `search_terms`. Unknown fields are rejected, and the encoded
object is at most 256 KiB. Per-field count and string limits are defined in
`product-requirements.md` under material processing. It contains:

- a concise description of the material and its purpose;
- the intended educational level, when the source identifies it;
- the main subjects and concepts;
- learning goals explicitly supported by the source;
- a content outline for chapters, sections, worksheets, or exercises;
- source locations, such as chapter and section names, when available;
- short topic descriptions and search terms for locating relevant passages;
- warnings about incomplete, unreadable, ambiguous, or low-quality OCR content.

For large material, the brief contains a concise overall summary and a
section-level outline. It must not attempt to reproduce or summarize the entire
source in one long narrative.

The generated brief must remain grounded in the source. It must not invent
learning goals, educational levels, source locations, or content. If the job
cannot produce a trustworthy brief, it fails visibly instead of storing a
plausible but unsupported result.

The brief does not use OCR page positions as printed page numbers. OCR output may
not preserve the pagination of the original material, so references use chapter
and section labels instead.

For course-wide material, the generated brief is a draft. A supervisor can
review and correct it before approving the material. Regeneration must not
silently overwrite supervisor edits; it must preserve them or report that
explicit human resolution is required.

A brief generated for student-private material has the same privacy as its
source. It is visible to the student who uploaded the material, the AI tutor,
and supervisors assigned to the course. It requires no supervisor review or
approval. Mentors and other students have no access.

Material and session summaries use the same bounded process. Material files use
ascending creation time and then ascending immutable file ID, followed by segment
sequence; chats use message sequence. The process uses at most 64 chunks of
24,000 tokens. Chunk boundaries
prefer JSONL segments or chat messages; an oversized unit is split at a
Unicode-safe token boundary while retaining source identity. Reduction greedily
combines ordered summaries into at most 24,000 input tokens per call, uses no more
than two rounds, and limits each output to 2,048 tokens.

MIA verifies complete coverage before the first provider call. If one final
summary cannot be formed within those bounds, it fails with
`summary_input_too_large`; it never samples or truncates while claiming
completeness. Intermediate summaries remain in memory. A failed attempt discards
them and restarts from the beginning with current configuration and processing
code while cumulative provider usage and sanitized failure state remain
persisted. Usage is an unattributed, model-agnostic operational total across
attempts and may span configuration changes.

## Tutoring Session Summary

After a tutoring session is completed, MIA queues a tutoring-session-summary
job. The configured job model receives the complete chat history and the names
and types of material used. It follows instructions from
`<data-dir>/llm-instructions/jobs/tutoring-session-summary.md` and updates the
session's `summary`, `follow_up`, `summary_source`, and `summary_updated_at`.
The result describes the student's strengths and weaknesses and suggests work
for the next tutoring session.
If a used material was deleted before this job reads its identity, the input
omits that material name and type. Material deletion does not delete or block a
session-owned summary job.

The generated summary and follow-up are drafts. An assigned supervisor can
correct them, and every correction is audited. Regeneration must not silently
overwrite supervisor corrections. The completed chat history remains immutable.

Session completion atomically queues one summary job. After automatic attempts
end in a terminally failed summary job, an assigned supervisor may request
regeneration only while the session remains completed, the summary is absent, and
no summary job is queued or running. This domain action creates a new job; MIA
exposes no generic job-retry route.
