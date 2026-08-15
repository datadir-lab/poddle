package broker

import "testing"

func TestCredential_Construct(t *testing.T) {
	c := Credential{
		Mode: ModeSubscription, Vendor: "anthropic",
		Secret: "tok", BaseURL: "https://api.anthropic.com",
	}
	if c.Mode != ModeSubscription || c.Vendor != "anthropic" || c.Secret != "tok" {
		t.Errorf("credential = %+v", c)
	}
}

func TestHandle_Construct(t *testing.T) {
	h := Handle{Value: "poddle_abc", Tenant: "local", CredID: "c1", Scope: "mybox"}
	if h.Value != "poddle_abc" || h.Tenant != "local" || h.CredID != "c1" || h.Scope != "mybox" {
		t.Errorf("handle = %+v", h)
	}
}

func TestZeroValues(t *testing.T) {
	var c Credential
	if c.Mode != "" || c.Secret != "" {
		t.Errorf("zero credential not empty: %+v", c)
	}
	var h Handle
	if h.Value != "" {
		t.Errorf("zero handle not empty: %+v", h)
	}
}
