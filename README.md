# MIA - AI powered tutoring

> [!IMPORTANT]
> MIA is currently in product, API, and implementation planning. This repository
> does not yet contain a runnable server or release.

MIA helps students prepare for their current classes and exams. Unlike platforms with a predefined curriculum, MIA
bases tutoring on the curriculum, learning goals, educational approach, and materials used in the student's classes and
selected by the course supervisors. The name "MIA" is an abbreviation of the Spanish "mi inteligencia artificial"
("my artificial intelligence").

MIA is a learning platform for students, supervisors, and mentors. MIA conducts personal and custom-tailored tutoring
sessions based on the learning material used at school. MIA works aligned with the learning goal given by the school's
curriculum.

MIA is not a self-learning platform, and it does not come with any prepared material, courses, or curricula. An
administrator must create each course and assign its supervisors. The supervisors are responsible for preparing the
course content.

MIA is a self-hosted learning platform that can run on almost any Linux server. Users interact via a browser with the
system.

While MIA is distributed under a free (MIT) license, running it is not free. You will need paid accounts for the
following services:

- [OpenAI API](https://openai.com/api/), used for AI tutoring and background jobs
- MistralAI, used as an [OCR document service](https://mistral.ai/solutions/document-ai/)
  (Mistral will prepare uploaded learning material for machine/LLM readability)
- Outgoing E-Mail service via SMTP
- [Sinch clicksend](https://www.clicksend.com/en/) for sending SMS (optional)
- [Eleven Labs](https://elevenlabs.io/text-to-speech) to text to speech (optional)

> [!NOTE]
> MIA development is divided between the backend in this repository and a
> separately maintained frontend repository.

The [documentation index](docs/README.md) provides reading paths for product,
operator, and developer documentation. Confirmed product behavior is defined in
the [product requirements](docs/product-requirements.md).

The person or organization operating a MIA server is responsible for deciding
who may use it, obtaining any required consent, and complying with applicable
local and institutional policies. The MIA project and its authors provide the
self-hosted software but do not operate third-party deployments. MIA does not
provide guardian accounts, age verification, or a guardian-consent workflow.

Uploaders are responsible for having any rights required to use their material.
MIA does not validate copyright, request license information, or require a
rights attestation.

Website and YouTube material is stored as link metadata only. The MIA server
does not fetch external URLs; AI-readable content must be uploaded separately in
a supported file.

## How does MIA work?

Let's go through the steps required to offer tutoring to students.

**Step 1: Create a course**: An administrator adds a course to MIA and grants one or more supervisors access to it. For
example, "English, fifth grade, secondary school". The course must be aligned with the school curriculum, and it should
not cover more than the school year.

**Step 2: Prepare a course**: An assigned supervisor prepares the course content. Preparation consists of the following
tasks:

- Create the description and high-level learning goals of the course.
- Fine tune the LLMs instructions and rules which will be the guard rails for all chats the students will conduct with
  the LLM. Supervisors shall give the LLM context about the students.
- Upload learning material. Ideally, the entire text book using in class is uploaded. High-quality scans are preferred.
  AI-powered OCR will process the material and store it for further using during the tutoring sessions.
- For each uploaded material the LLM creates a summary with the learning goals outlined. These summaries must be
  reviewed and corrected by the supervisor.
- Approve course-wide material that should be available to all students in the course. A supervisor can grant or revoke
  approval at any time. Unapproved course-wide material is not available to students or used by the AI tutor.

**Step 3: Add students**: Once a course is prepared, a supervisor can create a
student account with a username and temporary initial password or add an existing
student account to the course. Students do not need an email address. A new
student must change the supervisor-chosen temporary password at first login. The
supervisor can add context and special LLM instructions for the student, which
become part of the context during tutoring sessions.

MIA provides no public sign-up workflow. Administrators, supervisors, and mentors
require a verified email address. Supervisor and mentor registration is
invitation-only; student accounts are provisioned by assigned supervisors, and
student email is optional.
New supervisors and mentors choose their own username and password while
accepting the invitation.
With the server stopped, the operator creates the first administrator through a
one-time interactive local command. The command is disabled after the first
administrator exists.

**Step 4: make students familiar**: While technically not required, this step is crucial: Make the students familiar
with the tutoring platform. Integrate it into regular classes. Or conduct the first tutoring sessions under personal
supervision. The younger the students are, the more initial help they will need. Adding students to a course is not
fire and forget.

**Step 5: Conduct a tutoring session**: The student logs in, selects a course, and starts a tutoring session. Selecting
material is optional. The student may choose material or begin with only an intent, such as "Let's learn vocabulary."
The LLM receives the course instructions, any material selected by the student, and the student's individual
instructions as context. Typically, the LLM suggests activities for the session, such as making exercises, vocabulary
training or explaining class content or answering course-related questions. On completing a session, the LLM tries to
summarize the learning progress, strength, and weaknesses of the student. With every session completed, the LLM can
elaborate a more specific training session based on previous experiences.

Once the session intent is clear, the AI tutor can search any material available
to the student and choose suitable working material. It can request relevant
excerpts and refer the student to a specific material, chapter, or section when
that information is available. MIA authorizes every material request.

During a session, the student can upload private material, such as homework, worksheets received in class, or an exam.
Student-uploaded material is visible to the student who uploaded it, the AI tutor, and supervisors assigned to the
course. Assigned supervisors can inspect the files, generated material briefs, and complete tutoring history. The
material is available to the uploading student without approval but does not become course-wide material available to
all students. It is not visible to mentors, other students, unrelated supervisors, or administrators who are not
assigned as supervisors.

The student can delete their private material. Deletion removes its files and
generated material brief and prevents future use. Existing chat histories and
completed-session summaries remain, including quoted content whose source is no
longer available.

During a session, the AI tutor tries to identify and resolve difficulties using
clarifying questions, alternative explanations, examples, exercises, and
relevant material. Mentoring is the last resource and is suggested only after
these attempts fail. If the student has no mentor assigned in the course, all
student-facing mentoring features are disabled.

The student can listen via text-to-speech to the LLM responses on request.

MIA is not an emergency service and does not provide real-time human monitoring
or automated safeguarding alerts. The AI tutor responds supportively and may
direct a student to a trusted person or appropriate local emergency service, but
it must not imply that anyone has been notified. Supervisors provide local
safeguarding guidance outside MIA.

**Step 6: Fine tune**: A supervisor can review completed tutoring sessions in MIA
and correct or fine-tune the LLM instructions at course or student level. MIA
sends no workflow notifications.

## Technical requirements

The planned MIA backend, also called the API server, will be distributed as a
single dependency-free binary for modern x86-64 Linux systems. It will not
include the separately distributed frontend. Because MIA stores and processes
sensitive personal data, HTTPS is mandatory. MIA does not provide a TLS
listener. Run it behind a reverse proxy, such as Caddy or Nginx.

MIA uses SQLite as its database and the file system for uploaded and generated files. An external database is not
required or supported. The database and file storage together form one consistent data set and must be backed up and
restored together.

MIA retains data until it is deleted; generated speech uses its separate
configured retention period. Deleting a student removes that student's data.
Deleting a course removes its course-scoped data but keeps associated user
accounts and their data from other courses. MIA deletes only its live local data;
the operator remains responsible for external-provider data and backups.

Depending on the number of courses and students, you need disk space for storing the uploaded materials. Also, the
database grows over time.

Fast and reliable internet access is crucial. The server constantly connects to the LLM's APIs.

## Roles inside MIA

A user can have one or all of the following roles:

Every user has a preferred IANA time zone used to display dates and times and to
create server-generated communications. Students cannot change this setting;
any supervisor assigned to one of the student's courses can set it.

- **administrator**: An admin is allowed to
  - manage courses
  - permanently delete courses
  - assign supervisors to courses
  - invite supervisors to the system
  - supervise the platform background jobs
  - access the audit log
- **supervisor**: A supervisor is allowed to (all on course level)
  - edit all details
  - activate or deactivate the course
  - upload or delete material
  - review and correct material summaries
  - approve course-wide material or revoke its approval
  - inspect private material uploaded by students in the course
  - review completed students' tutoring sessions, including access to the full chat history
  - provision student accounts and add students to a course
  - invite mentors to the course and assign or reassign them to students
  - view and edit non-security profile data for students in the course
  - set a temporary student password for account recovery
  - ban or unban a student
- **mentor**: A mentor is allowed to
  - respond to questions
  - schedule a personal tutoring session using an external meeting channel
- **student**: A student is allowed to
  - view its own profile
  - upload an avatar
  - change its own mobile number after confirming the new number by SMS code
  - conduct tutoring sessions
  - upload private material for use by the AI tutor
  - delete its own private material
  - view the summary of strengths and weaknesses
  - download approved learning materials of assigned courses

Students cannot change other profile fields. If SMS delivery is unavailable,
the current mobile number remains unchanged.

A supervisor can recover a student's account by setting a temporary password.
MIA restricts existing browser sessions to password replacement and logout, and
the student must replace the temporary password at the next login.

Supervisors, mentors, and administrators recover forgotten passwords through a
single-use link sent to their verified email address. Existing stateless browser
cookies remain valid until their normal expiry in the MVP.
