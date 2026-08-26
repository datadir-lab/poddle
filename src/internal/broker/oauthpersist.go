package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// connMirror is the on-disk mirror shape for a connection's rotated OAuth
// material. Its json tags MUST match connector.OAuthMaterial byte-for-byte —
// this is a deliberate duplicate, not a shared type: the broker package
// cannot import connector (connector imports broker), so there is no single
// definition to share. See TestConnMirror_JSONTagsMatchOAuthMaterial, which
// pins this invariant so a future tag drift on either side is caught here.
type connMirror struct {
	AccessToken   string `json:"access"`
	RefreshToken  string `json:"refresh"`
	TokenEndpoint string `json:"token_endpoint"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	Scope         string `json:"scope"`

	ExpiresAt time.Time `json:"expires_at"`
	RotatedAt time.Time `json:"rotated_at"`
}

// OAuthPersister durably writes a connection's rotated OAuth material to
// disk so it survives a poddled restart. Implementations must be safe to
// call from concurrent goroutines (a later task wires this into the
// gateway's refresh path).
type OAuthPersister interface {
	Persist(connName string, m connMirror) error
}

// stateDirPersister is an OAuthPersister that mirrors each connection's
// rotated OAuth material as its own <connName>.json file under dir.
type stateDirPersister struct {
	dir string
}

// NewStateDirPersister returns an OAuthPersister that writes
// <dir>/<connName>.json for each Persist call.
func NewStateDirPersister(dir string) OAuthPersister {
	return &stateDirPersister{dir: dir}
}

// Persist writes m as <dir>/<connName>.json, atomically and mode 0600. It
// rejects a connName that isn't a bare filename (no path separators, no
// ".."), refusing to write anything for such input — this is the only
// guard between a connector-controlled name and the mirror directory, so it
// stays strict rather than merely best-effort.
func (p *stateDirPersister) Persist(connName string, m connMirror) error {
	if err := validConnName(connName); err != nil {
		return err
	}
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return fmt.Errorf("oauthpersist: create state dir: %w", err)
	}

	//nolint:gosec // G117: marshaling rotated OAuth material to disk is the design — this is the durable rotated-token mirror, written 0600, same posture as connections/oauth.json (connector.Store.SaveOAuth); the secretless invariant keeps it off the pod, not off the host.
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("oauthpersist: marshal: %w", err)
	}

	final := filepath.Join(p.dir, connName+".json")
	return writeAtomic(p.dir, connName, final, b)
}

// validConnName rejects any connName that is not a bare filename component:
// no path separators, and no "..". filepath.Base only treats "/" as a
// separator on Linux — poddle's CI and deployment OS — so a bare
// filepath.Base check lets a "\"-containing connName like "a\b" slip
// through there. We explicitly reject both "/" and "\" via
// strings.ContainsAny on every platform, in addition to the
// filepath.Base check, so the guard doesn't depend on the host OS's
// separator convention.
func validConnName(connName string) error {
	if connName == "" {
		return errors.New("oauthpersist: empty connection name")
	}
	if connName == "." || connName == ".." || strings.Contains(connName, "..") {
		return errors.New("oauthpersist: invalid connection name")
	}
	if strings.ContainsAny(connName, "/\\") {
		return errors.New("oauthpersist: invalid connection name")
	}
	if filepath.Base(connName) != connName {
		return errors.New("oauthpersist: invalid connection name")
	}
	return nil
}

// writeAtomic writes b to final by first writing a temp file in dir (same
// filesystem, so the rename is atomic) at mode 0600, then renaming it over
// final. The temp file is removed on any error path.
func writeAtomic(dir, connName, final string, b []byte) error {
	tmp, err := os.CreateTemp(dir, connName+".*.tmp")
	if err != nil {
		return fmt.Errorf("oauthpersist: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("oauthpersist: chmod temp file: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("oauthpersist: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("oauthpersist: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("oauthpersist: rename temp file: %w", err)
	}
	cleanup = false
	return nil
}
