# src/cli — CLI + feature slices

`main.go` + `root.go` wire and register the CLI. Each command is a **vertical
slice** in its own subpackage (`up/`, `ls/`, `down/`, `run/`, `attach/`), owning
its cobra command, logic, and co-located unit tests.

Boundary rules (enforced by `tests/architecture`):

- A feature slice must **not** import another feature slice.
- Only the root package imports the slices (to register them).
- Slices may import `src/internal/*`; never the reverse.
