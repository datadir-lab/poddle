package broker

import (
	"bytes"
	"strings"
	"testing"
)

// allIndexes returns the start offset of every (possibly overlapping)
// occurrence of needle in hay. needle must be non-empty.
func allIndexes(hay, needle []byte) []int {
	var idx []int
	for off := 0; off <= len(hay)-len(needle); {
		i := bytes.Index(hay[off:], needle)
		if i < 0 {
			break
		}
		idx = append(idx, off+i)
		off += i + 1 // +1, not +len(needle): allow overlapping matches
	}
	return idx
}

// FuzzRedactor_NeverEgressesManagedSecret is the core "no secret ever crosses to
// the pod" invariant. The redactor sits on the response path a pod reads, so a
// gap here is a direct secret disclosure — exactly the data-plane risk this
// fuzzing targets.
//
// For any body containing a managed secret:
//   - redact mode: no occurrence of the secret may survive in the output OUTSIDE
//     a placeholder region. An occurrence that overlaps an inserted placeholder
//     is not a leak — its bytes come (at least partly) from the constant
//     placeholder, and the non-placeholder remainder is public text that was
//     never part of the redacted secret. Only an occurrence sitting entirely in
//     un-redacted gap text is real secret material crossing to the pod.
//   - block mode: must set block=true so the caller rejects the request.
//
// (off mode is exempt — interception disabled.)
func FuzzRedactor_NeverEgressesManagedSecret(f *testing.F) {
	f.Add([]byte("Authorization: Bearer sk-ant-secret123"), "sk-ant-secret123")
	f.Add([]byte("prefixSECRETsuffix"), "SECRET")
	f.Add([]byte("SECRETSECRET back to back"), "SECRET")
	f.Add([]byte("overlap SESECRETCRET here"), "SECRET")
	f.Add([]byte("\xbbTT"), "\xbbT") // placeholder-boundary straddle (not a leak)
	f.Add([]byte("no secret present"), "zzz")
	f.Add([]byte(""), "x")
	pb := []byte(RedactPlaceholder)
	f.Fuzz(func(t *testing.T, body []byte, secret string) {
		if secret == "" || !strings.Contains(string(body), secret) {
			return // nothing to redact
		}
		sb := []byte(secret)

		out, _, _ := NewRedactor("redact").Scan(body, secret)
		for _, si := range allIndexes(out, sb) {
			// A secret occurrence [si, si+len(sb)) is a leak unless it overlaps
			// an inserted placeholder [pi, pi+len(pb)).
			overlaps := false
			for _, pi := range allIndexes(out, pb) {
				if si < pi+len(pb) && pi < si+len(sb) {
					overlaps = true
					break
				}
			}
			if !overlaps {
				t.Fatalf("redact mode leaked the managed secret %q in un-redacted text\n  body: %q\n  out:  %q\n  at:   %d", secret, body, out, si)
			}
		}

		if _, _, block := NewRedactor("block").Scan(body, secret); !block {
			t.Fatalf("block mode must reject (block=true) when a managed secret is present\n  body: %q\n  secret: %q", body, secret)
		}
	})
}
