# bui-go justfile

# Keep in sync with .github/workflows/test-lint.yaml. The lint recipes run exactly this version,
# so a different golangci-lint on PATH cannot give a local result that differs from CI.
golangci_lint_version := "2.15.0"
golangci_lint := "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v" + golangci_lint_version

# Install development dependencies (formatters and linters)
setup-deps:
    #!/usr/bin/env bash
    set -u
    command -v treefmt >/dev/null 2>&1 || { echo "Installing treefmt..."; curl -fsSL https://github.com/numtide/treefmt/releases/download/v2.6.0/treefmt_2.6.0_linux_amd64.tar.gz | sudo tar -C /usr/local/bin -xz treefmt; }
    command -v prettier >/dev/null 2>&1 || { echo "Installing prettier..."; npm install -g prettier@3.9.10; }
    command -v gofumpt >/dev/null 2>&1 || { echo "Installing gofumpt..."; go install mvdan.cc/gofumpt@v0.12.0; }
    command -v gci >/dev/null 2>&1 || { echo "Installing gci..."; go install github.com/daixiang0/gci@v0.14.0; }
    command -v shfmt >/dev/null 2>&1 || { echo "Installing shfmt..."; go install mvdan.cc/sh/v3/cmd/shfmt@v3.14.1; }
    command -v shellcheck >/dev/null 2>&1 || echo "WARNING: shellcheck not found. Install it with your package manager."
    echo "Ensure $(go env GOPATH)/bin is in your PATH"

default: check

# Format code using treefmt
fmt:
    treefmt --allow-missing-formatter

# Run linter
lint:
    {{golangci_lint}} run --config ./.golangci.toml --timeout 5m

# Run linter (with fix)
lint-fix:
    {{golangci_lint}} run --config ./.golangci.toml --timeout 5m --fix

# Format and fix lint issues
fix: lint-fix fmt

# Run tests (windowed tests are skipped without a display)
test:
    xvfb-run -a go test -race ./...

# Run tests on the wasm engine, with cgo and then without
test-wasm:
    xvfb-run -a go test -race -tags bui_nocgo ./...
    CGO_ENABLED=0 xvfb-run -a go test ./...

# Rebuild the wasm module and require identical bytes and the size budget
check-wasm:
    ./scripts/check-wasm.sh

# Run all checks
check: check-formatted check-tidy lint test

# Check that go.mod is tidy
check-tidy:
    ./scripts/error-on-diff.sh go mod tidy

# Check that code is formatted
check-formatted:
    ./scripts/error-on-diff.sh just fmt
