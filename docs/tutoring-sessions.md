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
4. The identity and brief of approved course-wide material or student-private
   material selected by the student. Complete extracted content is included only
   when it fits within MIA's fixed input limit; otherwise MIA supplies bounded
   relevant excerpts. This item is omitted when no material is selected.
5. Summary and follow-ups of the previous session.

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
start. It may include the complete extracted content of a small selected
material only when it fits within MIA's fixed input limit. Otherwise, it sends
bounded relevant excerpts.

The AI tutor can request additional material during the session. MIA provides
tools that let it:

- search student-selected material and other material available to the student;
- request a bounded excerpt from a specific material, chapter, or section.

Every request is authorized by MIA. The available set consists only of approved
material from the active course and private material uploaded by the active
student. A request made by the model never grants access by itself. MIA rejects
requests for unapproved course-wide material, another student's private
material, or material outside the active course.

Search and excerpt results include the material identity, chapter, and section
when available. The AI tutor should use this information when referring the
student to another source, for example, "Workbook XYZ, chapter 7.3 explains this
topic." It must not present an OCR page position as the printed page number.

MIA records material actually used during the session, including material
retrieved after the session started. The retrieval implementation is not defined
here; it may use provider-supported file search or application-controlled search
as long as it preserves this behavior and the authorization boundaries.

## Starting a tutoring session

A student can conduct only one session at a time across all courses. Starting
another session fails with an instruction to complete the active session first.

## Student-private material

Material uploaded by a student is visible to that student, the AI tutor, and
supervisors assigned to the course. Assigned supervisors can inspect the source,
its generated brief, and the completed session's full chat history. It does not
require supervisor approval and must not be exposed to mentors, other students,
unrelated supervisors, or administrators who are not assigned as supervisors.
Student-private material remains available to its uploading student but cannot
be converted into course-wide material for all students.

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
