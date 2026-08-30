package broker

import (
	"bytes"
	"regexp"
)

// RedactPlaceholder replaces any secret found in an outbound body.
const RedactPlaceholder = "«redacted:poddle»"

// defaultPatterns are high-confidence secret shapes scrubbed from egress. They
// are deliberately narrow to keep false positives near zero.
var defaultPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`), // PEM private keys
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                      // AWS access key id
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),            // GitHub tokens
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),          // Slack tokens
}

// Redactor scrubs secrets from an outbound request body: the broker's own
// managed secrets (exact, zero false positives) plus a small high-confidence
// pattern set. mode is "redact" (default), "block", or "off".
type Redactor struct {
	mode     string
	patterns []*regexp.Regexp
}

// NewRedactor returns a Redactor; an empty mode defaults to "redact".
func NewRedactor(mode string) *Redactor {
	if mode == "" {
		mode = "redact"
	}
	return &Redactor{mode: mode, patterns: defaultPatterns}
}

// Scan redacts managed secrets and known patterns from body. It returns the
// (possibly rewritten) body, the number of secrets found, and whether the
// request should be blocked. In "block" mode a hit returns the ORIGINAL body
// with block=true so the caller can reject; in "off" mode the body is untouched.
func (r *Redactor) Scan(body []byte, managed ...string) (out []byte, hits int, block bool) {
	if r == nil || r.mode == "off" || len(body) == 0 {
		return body, 0, false
	}
	out, hits = scrubExact(body, managed)
	for _, re := range r.patterns {
		if !re.Match(out) {
			continue
		}
		out = re.ReplaceAllFunc(out, func(m []byte) []byte {
			hits++
			return []byte(RedactPlaceholder)
		})
	}
	if hits > 0 && r.mode == "block" {
		return body, hits, true
	}
	return out, hits, false
}

// scrubExact replaces every exact occurrence of each secret in body with
// RedactPlaceholder, returning the result and the total number of occurrences
// replaced. Empty secrets are skipped. It is the zero-false-positive core shared
// by outbound request redaction (Redactor.Scan, alongside its pattern set) and
// response reflection scrubbing (localKeeper.RedactResponse, exact-match only).
func scrubExact(body []byte, secrets []string) (out []byte, hits int) {
	out = body
	for _, s := range secrets {
		if s == "" {
			continue
		}
		if c := bytes.Count(out, []byte(s)); c > 0 {
			hits += c
			out = bytes.ReplaceAll(out, []byte(s), []byte(RedactPlaceholder))
		}
	}
	return out, hits
}
