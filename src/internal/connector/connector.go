// Package connector brokers a pod's access to services (git host, CI, …) with
// the same secretless trick as the LLM: the pod gets a revocable handle, the
// broker holds the real token and injects it on the wire. A connector is
// DECLARATIVE — a Definition (auth mode + pod-wiring templates) — so a new
// service is a few lines of TOML, no code. forgejo and woodpecker ship built-in.
//
// A Connection is a named, owner-scoped instance of a connector (my Forgejo
// token ≠ someone else's), stored like an identity.
package connector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/datadir-lab/poddle/src/internal/broker"
)

// Definition is a declarative connector type. Env/Setup values use the
// placeholders {broker} (broker host:port), {handle}, and {base_url}.
type Definition struct {
	Mode    string            `toml:"mode"`     // bearer | basic | api-key | endpoint
	BaseURL string            `toml:"base_url"` // default upstream; a connection may override
	Env     map[string]string `toml:"env"`      // pod env
	Setup   []string          `toml:"setup"`    // pod setup commands
}

// gitRewrite routes any {base_url} git op through the broker with the handle as
// the Basic username. The broker swaps it for the real user:token (pod→broker is
// HTTP; broker→host may be HTTPS, e.g. github/gitlab — no TLS interception).
const gitRewrite = `git config --global url."http://{handle}:x@{broker}/".insteadOf "{base_url}/"`

// builtins are the connector definitions poddle ships.
var builtins = map[string]Definition{
	"forgejo": {Mode: "basic", Setup: []string{gitRewrite}}, // self-hosted → base_url per connection
	"github":  {Mode: "basic", BaseURL: "https://github.com", Setup: []string{gitRewrite}},
	"gitlab":  {Mode: "basic", BaseURL: "https://gitlab.com", Setup: []string{gitRewrite}},
	"npm": {
		Mode:    "bearer",
		BaseURL: "https://registry.npmjs.org",
		Setup: []string{
			`npm config set registry http://{broker}/`,
			`npm config set //{broker}/:_authToken {handle}`,
		},
	},
	"woodpecker": {
		Mode: "bearer",
		Env:  map[string]string{"WOODPECKER_SERVER": "http://{broker}", "WOODPECKER_TOKEN": "{handle}"},
	},
}

// LoadDefinition returns the definition for name: a user file in userDir
// (userDir/<name>.toml) overrides the built-in of the same name.
func LoadDefinition(userDir, name string) (Definition, error) {
	if b, err := os.ReadFile(filepath.Join(userDir, name+".toml")); err == nil {
		var d Definition
		if err := toml.Unmarshal(b, &d); err != nil {
			return Definition{}, fmt.Errorf("parse connector %q: %w", name, err)
		}
		return d, nil
	}
	if d, ok := builtins[name]; ok {
		return d, nil
	}
	return Definition{}, fmt.Errorf("unknown connector %q", name)
}

// brokerMode maps a connector-friendly mode name to a broker inject mode.
func brokerMode(m string) broker.Mode {
	switch m {
	case "bearer":
		return broker.ModeSubscription
	case "basic":
		return broker.ModeBasic
	case "api-key":
		return broker.ModeAPIKey
	case "endpoint":
		return broker.ModeEndpoint
	default:
		return broker.Mode(m)
	}
}

// Credential builds the broker credential for a connection using its definition.
// For basic auth the secret is "user:token"; otherwise it's the token.
func Credential(conn Connection, def Definition) (broker.Credential, error) {
	b, err := os.ReadFile(conn.tokenPath())
	if err != nil {
		return broker.Credential{}, fmt.Errorf("read %s token: %w", conn.Connector, err)
	}
	token := strings.TrimSpace(string(b))
	secret := token
	if def.Mode == "basic" {
		secret = conn.User + ":" + token
	}
	baseURL := conn.BaseURL // a connection's URL overrides the definition default
	if baseURL == "" {
		baseURL = def.BaseURL
	}
	return broker.Credential{
		Mode:    brokerMode(def.Mode),
		Vendor:  conn.Connector,
		Secret:  secret,
		BaseURL: baseURL,
	}, nil
}

// Wiring fills the definition's env/setup templates for the pod, routing the
// service through the broker at brokerAddr (host:port) with handle.
func Wiring(def Definition, cred broker.Credential, brokerAddr, handle string) (map[string]string, []string) {
	repl := strings.NewReplacer("{broker}", brokerAddr, "{handle}", handle, "{base_url}", cred.BaseURL)
	var env map[string]string
	if len(def.Env) > 0 {
		env = make(map[string]string, len(def.Env))
		for k, v := range def.Env {
			env[k] = repl.Replace(v)
		}
	}
	var setup []string
	for _, s := range def.Setup {
		setup = append(setup, repl.Replace(s))
	}
	return env, setup
}
