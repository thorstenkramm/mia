# TOML Configuration Rules

Use this document as the reference for writing mia TOML configuration files.
[Viper](https://github.com/spf13/viper) will parse the file.

Format every changed TOML file with `npx @taplo/cli fmt <FILE>.toml`. Then validate it with
`npx @taplo/cli check <FILE>.toml` and `npx @taplo/cli fmt --check <FILE>.toml`. Taplo reads the repository formatting rules
from `.taplo.toml`.

## File Structure

- Use top-level tables for configuration domains (for example: `[main]`, `[log]`, `[entraid]`).
- Keep keys grouped under their table; do not duplicate keys across tables.
- Indent settings and their comments by two spaces beneath each top-level table header.
- Use comments (`##` or `#`) to document each setting, including defaults and examples.

## Key and Value Conventions

- Use `snake_case` for keys (for example: `data_dir`, `client_id`).
- Use `=` for assignment. Do not use `:`.
- Strings must be quoted with double quotes.
- Integers should be unquoted (for example: `port = 9900`).
- Lists and nested tables are allowed if introduced later; keep them consistent with TOML syntax.

## Comments and Defaults

- Document each setting with a short description and include:
  - Whether it is mandatory or optional.
  - The override flag and environment variable, if applicable.
  - An example value.
  - The default value (or `none` if there is no default).
- Use consistent phrasing across sections.
- Keep comments ASCII-only and avoid fancy punctuation.

## Required vs. Optional Fields

- Mark required settings as `mandatory` in their comments.
- Mark optional settings as `optional` in their comments.
- If a value is required, the example must show a valid value. Do not leave it empty.

## Example (Style Only)

```toml
[main]
  ## Server listen address, string, optional
  ## Can be overridden with --listen flag or MIA_MAIN_LISTEN environment variable
  ## Example: "0.0.0.0"
  ## Default: "127.0.0.1"
  #listen = "127.0.0.1"
```

## Common Pitfalls

- Do not mix `:` and `=` assignment syntax.
- Do not use unquoted strings.
- Do not leave dangling keys with no value.
- Do not introduce new tables without updating documentation and examples.

## Naming and Branding

- Use "mia" for the product name.
- Keep URLs and examples consistent with existing sample files if we have some.
