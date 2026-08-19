# tests/architecture - structural rules

Black-box tests that load the `src/` import graph (via `go list`) and fail CI
when vertical-slice boundaries are violated:

- no feature-slice → feature-slice imports,
- no `src/internal` → `src/cli` imports (the kernel has no upward deps).

These enforce the slicing that Go's `internal/` alone cannot.
