#!/bin/sh
# Lint Go before pushing, if golangci-lint is installed locally (CI always lints).
if command -v golangci-lint >/dev/null 2>&1; then
	exec golangci-lint run ./src/...
fi
echo "golangci-lint not installed; skipping (CI lints). See https://golangci-lint.run"
