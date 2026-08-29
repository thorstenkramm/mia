# Techstack of dendrite-pulse

- Language: Golang Version 1.27.0
- Webframework: [Echo Framework](https://echo.labstack.com/) Version 5.3.1
- Linting: `golangci-lint` version 2.12.2
- VCS: `git` and [GitHub](https://github.com/thorstenkramm/mia)
- CI/CD: GitHub Actions
- Database: `modernc.org/sqlite` with WAL and `synchronous=FULL`

## Mandatory Go modules and packages

- Use [cobra](https://github.com/spf13/cobra) to handle command line arguments and flags.
- Use [viper](https://github.com/spf13/viper) to handle configuration files.
- Use structured logging using standard library logging package [log/slog](https://pkg.go.dev/log/slog) and explicit
  error wrapping to preserve call context.

## Persistence

For data storage SQLite3 is used with [Write-Ahead Logging](https://sqlite.org/wal.html) enabled.
Support for other databases is not desired.
The application must handle all database migrations using [golang-migrate](https://github.com/golang-migrate/migrate).

## Other rules

- Use Go's built-in concurrency features when beneficial for API performance.

## Docker & k8s

As go creates self-contained and easy to deploy single-file binaries, containerized deployments are not planned.
Also, instructions on how to develop having the go compiler inside a container are not provided.
All documentation assumes you have the `go` command line utility installed directly to your OS.

## Supported platforms

Developer instructions regarding the development environment are given only for Linux and macOS.
Binary releases are built and published by GitHub Actions only for Linux AMD64.
Microsoft Windows is completely out of scope.
