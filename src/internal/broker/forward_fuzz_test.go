package broker

import (
	"encoding/base64"
	"testing"
)

// FuzzProxyAuthToken feeds arbitrary Proxy-Authorization header values — fully
// pod-controlled — to the token parser. It must never panic (a crash in the
// forward proxy's request path is a DoS on the sole egress), and a garbage
// header must yield an empty token (which Check then denies), never a token
// that could impersonate another pod.
func FuzzProxyAuthToken(f *testing.F) {
	f.Add("Basic " + base64.StdEncoding.EncodeToString([]byte("poddle_egr_abc:x")))
	f.Add("Basic " + base64.StdEncoding.EncodeToString([]byte("nocolon")))
	f.Add("Basic notbase64!!")
	f.Add("Bearer sometoken")
	f.Add("Basic ")
	f.Add("")
	f.Fuzz(func(t *testing.T, header string) {
		_ = proxyAuthToken(header) // must not panic on any header value
	})
}
