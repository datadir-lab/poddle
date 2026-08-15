# src/internal — shared kernel (private)

Cross-cutting infrastructure used by the CLI slices. Under Go's `internal/`, so
it is importable only from within this module's `src/` tree — never by external
code.

Planned packages:

- `exec/`   — Runner abstraction (real + fake) over `os/exec`
- `podman/` — Podman provider (create / list / exec / remove)
- `config/` — `.poddle.toml` and user-config parsing
