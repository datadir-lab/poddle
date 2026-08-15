# tests/e2e — end-to-end tests

Build-tagged (`//go:build e2e`, run via `task e2e`). Builds the `poddle` binary
and drives it as a black box. Later slices exercise real sandboxes and require
Podman available on the runner.
