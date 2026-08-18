package dashboard

import "embed"

// bundle is the built dashboard UI, embedded into the binary so `poddle
// dashboard` needs no external assets. Task 5 replaces dist/ with the Preact/Vite
// build (`task dashboard-build`); until then it holds a minimal vanilla page that
// exercises the same /v1/audit* contract.
//
//go:embed dist
var bundle embed.FS
