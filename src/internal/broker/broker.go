// Package broker is poddle's secretless credential broker: it holds real
// credentials in memory and hands pods only revocable Handles. A pod points its
// harness at the broker's gateway and presents a Handle; the broker injects the
// real credential on the wire — so the secret never lives inside the pod.
package broker

import "time"

// Mode is how a credential authenticates against its vendor.
type Mode string

const (
	// ModeSubscription: an OAuth/subscription token → Authorization: Bearer.
	ModeSubscription Mode = "subscription"
	// ModeAPIKey: a vendor API key → x-api-key.
	ModeAPIKey Mode = "api-key"
	// ModeEndpoint: a local/self-hosted LLM base URL (+ optional key).
	ModeEndpoint Mode = "endpoint"
	// ModeBasic: HTTP Basic auth (git/Forgejo) → Authorization: Basic. Secret
	// holds "user:token".
	ModeBasic Mode = "basic"
	// ModeGoogleAPIKey: a Google AI Studio API key → x-goog-api-key. The pod's
	// gemini-cli presents its handle as Authorization: Bearer (its bearer auth
	// mechanism), which the gateway reads; applyAuth then strips both the Bearer
	// and the SDK's own x-goog-api-key (both carry the handle) and injects the
	// real key as x-goog-api-key, the header Google's endpoint expects.
	ModeGoogleAPIKey Mode = "google-api-key"
	// ModeOAuthBearer: an OAuth 2.1 access token → Authorization: Bearer, refreshed
	// by the gateway using RefreshToken/TokenEndpoint/ClientID before it expires.
	ModeOAuthBearer Mode = "oauth-bearer"
)

// Credential is a real secret plus how/where to use it. It lives ONLY in the
// broker — never in a pod.
type Credential struct {
	Mode    Mode
	Vendor  string // "anthropic" | "openai" | "local" | ...
	Secret  string // OAuth token or API key
	BaseURL string // real upstream, e.g. https://api.anthropic.com

	// OAuth (ModeOAuthBearer only):
	RefreshToken  string
	ExpiresAt     time.Time
	TokenEndpoint string
	ClientID      string
	ClientSecret  string
}

// Handle is the pod-facing capability. The pod only ever sees Value; it is
// opaque, high-entropy, revocable, and worthless off the broker.
type Handle struct {
	Value     string    // opaque, high-entropy
	Tenant    string    // owning tenant (single "local" tenant for now; multi-tenant later)
	CredID    string    // the credential this handle resolves to
	Scope     string    // e.g. the pod name it was issued for
	ExpiresAt time.Time // when the handle auto-expires; zero means never
}
