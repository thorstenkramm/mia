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

MIA creates default AI tutor instruction files but never overwrites existing
ones. Operators may edit these files. MIA reads them only at startup, so changes
require a restart.

## Directory Structure

```text
<data-dir>/
├── mia.sqlite3
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
│               └── content.txt
├── users/
│   └── <user-id>
│       └── avatar.png
└── tts-cache/
    └── <speech-id>.mp3
```

`mia.sqlite3` is MIA's fixed SQLite database. Its name is not an operator
setting.

`session.key` contains 64 random bytes generated on first startup. MIA creates it
with mode `0600`, never logs it, and uses the first 32 bytes for signing and the
remaining 32 bytes for encryption with Echo CookieStore. It is part of
data-directory backup and restore. Replacing or losing it invalidates every
existing browser cookie.

Generated speech in `tts-cache` is retained for the configured number of days
after generation. The default is 30 days. Access does not extend retention.
Expired files are deleted and can be generated again on request when
text-to-speech is available.

Cached speech can be reused only while its source chat message and requested
voice still match. A changed source or voice invalidates the cached file.

Each material-file directory is one managed filesystem unit. `file` is the
validated source upload and `content.txt` is its normalized extracted content.
MIA writes generated content atomically and does not retain raw OCR responses,
separate content outlines, or retrieval indexes. Deleting a material file removes
the complete directory.

An avatar has no database metadata row. Its presence is determined by the fixed
`users/<user-id>/avatar.png` path. MIA validates an uploaded image before writing
it and replaces an existing avatar through a temporary file and atomic rename.
Startup reconciliation removes avatar files and user directories whose user no
longer exists.
