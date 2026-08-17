package up

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/config"
)

func TestMove_RecreatesWithVolumesNoClone(t *testing.T) {
	f := &fakeCreator{}
	ap := taskApp(t, f, "run %s") // identity "work" exists
	ap.Templates = fakeTemplates{tpl: config.Template{
		Identity: "work", Harness: "claude-code",
		Repo: "https://git.example/r.git", Image: "docker.io/library/debian:stable-slim",
	}}
	var log []string
	c := NewMoveCmd(ap, &spyBroker{log: &log})
	c.SetArgs([]string{"proj", "--size", "strong"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The old shell is removed (its named volumes persist).
	if f.removed != "proj" {
		t.Errorf("old shell should be removed, removed = %q", f.removed)
	}
	// A new shell is created, re-sized, mounting the session's workspace volume.
	if f.spec.Name != "proj" || f.spec.Size != "strong" {
		t.Errorf("new spec = %+v", f.spec)
	}
	ws := false
	for _, v := range f.spec.Volumes {
		if v.Name == "poddle-proj-workspace" && v.Container == "/workspace" {
			ws = true
		}
	}
	if !ws {
		t.Errorf("move should mount the workspace volume; volumes = %v", f.spec.Volumes)
	}
	// The repo is NOT re-cloned (the volume already has it).
	for _, s := range f.spec.Setup {
		if strings.Contains(s, "git clone") {
			t.Errorf("move should not re-clone; setup = %v", f.spec.Setup)
		}
	}
	// Old handles retired before re-brokering.
	revoked := false
	for _, e := range log {
		if e == "revoke:proj" {
			revoked = true
		}
	}
	if !revoked {
		t.Errorf("move should retire old handles; log = %v", log)
	}
}

func moveApp(t *testing.T, f *fakeCreator) *app.App {
	t.Helper()
	ap := taskApp(t, f, "run %s")
	ap.Templates = fakeTemplates{tpl: config.Template{Identity: "work", Harness: "claude-code"}}
	return ap
}

func TestMove_ResumesHeadlessInBackground(t *testing.T) {
	f := &fakeCreator{podMode: "headless"}
	var log []string
	c := NewMoveCmd(moveApp(t, f), &spyBroker{log: &log})
	c.SetArgs([]string{"proj", "--size", "strong"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(f.detached, "resume-headless") {
		t.Errorf("headless move should resume the agent in the background, detached = %q", f.detached)
	}
	if f.attached != "" {
		t.Errorf("headless move should not attach a shell, attached = %q", f.attached)
	}
}

func TestMove_ResumesInteractive(t *testing.T) {
	f := &fakeCreator{podMode: "interactive"}
	var log []string
	c := NewMoveCmd(moveApp(t, f), &spyBroker{log: &log})
	c.SetArgs([]string{"proj"}) // no --detach → interactive resume
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.ttyExeced != "resume-interactive" {
		t.Errorf("interactive move should ExecTTY the resume, got %q", f.ttyExeced)
	}
	if f.attached != "" {
		t.Errorf("interactive move should resume, not plain-attach; attached = %q", f.attached)
	}
}
