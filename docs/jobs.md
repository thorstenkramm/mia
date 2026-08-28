# Jobs

Audience: developers. This document defines the current first-version background
job design. The [product requirements](product-requirements.md) remain
authoritative for user-visible behavior.

MIA performs background tasks to process materials and chats asynchronously. The
`jobs` table acts as the queue and records current job state.

The first version uses one worker that processes one job at a time. When no job is
due, the worker waits one second before polling again.

## OCR

After a PDF, PNG, or JPEG material is completely uploaded, MIA queues an OCR job.
The job sends provider-compatible requests to the Mistral OCR API, validates the
responses, and writes normalized extracted content to the source file's
deterministic `content.txt` path. Raw provider responses are discarded.

The job splits large files into chunks that respect provider limits. A file that
exceeds a provider limit and cannot be split, such as a large PNG or JPEG image,
is marked faulty. If one file is faulty, the entire material remains unusable
until that file is removed or replaced. MIA never approves or uses a partial
material while silently excluding a failed file.

## Material Summary

After OCR completes, or after directly readable material is uploaded, MIA queues
a material-summary job. The configured job model creates the material brief
using instructions from
`<data-dir>/llm-instructions/jobs/material/<material-type>.md`. The job updates
`materials.brief_json`, `materials.brief_source`, and
`materials.brief_updated_at`.

Website and YouTube links are metadata only and do not trigger server-side
fetching or a material-summary job. AI-readable content requires a separately
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
