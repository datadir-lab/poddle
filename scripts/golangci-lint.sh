#!/bin/sh
# Lint Go before pushing, if golangci-lint is installed locally (CI always lints).
if command -v golangci-lint >/dev/null 2>&1; then
	# --allow-parallel-runners: pushing to two remotes runs this hook twice; do
	# not fail on the lock or a stale lock from an interrupted run.
	exec golangci-lint run --allow-parallel-runners ./src/...
fi
echo "golangci-lint not installed; skipping (CI lints). See https://golangci-lint.run"
