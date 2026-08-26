package oauth

import (
	"context"
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
