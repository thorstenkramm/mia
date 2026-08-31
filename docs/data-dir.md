# The data directory

Audience: operators and developers. This document defines the current filesystem
layout beneath configured `main.data_dir`.

MIA stores SQLite data, uploads, and generated files in the configured data
directory. The directory must already exist. At startup, MIA creates missing
internal subdirectories. Operators must reserve enough space for expected users,
courses, and material.

The MVP treats the complete data directory and its backups as private trusted
storage. It does not apply application-layer encryption to SQLite fields,
including TOTP secrets and active SMS codes. The data directory must be owned by
the effective service user and have no group or other permission bits. MIA
creates internal directories with mode `0700` and files with mode `0600`.
Anyone who can bypass these permissions, including a root backup process, can
read all live MIA data and must be treated as fully trusted.

Short default tutor, material-brief, and session-summary instructions are
embedded in the executable. At startup MIA creates each missing instruction file
independently and never overwrites an existing file. Operators may edit these
files; changes require a restart. There is no runtime prompt version, migration,
or automatic replacement mechanism. Git and release history track revisions to
the embedded defaults.

## Directory Structure

```text
<data-dir>/
├── mia.sqlite3
├── mia.lock
├── session.key
├── llm-instructions/
│   ├── init.md
│   └── jobs/
│       ├── tutoring-session-summary.md
│       └── material/
│           ├── text-book.md
│           ├── exam.md
│           ├── website.md
│           ├── worksheet.md
│           └── youtube.md
├── courses/
│   └── <course-id>/
│       └── logo.png
├── materials/
│   └── <material-id>
│       └── files/
│           └── <file-id>/
│               ├── file
│               └── content.jsonl
├── users/
│   └── <user-id>
│       └── avatar.png
└── tts-cache/
    └── <speech-id>.mp3
```

`mia.sqlite3` is MIA's fixed SQLite database. Its name is not an operator
setting.

`mia.lock` is an empty mode-`0600` file used for a non-blocking exclusive process
lock. `mia serve` holds the lock for its lifetime, and database-using local
commands hold it for their complete operation. The file may remain after exit;
only the OS lock state determines ownership.

`session.key` contains 64 random bytes generated on first startup. MIA creates it
with mode `0600`, never logs it, and uses the first 32 bytes for signing and the
remaining 32 bytes for encryption with Echo CookieStore. It is part of
data-directory backup and restore. Replacing or losing it invalidates every
existing browser cookie.

Generated speech in `tts-cache` is retained for the configured number of days
after generation. The default is 30 days. Access does not extend retention.
Expired files are deleted and can be generated again on request when
text-to-speech is available.

Each `<speech-id>.mp3` path is derived directly from its generated-speech row ID.
MIA stores and serves only MP3 (`audio/mpeg`) and keeps no separate storage key.
One file is limited to 25 MiB, signature-validated, written through a mode-`0600`
temporary file, and published by atomic rename.

Cached speech can be reused only while its completed tutor-response content hash
and requested voice match.

Each material-file directory is one managed filesystem unit. `file` is the
validated source upload and `content.jsonl` is its normalized extracted content.
Each line is a strict version-1 segment object with positive contiguous sequence,
nullable chapter and section labels, and text. Encoded JSONL and decoded segment
text are each bounded to 512 MiB per material. MIA writes generated content
atomically and does not retain raw OCR responses, separate content outlines, or
retrieval indexes. Deleting a material file removes the complete directory.
MIA validates a source upload into a temporary file and atomically renames it to
`file` before inserting and committing the database row. Transaction failure
removes the published source, and startup removes a source with no row. The
shared publisher places mode-`0600` temporary files in the destination directory,
syncs completed temporary content before atomic rename, and syncs the destination
directory. Replacement retains a same-filesystem private backup until the related
database transaction commits so a failed audit or database commit can restore the
prior file.
Generated output is fully written and atomically renamed before SQLite marks it
processed or available. A stale or failed database commit removes that output;
startup removes crash-left orphan files.

An avatar has no database metadata row. Its presence is determined by the fixed
`users/<user-id>/avatar.png` path. MIA validates an uploaded image before writing
it, applies orientation, strips metadata, preserves aspect ratio, fits it within
512 by 512 pixels, and re-encodes it as a non-animated PNG. Source JPEG or PNG is
limited to 10 MiB, 40 decoded megapixels, and 10,000 pixels per dimension. MIA
replaces an existing avatar through a temporary file and atomic rename. Startup
reconciliation removes avatar files and user directories whose user no longer
exists.

A course logo likewise has no database metadata row. Its presence is determined
by `courses/<course-id>/logo.png`. It uses the same validated JPEG/PNG input,
limits, orientation, metadata stripping, aspect-preserving resize, atomic
replacement, and PNG output as avatars. Course deletion removes the course
directory, and startup reconciliation removes directories for missing courses.

Startup first deletes expired speech rows and files. It then marks every remaining
speech row stranded in generating state failed with a sanitized restart code and
removes associated incomplete output. Finally, it logs an error and exits if
SQLite references a missing source file, processed `content.jsonl`, or unexpired
available speech file. Missing avatar and logo files are normal and mean no image
is set.
