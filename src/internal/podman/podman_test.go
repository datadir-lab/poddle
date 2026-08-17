package podman

import (
	"errors"
	"reflect"
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

// twoStepRunner returns one output for the `ps` call and another for `stats`.
type twoStepRunner struct {
	ps, stats string
	calls     [][]string
}

func (r *twoStepRunner) Run(name string, args ...string) (exec.Result, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	for _, a := range args {
		if a == "stats" {
			return exec.Result{Stdout: r.stats}, nil
		}
	}
	return exec.Result{Stdout: r.ps}, nil
}
func (r *twoStepRunner) RunInteractive(string, ...string) error { return nil }

func TestAutoscaleStats_JoinsLabelsAndMem(t *testing.T) {
	r := &twoStepRunner{
		ps:    "job|headless|weak\ndev|interactive|strong\n",
		stats: "job|91.5%\ndev|40.0%\n",
	}
	p := New(r, "")
	got, err := p.AutoscaleStats()
	if err != nil {
		t.Fatal(err)
	}
	want := []sandbox.MemStat{
		{Name: "job", Mode: "headless", Size: "weak", MemPercent: 91.5},
		{Name: "dev", Mode: "interactive", Size: "strong", MemPercent: 40.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AutoscaleStats = %+v, want %+v", got, want)
	}
	// It filters to opted-in pods.
	if j := strings.Join(r.calls[0], " "); !strings.Contains(j, "label=poddle.autoscale=true") {
		t.Errorf("should filter by the autoscale label: %q", j)
	}
}

func TestAutoscaleStats_NoneOptedIn(t *testing.T) {
	r := &twoStepRunner{ps: "\n"}
	p := New(r, "")
	got, err := p.AutoscaleStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
	if len(r.calls) != 1 {
		t.Errorf("should skip `podman stats` when no pod opted in; calls = %d", len(r.calls))
	}
}

func TestPodInfo_ParsesLabels(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{
		"podman": "node:22|strong|claude-code|work|https://git/r.git|headless|true\n",
	}}
	p := New(f, "")
	got, err := p.PodInfo("box")
	if err != nil {
		t.Fatal(err)
	}
	want := sandbox.PodInfo{
		Image: "node:22", Size: "strong", Harness: "claude-code",
		Identity: "work", Repo: "https://git/r.git", Mode: "headless", Autoscale: true,
	}
	if got != want {
		t.Errorf("PodInfo = %+v, want %+v", got, want)
	}
}

func TestCreate_AutoscaleLabel(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "cid\n"}}
	p := New(f, "")
	if _, err := p.Create(sandbox.Spec{Name: "box", Image: "img", Autoscale: true}); err != nil {
		t.Fatal(err)
	}
	var call string
	for _, c := range f.Calls {
		if j := strings.Join(c, " "); strings.Contains(j, "run -d") {
			call = j
		}
	}
	if !strings.Contains(call, "--label poddle.autoscale=true") {
		t.Errorf("autoscale label missing in run args: %q", call)
	}
}

func TestCreate_BuildsRunArgs(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "cid123\n"}}
	p := New(f, "")

	id, err := p.Create(sandbox.Spec{
		Name: "box", Image: "debian:slim", Template: "base",
		Runtime: "container", Size: "strong", CPUs: 8, Memory: "16g", Repo: "r",
		Identity: "work", Harness: "claude-code",
		Mounts:  []sandbox.Mount{{Host: "/h/.claude", Container: "/root/.claude", ReadOnly: true}},
		Volumes: []sandbox.Volume{{Name: "poddle-box-workspace", Container: "/workspace"}},
		Env:     map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != "cid123" {
		t.Errorf("id = %q", id)
	}
	var call string
	for _, c := range f.Calls {
		if j := strings.Join(c, " "); strings.Contains(j, "run -d") {
			call = j
		}
	}
	for _, w := range []string{
		"podman run -d", "--name box",
		"--label poddle.managed=true", "--label poddle.name=box",
		"--label poddle.template=base", "--label poddle.runtime=container",
		"--label poddle.size=strong", "--label poddle.repo=r",
		"--label poddle.image=debian:slim", "--label poddle.identity=work",
		"--label poddle.harness=claude-code",
		"--cpus 8", "--memory 16g",
		"--volume /h/.claude:/root/.claude:ro", "--volume poddle-box-workspace:/workspace",
		"--env CLAUDE_CODE_OAUTH_TOKEN=tok",
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

func TestCreate_RunsSetupCommands(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "cid123\n"}}
	p := New(f, "")

	id, err := p.Create(sandbox.Spec{
		Name: "box", Image: "img",
		Setup: []string{"npm i -g @anthropic-ai/claude-code", "echo hi"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != "cid123" {
		t.Errorf("id = %q", id)
	}
	if len(f.Calls) != 3 { // run + 2 setup execs
		t.Fatalf("want 3 calls (run + 2 setup), got %d: %v", len(f.Calls), f.Calls)
	}
	want1 := []string{"podman", "exec", "cid123", "sh", "-c", "npm i -g @anthropic-ai/claude-code"}
	if !reflect.DeepEqual(f.Calls[1], want1) {
		t.Errorf("setup 1 = %v, want %v", f.Calls[1], want1)
	}
	want2 := []string{"podman", "exec", "cid123", "sh", "-c", "echo hi"}
	if !reflect.DeepEqual(f.Calls[2], want2) {
		t.Errorf("setup 2 = %v, want %v", f.Calls[2], want2)
	}
}

// failExecRunner succeeds for `podman run` but fails for `podman exec`.
type failExecRunner struct{ calls [][]string }

func (r *failExecRunner) Run(name string, args ...string) (exec.Result, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	for _, a := range args {
		if a == "exec" {
			return exec.Result{Stderr: "install boom"}, errors.New("exit 1")
		}
	}
	return exec.Result{Stdout: "cid123\n"}, nil
}
func (r *failExecRunner) RunInteractive(name string, args ...string) error { return nil }

func TestCreate_SetupFailureReturnsError(t *testing.T) {
	p := New(&failExecRunner{}, "")
	_, err := p.Create(sandbox.Spec{Name: "box", Image: "img", Setup: []string{"bad-cmd"}})
	if err == nil {
		t.Fatal("expected an error when a setup command fails")
	}
	if !strings.Contains(err.Error(), "box") {
		t.Errorf("error should name the pod for cleanup, got %v", err)
	}
}

func TestExec_BuildsArgs(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.Exec("cid", "npm test"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman exec cid sh -c npm test" {
		t.Errorf("exec args = %q", got)
	}
}

func TestExec_RemoteAddsURL(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "ssh://h/sock")
	if err := p.Exec("cid", "echo hi"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := strings.Join(f.Calls[0], " "); got != "podman --url ssh://h/sock exec cid sh -c echo hi" {
		t.Errorf("remote exec args = %q", got)
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

// statsRunner returns pod names for `ps` and a stats table for `stats`.
type statsRunner struct{}

func (statsRunner) Run(name string, args ...string) (exec.Result, error) {
	if len(args) > 0 && args[0] == "ps" {
		return exec.Result{Stdout: "box1\nbox2\n"}, nil
	}
	return exec.Result{Stdout: "box1|10.0%|100MB / 4GB|2.5%\nbox2|5.0%|50MB / 4GB|1.2%\n"}, nil
}
func (statsRunner) RunInteractive(string, ...string) error { return nil }

func TestStats_ListsRunningPods(t *testing.T) {
	stats, err := New(statsRunner{}, "").Stats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d stats, want 2: %+v", len(stats), stats)
	}
	if stats[0].Name != "box1" || stats[0].CPU != "10.0%" || stats[0].Mem != "100MB / 4GB" {
		t.Errorf("stats[0] = %+v", stats[0])
	}
	if stats[1].MemPerc != "1.2%" {
		t.Errorf("stats[1] mem%% = %q", stats[1].MemPerc)
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
