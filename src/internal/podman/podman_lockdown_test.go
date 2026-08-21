package podman

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/brokerendpoint"
	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

func lockedSpec() sandbox.Spec {
	return sandbox.Spec{Name: "box", Image: "img",
		Network: &sandbox.Network{AllowList: []brokerendpoint.HostPort{{Host: "host.containers.internal", Port: "16379"}}}}
}

func TestCreate_LockedNetwork_Attaches(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "cid123\n"}}
	p := New(f, "")
	if _, err := p.Create(lockedSpec()); err != nil {
		t.Fatalf("create: %v", err)
	}
	var joined string
	for _, c := range f.Calls {
		joined += strings.Join(c, " ") + "\n"
	}
	if strings.Contains(joined, "network create") {
		t.Errorf("Create must no longer create the lock network itself; calls:\n%s", joined)
	}
	if !strings.Contains(joined, "--network poddle-lock-box") {
		t.Errorf("expected run --network poddle-lock-box; calls:\n%s", joined)
	}
}

func TestCreate_NoNetwork_Unchanged(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "cid\n"}}
	p := New(f, "")
	if _, err := p.Create(sandbox.Spec{Name: "box", Image: "img"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, c := range f.Calls {
		if strings.Contains(strings.Join(c, " "), "network create") {
			t.Error("a pod with no Network must not create a lock network")
		}
	}
}

func TestRemoveVolumesForPod_RemovesLockNetwork(t *testing.T) {
	f := &exec.Fake{} // empty volume list -> just the network rm
	p := New(f, "")
	if err := p.RemoveVolumesForPod("box"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	var joined string
	for _, c := range f.Calls {
		joined += strings.Join(c, " ") + "\n"
	}
	if !strings.Contains(joined, "network rm poddle-lock-box") {
		t.Errorf("expected lock network removal; calls:\n%s", joined)
	}
}
