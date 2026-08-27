package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// callbackURLFrom parses the authorization URL AuthCodeFlow hands to `open`
// and returns the redirect_uri (the loopback callback) and the state it
// generated, so a test's `open` closure can forge callback requests.
func callbackURLFrom(t *testing.T, authURL string) (redirectURI, state string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL %q: %v", authURL, err)
	}
	q := u.Query()
	redirectURI = q.Get("redirect_uri")
	state = q.Get("state")
	if redirectURI == "" || state == "" {
		t.Fatalf("auth URL missing redirect_uri/state: %q", authURL)
	}
	return redirectURI, state
}

func TestAuthCodeFlow_StateMismatch(t *testing.T) {
	m := Metadata{AuthorizationEndpoint: "http://example.invalid/authorize"}
	open := func(authURL string) error {
		redirectURI, _ := callbackURLFrom(t, authURL)
		resp, err := http.Get(redirectURI + "?code=x&state=WRONG")
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, _, _, err := AuthCodeFlow(ctx, m, "cid", "", open)
	if err == nil {
		t.Fatal("a state mismatch must return an error")
	}
	if !strings.Contains(err.Error(), "state") {
		t.Errorf("error should mention state, got: %v", err)
	}
	if code != "" {
		t.Errorf("code must be empty on state mismatch, got %q", code)
	}
}

func TestAuthCodeFlow_AuthorizationError(t *testing.T) {
	m := Metadata{AuthorizationEndpoint: "http://example.invalid/authorize"}
	open := func(authURL string) error {
		redirectURI, state := callbackURLFrom(t, authURL)
		resp, err := http.Get(redirectURI + "?error=access_denied&state=" + state)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, _, _, err := AuthCodeFlow(ctx, m, "cid", "", open)
	if err == nil {
		t.Fatal("an authorization error callback must return an error")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("error should surface the error= reason, got: %v", err)
	}
	if code != "" {
		t.Errorf("code must be empty on authorization error, got %q", code)
	}
}

// TestAuthCodeFlow_Success drives the happy path: a fake `open` that (in a
// goroutine, like a real browser navigating async) GETs the callback URL
// with a valid code and the state AuthCodeFlow generated. Covers the
// default branch of the callback handler (browserflow.go's `default:` case)
// that the state-mismatch/authz-error/timeout tests never reach.
func TestAuthCodeFlow_Success(t *testing.T) {
	m := Metadata{AuthorizationEndpoint: "http://example.invalid/authorize"}
	open := func(authURL string) error {
		redirectURI, state := callbackURLFrom(t, authURL)
		go func() {
			resp, err := http.Get(redirectURI + "?code=THECODE&state=" + state)
			if err != nil {
				return
			}
			resp.Body.Close()
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	var code, redirectURI, verifier string
	var err error
	go func() {
		code, redirectURI, verifier, err = AuthCodeFlow(ctx, m, "cid", "", open)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AuthCodeFlow did not return after a successful callback")
	}

	if err != nil {
		t.Fatalf("AuthCodeFlow: %v", err)
	}
	if code != "THECODE" {
		t.Errorf("code = %q, want THECODE", code)
	}
	if !strings.Contains(redirectURI, "/callback") {
		t.Errorf("redirectURI = %q, want it to end in /callback", redirectURI)
	}
	if verifier == "" {
		t.Error("verifier must be non-empty")
	}
}

// TestAuthCodeFlow_OpenFails covers the `open` failure branch: when the
// caller-supplied opener (e.g. a broken OpenBrowser) errors, AuthCodeFlow
// must return that error wrapped (not hang waiting on a callback that will
// never arrive).
func TestAuthCodeFlow_OpenFails(t *testing.T) {
	m := Metadata{AuthorizationEndpoint: "http://example.invalid/authorize"}
	wantErr := errors.New("boom: no browser available")
	open := func(_ string) error { return wantErr }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	var code string
	var err error
	go func() {
		code, _, _, err = AuthCodeFlow(ctx, m, "cid", "", open)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AuthCodeFlow did not return after open() failed")
	}

	if err == nil {
		t.Fatal("expected an error when open() fails")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want it to wrap %v", err, wantErr)
	}
	if code != "" {
		t.Errorf("code must be empty on open failure, got %q", code)
	}
}

func TestAuthCodeFlow_CtxTimeout(t *testing.T) {
	m := Metadata{AuthorizationEndpoint: "http://example.invalid/authorize"}
	open := func(authURL string) error {
		return nil // never hits the callback
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	var err error
	go func() {
		_, _, _, err = AuthCodeFlow(ctx, m, "cid", "", open)
		close(done)
	}()

	select {
	case <-done:
		if err == nil {
			t.Fatal("expected a ctx-deadline error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AuthCodeFlow did not return after ctx timeout")
	}
}
