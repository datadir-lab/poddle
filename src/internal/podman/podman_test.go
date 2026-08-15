package podman

import (
	"strings"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// Provider must satisfy the engine.Engine contract.
var _ engine.Engine = (*Provider)(nil)

func TestList_BuildsArgsAndParses(t *testing.T) {
	out := `[{"Id":"abc123def4567890","State":"running","Labels":{"poddle.managed":"true","poddle.name":"app","poddle.template":"python","poddle.runtime":"container","poddle.size":"strong","poddle.repo":"https://f/me/app.git"}}]`
	f := &exec.Fake{Outputs: map[string]string{"podman": out}}
	p := New(f, "")

	list, err := p.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}
	want := sandbox.Sandbox{
		ID: "abc123def456", Name: "app", Template: "python",
		Runtime: "container", Size: "strong",
		Repo: "https://f/me/app.git", State: "running",
	}
	if list[0] != want {
		t.Errorf("sandbox = %+v, want %+v", list[0], want)
	}

	call := strings.Join(f.Calls[0], " ")
	for _, w := range []string{"podman ps -a", "--filter label=poddle.managed=true", "--format json"} {
		if !strings.Contains(call, w) {
			t.Errorf("args missing %q in %q", w, call)
		}
	}
}

func TestList_RemoteAddsURL(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "[]"}}
	p := New(f, "ssh://me@host/run/podman/podman.sock")
	if _, err := p.List(); err != nil {
		t.Fatalf("list: %v", err)
	}
	call := strings.Join(f.Calls[0], " ")
	if !strings.Contains(call, "--url ssh://me@host/run/podman/podman.sock") {
		t.Errorf("remote url missing: %q", call)
	}
}

func TestList_EmptyOutput(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	list, err := p.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("want empty, got %v", list)
	}
}

func TestMapState(t *testing.T) {
	for in, want := range map[string]string{
		"running": "running", "paused": "paused",
		"exited": "stopped", "created": "stopped", "dead": "stopped",
	} {
		if got := mapState(in); got != want {
			t.Errorf("mapState(%q) = %q, want %q", in, got, want)
		}
	}
}
