package l4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// scramAuthenticator performs the ONE step of a SCRAM exchange that needs the
// real password: turning the server's PBKDF2 parameters (salt, iteration count)
// and the public auth message into the client proof. The SCRAM state machine
// below drives the exchange and parses the untrusted upstream bytes, but calls
// this for the password-bearing arithmetic — so it never holds the password or
// any reusable password-derived key (clientKey/storedKey stay inside Proof).
//
// This is the privilege-separation boundary for Tier 2 (see
// docs/design/broker-privilege-separation.md). Today localSCRAMAuthenticator
// closes over the password in-process; under privsep this same interface becomes
// one RPC to the vault process over the socketpair, and nothing else in the state
// machine changes. Inputs are a small salt, a bounded int, and an opaque message
// the vault only HMACs (never parses); the output is a 32-byte proof.
type scramAuthenticator interface {
	Proof(salt []byte, iter int, authMessage string) ([]byte, error)
}

// localSCRAMAuthenticator holds the real password and computes the proof
// in-process. It is the single-process stand-in for the vault-side authenticator.
type localSCRAMAuthenticator struct{ password string }

// Proof derives the SCRAM ClientProof. It is the only code that touches the
// password. clientKey and storedKey are derived and consumed here and never
// returned, so a caller (the byte-parsing worker, the likely compromise target)
// receives only a proof bound to this one authMessage — useless for replay.
func (l localSCRAMAuthenticator) Proof(salt []byte, iter int, authMessage string) ([]byte, error) {
	// Defensive bound: never trust the caller's iteration count. The worker also
	// bounds it before delegating (finalMessage), but a compromised worker could
	// send an unbounded iter to spin PBKDF2 for minutes — a CPU DoS on the vault.
	// Re-check here so the password-holding side is self-protecting.
	if iter < 1 || iter > maxSCRAMIterations {
		return nil, fmt.Errorf("scram: iteration count %d out of range", iter)
	}
	saltedPassword := pbkdf2SHA256([]byte(l.password), salt, iter)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	clientSig := hmacSHA256(storedKey[:], []byte(authMessage))
	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ clientSig[i]
	}
	return proof, nil
}

// scramClient drives a SCRAM-SHA-256 client exchange (RFC 5802 / 7677). It is
// used by the L4 Postgres broker to authenticate to the real database with the
// real password — the pod never performs SCRAM (it can't; it holds only a
// handle). It routes the password-bearing step through a scramAuthenticator, so
// the state machine itself never holds the password.
type scramClient struct {
	auth        scramAuthenticator
	clientNonce string
	firstBare   string // "n=,r=<clientNonce>"
}

// newSCRAM builds a client that authenticates in-process with password. Postgres
// always passes an empty username (the real username travels in the startup
// packet, not in SCRAM); the RFC 7677 example uses "user".
func newSCRAM(username, password, clientNonce string) *scramClient {
	return newSCRAMWithAuth(localSCRAMAuthenticator{password: password}, username, clientNonce)
}

// newSCRAMWithAuth builds a client whose proof step is delegated to auth. It is
// the privsep-ready constructor: pass a vault-backed authenticator and the state
// machine never sees the password.
func newSCRAMWithAuth(auth scramAuthenticator, username, clientNonce string) *scramClient {
	return &scramClient{
		auth:        auth,
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

	finalNoProof := "c=biws,r=" + combinedNonce
	authMessage := s.firstBare + "," + serverFirst + "," + finalNoProof
	// The password boundary: everything above is parsing of untrusted server bytes
	// and assembling public material; the proof is the only password-dependent
	// step, so delegate it. Under Tier 2 this call crosses to the vault process.
	proof, err := s.auth.Proof(salt, iter, authMessage)
	if err != nil {
		return "", err
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
