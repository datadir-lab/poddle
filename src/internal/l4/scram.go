package l4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// scramClient drives a SCRAM-SHA-256 client exchange (RFC 5802 / 7677). It is
// used by the L4 Postgres broker to authenticate to the real database with the
// real password — the pod never performs SCRAM (it can't; it holds only a
// handle).
type scramClient struct {
	password    string
	clientNonce string
	firstBare   string // "n=,r=<clientNonce>"
}

// newSCRAM builds a client. Postgres always passes an empty username (the real
// username travels in the startup packet, not in SCRAM); the RFC 7677 example
// uses "user".
func newSCRAM(username, password, clientNonce string) *scramClient {
	return &scramClient{
		password:    password,
		clientNonce: clientNonce,
		firstBare:   "n=" + saslName(username) + ",r=" + clientNonce,
	}
}

// saslName escapes '=' and ',' per RFC 5802.
func saslName(s string) string {
	s = strings.ReplaceAll(s, "=", "=3D")
	return strings.ReplaceAll(s, ",", "=2C")
}

// firstMessage is the SASLInitialResponse payload (gs2 header + client-first-bare).
func (s *scramClient) firstMessage() string { return "n,," + s.firstBare }

// maxSCRAMIterations caps the server-supplied PBKDF2 iteration count. Real
// servers use a few thousand (Postgres defaults to 4096); an unbounded count
// from a hostile or MITM'd upstream would spin pbkdf2SHA256 for minutes — a CPU
// DoS. 1<<20 is far above any real server yet bounds the loop to milliseconds.
const maxSCRAMIterations = 1 << 20

// finalMessage computes the client-final message (with proof) from the server's
// server-first message.
func (s *scramClient) finalMessage(serverFirst string) (string, error) {
	attrs := parseSCRAM(serverFirst)
	combinedNonce := attrs["r"]
	if !strings.HasPrefix(combinedNonce, s.clientNonce) {
		return "", fmt.Errorf("scram: server nonce does not extend the client nonce")
	}
	salt, err := base64.StdEncoding.DecodeString(attrs["s"])
	if err != nil {
		return "", fmt.Errorf("scram: bad salt: %w", err)
	}
	iter, err := strconv.Atoi(attrs["i"])
	if err != nil || iter < 1 || iter > maxSCRAMIterations {
		return "", fmt.Errorf("scram: bad iteration count %q", attrs["i"])
	}

	saltedPassword := pbkdf2SHA256([]byte(s.password), salt, iter)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)

	finalNoProof := "c=biws,r=" + combinedNonce
	authMessage := s.firstBare + "," + serverFirst + "," + finalNoProof
	clientSig := hmacSHA256(storedKey[:], []byte(authMessage))

	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ clientSig[i]
	}
	return finalNoProof + ",p=" + base64.StdEncoding.EncodeToString(proof), nil
}

// parseSCRAM splits a comma-separated "k=v" SCRAM message into a map. Values may
// contain '=' (e.g. base64), so only the first '=' delimits.
func parseSCRAM(msg string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(msg, ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out
}

func hmacSHA256(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}

// pbkdf2SHA256 derives a 32-byte key. SCRAM-SHA-256 uses dkLen == hLen == 32, so
// a single PBKDF2 block suffices (no dependency needed).
func pbkdf2SHA256(password, salt []byte, iter int) []byte {
	block := append(append([]byte{}, salt...), 0, 0, 0, 1) // salt || INT(1)
	u := hmacSHA256(password, block)
	out := make([]byte, len(u))
	copy(out, u)
	for i := 1; i < iter; i++ {
		u = hmacSHA256(password, u)
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}
