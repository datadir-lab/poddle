package podman

import (
	"errors"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/exec"
)

func TestExecDetached_BuildsArgs(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.ExecDetached("cid", "npm install"); err != nil {
		t.Fatalf("ExecDetached: %v", err)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman exec -d cid sh -c npm install" {
		t.Errorf("args = %q", got)
	}
}

func TestExecDetached_RemoteAddsURL(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "ssh://h/sock")
	if err := p.ExecDetached("cid", "x"); err != nil {
		t.Fatalf("ExecDetached: %v", err)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman --url ssh://h/sock exec -d cid sh -c x" {
		t.Errorf("args = %q", got)
	}
}

func TestExecDetached_PropagatesError(t *testing.T) {
	p := New(&exec.Fake{Err: errors.New("boom")}, "")
	if err := p.ExecDetached("cid", "x"); err == nil {
		t.Error("expected the runner error to propagate")
	}
}

func TestExecTTY_BuildsInteractiveArgs(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.ExecTTY("cid", "vim ."); err != nil {
		t.Fatalf("ExecTTY: %v", err)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman exec -it cid sh -c vim ." {
		t.Errorf("args = %q", got)
	}
}

func TestResize_BuildsArgs(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.Resize("cid", 2.5, "512m"); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman update --cpus 2.5 --memory 512m cid" {
		t.Errorf("args = %q", got)
	}
}

func TestResize_OmitsUnsetFields(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.Resize("cid", 0, ""); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman update cid" {
		t.Errorf("args = %q, want no --cpus/--memory flags", got)
	}
}

func TestRemoveVolumesForPod_ListsThenRemoves(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "vol1\nvol2\n"}}
	p := New(f, "")
	if err := p.RemoveVolumesForPod("box"); err != nil {
		t.Fatalf("RemoveVolumesForPod: %v", err)
	}
	if len(f.Calls) != 3 {
		t.Fatalf("expected list + remove + lock-network rm (3 calls), got %d: %v", len(f.Calls), f.Calls)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman volume ls -q --filter label=poddle.pod=box" {
		t.Errorf("list args = %q", got)
	}
	if got := strings.Join(f.Calls[1], " "); got != "podman volume rm vol1 vol2" {
		t.Errorf("remove args = %q", got)
	}
	if got := strings.Join(f.Calls[2], " "); got != "podman network rm poddle-lock-box" {
		t.Errorf("lock-network rm args = %q", got)
	}
}

func TestRemoveVolumesForPod_NoVolumesIsNoop(t *testing.T) {
	f := &exec.Fake{} // empty stdout -> no volumes
	p := New(f, "")
	if err := p.RemoveVolumesForPod("box"); err != nil {
		t.Fatalf("RemoveVolumesForPod: %v", err)
	}
	if len(f.Calls) != 2 {
		t.Errorf("expected the list call + best-effort lock-network rm, got %d: %v", len(f.Calls), f.Calls)
	}
}
