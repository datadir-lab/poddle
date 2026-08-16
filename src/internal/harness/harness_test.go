package harness

import (
	"reflect"
	"testing"
)

// FakeHarness must satisfy the Harness contract.
var _ Harness = (*FakeHarness)(nil)

func TestFakeHarness_Env(t *testing.T) {
	f := &FakeHarness{HarnessName: "fake"}
	env := f.Env("http://broker:9000", "poddle_abc")
	if env["BROKER_ADDR"] != "http://broker:9000" || env["HANDLE"] != "poddle_abc" {
		t.Errorf("env = %v", env)
	}
}

func TestFakeHarness_Supports(t *testing.T) {
	f := &FakeHarness{Vendors: []string{"anthropic"}}
	if !f.Supports("anthropic") {
		t.Error("should support anthropic")
	}
	if f.Supports("openai") {
		t.Error("should not support openai")
	}
}

func TestFakeHarness_Provisions(t *testing.T) {
	want := []string{"install-me"}
	f := &FakeHarness{Provs: want}
	if got := f.Provisions(); !reflect.DeepEqual(got, want) {
		t.Errorf("provisions = %v, want %v", got, want)
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := Registry{"claude-code": &FakeHarness{HarnessName: "claude-code", Vendors: []string{"anthropic"}}}

	h, ok := reg.Get("claude-code")
	if !ok || h.Name() != "claude-code" {
		t.Fatalf("get = %v, %v", h, ok)
	}
	if !h.Supports("anthropic") {
		t.Error("expected anthropic support")
	}
	if _, ok := reg.Get("nope"); ok {
		t.Error("expected miss for unknown harness")
	}
}
