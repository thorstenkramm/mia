#!/usr/bin/env bash

set -euo pipefail

go_files=()
while IFS= read -r file; do
  go_files+=("$file")
done < <(git ls-files --cached --others --exclude-standard -- '*.go')

echo "Checking gofmt ..."
unformatted="$(gofmt -l "${go_files[@]}")"
if [[ -n "$unformatted" ]]; then
  echo "Unformatted Go files:"
  echo "$unformatted"
  exit 1
fi

echo "Running Go tests ..."
go test ./...

echo "Running go vet ..."
go vet ./...

echo "Running golangci-lint ..."
golangci-lint run ./...

echo "Running Go race tests ..."
go test -race ./...

echo "Running code duplication search ..."
npx --yes jscpd@5.1.2 --min-lines 10 --min-tokens 50 --threshold 0 \
  --reporters console --no-tips \
  --ignore "**/*_test.go,**/vendor/**,**/_bmad/**,**/.cache/**,**/_bmad-output/**,**/.agents/**" \
  --format go,markup .

if command -v govulncheck >/dev/null 2>&1; then
  echo "Running Go vulnerability scan ..."
  govulncheck ./...
fi

echo "Running Trivy vulnerability scan ..."
trivy fs . --skip-dirs .agents --ignorefile .trivyignore.yaml

echo "Running Redocly API documentation lint ..."
npx --yes @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml

markdown_files=()
while IFS= read -r file; do
  markdown_files+=("$file")
done < <(git ls-files --cached --others --exclude-standard -- '*.md' \
  ':(exclude)_bmad/**' ':(exclude)_bmad-output/**' ':(exclude).agents/skills/**' \
  ':(exclude).opencode/**' ':(exclude).cache/**' ':(exclude)vendor/**')

echo "Running Markdown lint ..."
npx --yes markdownlint-cli@0.49.1 "${markdown_files[@]}"

echo "🥳🥳 All tests passed. 🎉🎉 "
