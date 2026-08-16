package harness

import "strings"

// FakeHarness is a test Harness with configurable name, provisions, and
// supported vendors. Env returns the broker address and handle it was called
// with under fixed keys, so wiring tests can assert them deterministically.
type FakeHarness struct {
	HarnessName string
	Provs       []string
	Vendors     []string
	Task        string // returned by TaskCommand; %s is replaced with the prompt
}

func (f *FakeHarness) Name() string { return f.HarnessName }

func (f *FakeHarness) Provisions() []string { return f.Provs }

func (f *FakeHarness) Supports(vendor string) bool {
	for _, v := range f.Vendors {
		if v == vendor {
			return true
		}
	}
	return false
}

func (f *FakeHarness) Env(brokerAddr, handle string) map[string]string {
	return map[string]string{"BROKER_ADDR": brokerAddr, "HANDLE": handle}
}

func (f *FakeHarness) TaskCommand(prompt string, _ int) string {
	if f.Task == "" {
		return "run " + prompt
	}
	return strings.ReplaceAll(f.Task, "%s", prompt)
}
