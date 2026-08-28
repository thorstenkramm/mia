# Jobs

Audience: developers. This document defines the current first-version background
job design. The [product requirements](product-requirements.md) remain
authoritative for user-visible behavior.

MIA performs background tasks to process materials and chats asynchronously. The
`jobs` table acts as the queue and records current job state.

The first version uses one worker that processes one job at a time. When no job is
due, the worker waits one second before polling again.

## Execution policy

The worker atomically claims one queued job with a two-minute lease and a unique
lease token. It renews the lease every 30 seconds while work continues. Result
commits require the current lease token, so an expired or cancelled attempt
cannot write stale output.

A job has at most three attempts. Transient timeout, throttling, and provider 5xx
failures retry after one minute and then five minutes. Validation, authorization,
malformed provider output, and other permanent failures do not retry. A valid
provider `Retry-After` increases the normal delay up to one hour; malformed
values are ignored and no provider value can delay a retry longer than one hour.

An OpenAI material- or session-summary request uses a ten-second response-header
timeout and a two-minute total deadline. One Mistral OCR chunk uses a 30-second
response-header timeout and a five-minute total deadline.

Startup treats an expired running lease as an abandoned attempt. It requeues the
job when attempts remain and otherwise marks it failed. Provider requests use a
stable idempotency key derived from the job ID when supported. Logical output is
idempotent and commits only while the attempt owns the live lease.

Graceful shutdown stops claiming jobs and gives the running job up to 30 seconds
to finish. MIA then cancels and requeues it when attempts remain; the interrupted
attempt stays counted. A job with no attempt remaining becomes failed.

## OCR

Finalizing a draft PDF, PNG, or JPEG material queues its OCR work once. The job
sends provider-compatible requests to the Mistral OCR API, validates the
responses, and writes normalized extracted content to the source file's
deterministic `content.txt` path. Raw provider responses are discarded.

The job splits large files into chunks that respect provider limits. A file that
exceeds a provider limit and cannot be split, such as a large PNG or JPEG image,
is marked faulty. If one file is faulty, the entire material remains unusable
until that file is removed or replaced. MIA never approves or uses a partial
material while silently excluding a failed file.

Finalizing DOCX, UTF-8 text, or Markdown queues bounded local extraction instead.
MIA validates and extracts these formats without sending them to Mistral. DOCX
processing treats its ZIP and XML structures as untrusted input and never runs
macros or embedded content.

Finalization atomically queues one extraction job per source file. The last
successful extraction queues one material-summary job in the same transaction.
A permanent extraction failure marks the material failed and cancels its other
non-terminal extraction jobs. Late results from cancelled or expired leases are
discarded.

## Material Summary

After every required extraction completes, MIA queues one material-summary job.
The configured job model creates the material brief
using instructions from
`<data-dir>/llm-instructions/jobs/material/<material-type>.md`. The job updates
`materials.brief_json`, `materials.brief_source`, and
`materials.brief_updated_at`.

Website and YouTube links are metadata only and do not trigger server-side
fetching or a material-summary job. A link-only material requires a
supervisor-authored brief. AI-readable source content requires a separately
uploaded supported file.

The output is a material brief used by supervisors and by the AI tutor when it
selects relevant material. It is not an unrestricted prose summary. One brief is
created for each material and contains:

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

## Tutoring Session Summary

After a tutoring session is completed, MIA queues a tutoring-session-summary
job. The configured job model receives the complete chat history and the names
and types of material used. It follows instructions from
`<data-dir>/llm-instructions/jobs/tutoring-session-summary.md` and updates the
session's `summary`, `follow_up`, `summary_source`, and `summary_updated_at`.
The result describes the student's strengths and weaknesses and suggests work
for the next tutoring session.

The generated summary and follow-up are drafts. An assigned supervisor can
correct them, and every correction is audited. Regeneration must not silently
overwrite supervisor corrections. The completed chat history remains immutable.

Session completion atomically queues one summary job. After automatic attempts
are exhausted, an assigned supervisor may request regeneration while the summary
is absent and no summary job is queued or running. This domain action creates a
new job; MIA exposes no generic job-retry route.
