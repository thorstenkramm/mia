# Documentation

MIA is currently in product, API, and implementation planning. This repository
does not yet contain a runnable server.

## Start Here

- [Public overview](../README.md) explains the product, roles, external services,
  and deployment model.
- [Product requirements](product-requirements.md) is the authoritative contract
  for confirmed user-visible behavior and security boundaries.

## Operators

- [Server configuration](server-configuration.md) is the normative setting
  reference.
- [`mia.example.toml`](../mia.example.toml) is the complete annotated example.
- [Data directory](data-dir.md) defines the filesystem layout and operator-owned
  storage requirements.

Installation, service management, backup, restore, upgrade, and troubleshooting
guides will be added when the corresponding implementation exists.

## Developers

- [API design](api.md) defines the current route layout and open transport
  decisions.
- [Database layout](database-layout.md) defines the current logical SQLite
  design.
- [Background jobs](jobs.md) defines the current asynchronous processing design.
- [Tutoring sessions](tutoring-sessions.md) explains AI context construction,
  material retrieval, and tutoring boundaries.

## Authority

When documents differ, use this order:

1. [Product requirements](product-requirements.md) for product behavior.
2. [Server configuration](server-configuration.md) for operator settings.
3. [API design](api.md) for the current HTTP route layout.
4. The relevant current developer design for other implementation details.

Silence is not a product decision. Current design documents may be extended as
implementation questions are resolved, but superseded text should be removed
rather than preserved as history.
