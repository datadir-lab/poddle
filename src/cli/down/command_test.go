package down

import "testing"

type fakeRemover struct {
	removed string
	err     error
}

func (f *fakeRemover) Remove(id string) error {
	f.removed = id
	return f.err
}

func TestDown_RemovesByArg(t *testing.T) {
	f := &fakeRemover{}
	c := NewCmd(f)
	c.SetArgs([]string{"mybox"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.removed != "mybox" {
		t.Errorf("removed = %q, want mybox", f.removed)
	}
}
