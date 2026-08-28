# Server Configuration

This document is the normative reference for configuring the `mia` server. MIA validates the complete effective
configuration before serving requests. A validation or required-service connectivity failure is written to standard error
and terminates startup with exit code 1.

Audience: operators and implementers. This document is authoritative for server
settings.

See [`mia.example.toml`](../mia.example.toml) for a complete annotated
configuration file.

## Configuration Sources

MIA reads configuration from the following sources, from lowest to highest precedence:

1. Built-in defaults
2. TOML configuration file
3. Environment variables
4. Command-line flags

An explicitly configured empty string is a value. It never reveals a lower-precedence value and is invalid unless a setting
explicitly permits empty.

Unknown TOML tables and keys are startup errors. MIA reports the offending key without including secret values.

### Configuration File

MIA reads `/etc/mia/mia.toml` by default. The default file is optional, so a complete configuration may instead come from
environment variables and command-line flags.

`--config <path>` selects one alternative file. MIA uses that exact path without searching fallback locations. A path selected
with `--config` must exist.

The file may be a symbolic link. Its resolved target must meet all of these requirements:

- It is a regular file.
- It is owned by root or by the effective service user.
- Its owner can read it.
- Owner write and group read are permitted.
- Owner execute, group write or execute, and every permission for others are rejected.

Accepted modes include `0400`, `0440`, `0600`, and `0640`.

### Override Names

Environment variables and non-secret command-line flags derive mechanically from fully qualified TOML keys:

- `http.listen` becomes `MIA_HTTP_LISTEN` and `--http-listen`.
- Environment variables replace dots with underscores and use uppercase.
- Flags replace dots and underscores with hyphens and use lowercase.
- `--config` is the only naming exception.

Secret settings have environment variables but no command-line flags. This prevents secrets from appearing in process lists
or shell history.

TOML uses arrays for list settings. Environment variables and flags use comma-separated values. MIA ignores surrounding
whitespace on each element and rejects empty elements.

### Secrets and Diagnostics

API keys, passwords, and other service credentials may be provided in TOML or environment variables. MIA redacts them from
logs, errors, diagnostics, and any effective-configuration output.

## General Validation

All configured filesystem paths must be absolute. MIA does not resolve relative paths against the configuration file or the
process working directory.

After resolving symbolic links, `main.data_dir` and `main.doc_root` must be disjoint directory trees. They cannot be equal,
and neither can contain the other.

Operational durations that may be introduced later use Go duration strings such as `"30s"` and `"2m"`. Retention expressed
in whole days uses integers.

MIA reads and validates configuration only at startup. Every configuration change requires a process restart. `SIGHUP` reopens
the configured log file but does not reload configuration.

## Settings

### `[main]`

The main table defines installation paths and MIA's externally visible origin.

#### `main.data_dir`

- Type: string
- Status: mandatory
- Default: none
- Environment: `MIA_MAIN_DATA_DIR`
- Flag: `--main-data-dir`

The absolute base directory for SQLite data, uploaded files, generated files, and MIA-managed instructions. It must already
exist and be writable by the service user. MIA creates documented internal subdirectories when they are missing.

#### `main.doc_root`

- Type: string
- Status: mandatory
- Default: none
- Environment: `MIA_MAIN_DOC_ROOT`
- Flag: `--main-doc-root`

The absolute directory containing the separately installed frontend. It must already exist and be readable by the service
user. Private data must never be placed beneath this directory.

#### `main.public_url`

- Type: string
- Status: mandatory
- Default: none
- Environment: `MIA_MAIN_PUBLIC_URL`
- Flag: `--main-public-url`

The externally visible origin used for invitation and password-recovery links, for example `https://mia.example.org`. It
must use HTTPS and contain no credentials, non-root path, query, or fragment. A trailing root slash is accepted and
normalized. MIA never derives security-sensitive links from request or forwarded headers.

### `[http]`

The HTTP table defines the local listener and reverse proxies trusted to report client addresses. HTTPS terminates at an
operator-managed reverse proxy; MIA does not expose TLS certificate settings.

The reverse proxy must pass `/api/v1/tutor-responses/{id}/events` responses
incrementally and disable response buffering for that Server-Sent Events route.

#### `http.listen`

- Type: string
- Status: optional
- Default: `"127.0.0.1:9900"`
- Environment: `MIA_HTTP_LISTEN`
- Flag: `--http-listen`

A TCP endpoint such as `127.0.0.1:9900` or `[::1]:9900`, or a Unix endpoint such as
`unix:/run/mia/mia.sock`.

For a Unix endpoint, the parent directory must already exist and be writable by the service user. MIA creates the socket
with mode `0660`, owned by the effective service user and the selected socket group.

If the path exists, MIA fails when an active listener uses it. MIA removes only an inactive socket owned by root or the
effective service user. A foreign-owned socket or an existing path of another file type is a startup error.

#### `http.trusted_proxy_cidrs`

- Type: array of strings
- Status: optional
- Default: empty
- Environment: `MIA_HTTP_TRUSTED_PROXY_CIDRS`
- Flag: `--http-trusted-proxy-cidrs`

Additional proxy IP networks whose forwarded client-address headers MIA trusts. Loopback networks are always trusted and
are not removed by this list. MIA ignores forwarded headers from every other peer.

Each value must use CIDR notation, for example `"10.0.0.0/8"` or `"2001:db8::/32"`.

#### `http.socket_group`

- Type: string
- Status: optional
- Default: service user's primary group
- Environment: `MIA_HTTP_SOCKET_GROUP`
- Flag: `--http-socket-group`

An existing group that owns a Unix listener so the reverse proxy can connect. This setting is invalid when
`http.listen` is a TCP endpoint.

### `[log]`

MIA never logs passwords, API keys, tokens, MFA values, recovery codes, private prompts, message bodies, or provider payloads.

#### `log.file`

- Type: string
- Status: optional
- Default: none; logs are written to standard error
- Environment: `MIA_LOG_FILE`
- Flag: `--log-file`

An absolute log-file path. Its parent directory must already exist and be writable by the service user. MIA creates or appends
to the file with mode `0640`.

External tooling owns rotation and retention. Sending `SIGHUP` makes MIA close and reopen this file.

#### `log.level`

- Type: string
- Status: optional
- Default: `"info"`
- Environment: `MIA_LOG_LEVEL`
- Flag: `--log-level`

The minimum emitted level. Valid values are `debug`, `info`, `warn`, and `error`.

#### `log.format`

- Type: string
- Status: optional
- Default: `"json"`
- Environment: `MIA_LOG_FORMAT`
- Flag: `--log-format`

The log encoding. Valid values are `json` for newline-delimited JSON and `text` for human-readable output.

### `[openai]`

OpenAI is required for tutoring and model-driven background jobs. Startup performs a bounded live credential, connectivity,
and model-capability check. MIA does not start while that check fails.

#### `openai.api_key`

- Type: string
- Status: mandatory, secret
- Default: none
- Environment: `MIA_OPENAI_API_KEY`
- Flag: none

The OpenAI API key used by MIA.

#### `openai.chat_model`

- Type: string
- Status: optional
- Default: `"gpt-5.6-terra"`
- Environment: `MIA_OPENAI_CHAT_MODEL`
- Flag: `--openai-chat-model`

The model used for tutoring sessions. It must support the Responses API, function calling, and every capability required
by the tutoring workflow.

#### `openai.job_model`

- Type: string
- Status: optional
- Default: `"gpt-5.6-terra"`
- Environment: `MIA_OPENAI_JOB_MODEL`
- Flag: `--openai-job-model`

The model used for model-driven background jobs. It must support the Responses API and every capability required by those
jobs.

### `[mistral]`

Mistral is required for OCR. Startup performs a bounded live credential and connectivity check. The supported OCR model is
fixed by the MIA release and is not configurable.

#### `mistral.api_key`

- Type: string
- Status: mandatory, secret
- Default: none
- Environment: `MIA_MISTRAL_API_KEY`
- Flag: none

The Mistral API key used by MIA.

### `[smtp]`

SMTP is required for email. Startup performs a bounded live connectivity and credential check without sending an email.

#### `smtp.host`

- Type: string
- Status: mandatory
- Default: none
- Environment: `MIA_SMTP_HOST`
- Flag: `--smtp-host`

The SMTP server hostname or IP address.

#### `smtp.port`

- Type: integer
- Status: optional
- Default: `587`
- Environment: `MIA_SMTP_PORT`
- Flag: `--smtp-port`

The SMTP server port, from 1 through 65535.

#### `smtp.transport`

- Type: string
- Status: optional
- Default: `"starttls"`
- Environment: `MIA_SMTP_TRANSPORT`
- Flag: `--smtp-transport`

The SMTP transport mode. Valid values are `starttls`, `implicit_tls`, and `plaintext`. STARTTLS and implicit TLS always verify
the server certificate using the system trust store. Certificate verification cannot be disabled.

> [!WARNING]
> `plaintext` exposes message content and, when configured, SMTP credentials in transit. Use it only when the operator
> deliberately accepts that risk.

#### `smtp.username`

- Type: string
- Status: optional credential
- Default: none
- Environment: `MIA_SMTP_USERNAME`
- Flag: none

The SMTP authentication username. `username` and `password` must either both be absent or both be configured.

#### `smtp.password`

- Type: string
- Status: optional, secret
- Default: none
- Environment: `MIA_SMTP_PASSWORD`
- Flag: none

The SMTP authentication password. `username` and `password` must either both be absent or both be configured.

#### `smtp.sender_email`

- Type: string
- Status: mandatory
- Default: none
- Environment: `MIA_SMTP_SENDER_EMAIL`
- Flag: `--smtp-sender-email`

The valid email address used in the message `From` header.

#### `smtp.sender_name`

- Type: string
- Status: optional
- Default: none
- Environment: `MIA_SMTP_SENDER_NAME`
- Flag: `--smtp-sender-name`

The display name used in the message `From` header. When absent, messages use only `sender_email`.

### `[clicksend]`

ClickSend is optional. An absent table disables SMS features. If any key is present, the complete mandatory configuration
must be valid and startup performs a bounded live credential and connectivity check.

#### `clicksend.username`

- Type: string
- Status: mandatory when the table is present
- Default: none
- Environment: `MIA_CLICKSEND_USERNAME`
- Flag: none

The ClickSend account username used with the API key for HTTP Basic authentication.

#### `clicksend.api_key`

- Type: string
- Status: mandatory when the table is present, secret
- Default: none
- Environment: `MIA_CLICKSEND_API_KEY`
- Flag: none

The ClickSend API key. MIA does not call this value a password even though some ClickSend SDKs place it in a password field.

#### `clicksend.sender_id`

- Type: string
- Status: optional
- Default: none; ClickSend assigns the sender
- Environment: `MIA_CLICKSEND_SENDER_ID`
- Flag: `--clicksend-sender-id`

A ClickSend-supported alpha tag or sending number.

### `[eleven_labs]`

ElevenLabs is optional. An absent table disables text-to-speech. If any key is present, the complete mandatory configuration
must be valid and startup performs a bounded live credential and connectivity check.

#### `eleven_labs.api_key`

- Type: string
- Status: mandatory when the table is present, secret
- Default: none
- Environment: `MIA_ELEVEN_LABS_API_KEY`
- Flag: none

The ElevenLabs API key used by MIA.

#### `eleven_labs.cache_retention_days`

- Type: integer
- Status: optional
- Default: `30`
- Environment: `MIA_ELEVEN_LABS_CACHE_RETENTION_DAYS`
- Flag: `--eleven-labs-cache-retention-days`

The number of whole days generated speech is retained after generation. Valid values are 1 through 365. Access does not extend
retention.

### `[uploads]`

Upload limits may be lowered or raised only within MIA's fixed safety bounds. These settings do not change accepted file
formats or permit unbounded uploads. One MiB is 1,048,576 bytes.

#### `uploads.max_file_size_mib`

- Type: integer
- Status: optional
- Default: `100`
- Environment: `MIA_UPLOADS_MAX_FILE_SIZE_MIB`
- Flag: `--uploads-max-file-size-mib`

The maximum size of one uploaded file. Valid values are 1 through 512.

#### `uploads.max_material_size_mib`

- Type: integer
- Status: optional
- Default: `200`
- Environment: `MIA_UPLOADS_MAX_MATERIAL_SIZE_MIB`
- Flag: `--uploads-max-material-size-mib`

The maximum combined size of all files in one material. Valid values are 1 through 512. It must be greater than or equal
to `max_file_size_mib`.

#### `uploads.max_material_pages`

- Type: integer
- Status: optional
- Default: `1000`
- Environment: `MIA_UPLOADS_MAX_MATERIAL_PAGES`
- Flag: `--uploads-max-material-pages`

The maximum combined page count of one material. Valid values are 1 through 2000.

## Deliberately Fixed Behavior

The initial configuration surface stays small. MIA provides no TOML settings for:

- API rate limits, limiter state bounds, or limiter eviction
- HTTP read, write, idle, or graceful-shutdown timeouts
- External-provider operation timeouts, retries, or backoff
- Non-upload API request-body limits
- Password, session, invitation, MFA, or SMS security policies
- Safeguarding guidance
- The SQLite database path or storage engine
- The Mistral OCR model
- Accepted upload formats or upload hard maxima

These values and policies are fixed by MIA. Settings may be added later when a concrete installation requirement justifies
them.
