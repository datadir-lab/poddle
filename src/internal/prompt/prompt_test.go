package prompt

import "testing"

var (
	_ Prompter = (*FakePrompter)(nil)
	_ Prompter = (*Huh)(nil)
)

func TestFakePrompter_Queued(t *testing.T) {
	f := &FakePrompter{Selects: []int{2}, Inputs: []string{"work"}}
	if i, err := f.Select("pick", []string{"a", "b", "c"}); err != nil || i != 2 {
		t.Errorf("select = %d, %v; want 2, nil", i, err)
	}
	if v, err := f.Input("name"); err != nil || v != "work" {
		t.Errorf("input = %q, %v; want work, nil", v, err)
	}
}

func TestFakePrompter_ExhaustedErrors(t *testing.T) {
	f := &FakePrompter{}
	if _, err := f.Select("pick", []string{"a"}); err == nil {
		t.Error("expected an error when no Select was queued")
	}
	if _, err := f.Input("name"); err == nil {
		t.Error("expected an error when no Input was queued")
	}
}
