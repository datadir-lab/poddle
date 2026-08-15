package podman

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/engine"
	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
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

func TestCreate_BuildsRunArgs(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "cid123\n"}}
	p := New(f, "")

	id, err := p.Create(sandbox.Spec{
		Name: "box", Image: "debian:slim", Template: "base",
		Runtime: "container", Size: "strong", CPUs: 8, Memory: "16g", Repo: "r",
		Mounts: []sandbox.Mount{{Host: "/h/.claude", Container: "/root/.claude", ReadOnly: true}},
		Env:    map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != "cid123" {
		t.Errorf("id = %q", id)
	}
	call := strings.Join(f.Calls[0], " ")
	for _, w := range []string{
		"podman run -d", "--name box",
		"--label poddle.managed=true", "--label poddle.name=box",
		"--label poddle.template=base", "--label poddle.runtime=container",
		"--label poddle.size=strong", "--label poddle.repo=r",
		"--cpus 8", "--memory 16g",
		"--volume /h/.claude:/root/.claude:ro", "--env CLAUDE_CODE_OAUTH_TOKEN=tok",
		"debian:slim tail -f /dev/null",
	} {
		if !strings.Contains(call, w) {
			t.Errorf("run args missing %q in %q", w, call)
		}
	}
}

func TestCreate_RemoteAddsURL(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "x\n"}}
	p := New(f, "ssh://h/sock")
	if _, err := p.Create(sandbox.Spec{Name: "b", Image: "img"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(strings.Join(f.Calls[0], " "), "--url ssh://h/sock run -d") {
		t.Errorf("remote url missing: %v", f.Calls[0])
	}
}

func TestAttach_BuildsInteractiveExec(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.Attach("cid"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	call := strings.Join(f.Calls[0], " ")
	if !strings.Contains(call, "exec -it cid sh -c") {
		t.Errorf("attach args = %v", f.Calls[0])
	}
}

func TestRemove_BuildsArgs(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.Remove("cid"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Join(f.Calls[0], " ") != "podman rm -f cid" {
		t.Errorf("remove args = %v", f.Calls[0])
	}
}

func TestRemove_RemoteAddsURL(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "ssh://h/sock")
	if err := p.Remove("cid"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Join(f.Calls[0], " ") != "podman --url ssh://h/sock rm -f cid" {
		t.Errorf("remote remove args = %v", f.Calls[0])
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
