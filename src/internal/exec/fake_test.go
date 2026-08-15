package exec

import "testing"

func TestFake_RecordsCallsAndReturnsOutput(t *testing.T) {
	f := &Fake{Outputs: map[string]string{"podman": "hello"}}

	res, err := f.Run("podman", "ps", "-a")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stdout != "hello" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello")
	}
	if len(f.Calls) != 1 || f.Calls[0][0] != "podman" || f.Calls[0][2] != "-a" {
		t.Errorf("calls = %v", f.Calls)
	}
}
