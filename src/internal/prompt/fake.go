package prompt

import "fmt"

// FakePrompter returns queued responses and errors if asked for one it wasn't
// given — so an unexpected prompt fails a test loudly instead of hanging.
type FakePrompter struct {
	Selects []int    // queued Select return indices, in order
	Inputs  []string // queued Input return values, in order
	si, ii  int
}

func (f *FakePrompter) Select(label string, options []string) (int, error) {
	if f.si >= len(f.Selects) {
		return 0, fmt.Errorf("FakePrompter: unexpected Select(%q)", label)
	}
	i := f.Selects[f.si]
	f.si++
	return i, nil
}

func (f *FakePrompter) Input(label string) (string, error) {
	if f.ii >= len(f.Inputs) {
		return "", fmt.Errorf("FakePrompter: unexpected Input(%q)", label)
	}
	v := f.Inputs[f.ii]
	f.ii++
	return v, nil
}
