package connect

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/connector"
)

func TestConnect_AddViaStdin(t *testing.T) {
	store := connector.NewStore(t.TempDir())
	c := NewCmd(&app.App{Connections: store})
	c.SetArgs([]string{"add", "my-forgejo", "--connector", "forgejo", "--url", "http://forge", "--user", "me"})
	c.SetIn(strings.NewReader("SECRET-TOKEN\n")) // token piped, never in argv

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := store.Get("my-forgejo")
	if err != nil {
		t.Fatalf("connection not stored: %v", err)
	}
	if got.Connector != "forgejo" || got.User != "me" || got.BaseURL != "http://forge" {
		t.Errorf("stored connection = %+v", got)
	}
}

func TestConnect_LsAndRm(t *testing.T) {
	store := connector.NewStore(t.TempDir())
	if _, err := store.Create("wp", "woodpecker", "http://wp", "", "TOK", ""); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := NewCmd(&app.App{Connections: store})
	c.SetOut(&out)
	c.SetArgs([]string{"ls"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"NAME", "wp", "woodpecker"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("ls output missing %q:\n%s", w, out.String())
		}
	}

	rm := NewCmd(&app.App{Connections: store})
	rm.SetArgs([]string{"rm", "wp"})
	if err := rm.Execute(); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.List(); len(list) != 0 {
		t.Errorf("expected removed, got %v", list)
	}
}
