package exec

// Fake is a test Runner. It records each invocation as {name, args...} in Calls,
// returns Outputs[name] as stdout, and returns Err from both methods when set.
// Stderr, when set, is returned as Result.Stderr on every call (with or without
// Err) so tests can exercise stderr-driven branches (e.g. "already exists").
type Fake struct {
	Calls   [][]string
	Outputs map[string]string
	Err     error
	Stderr  string
}

func (f *Fake) Run(name string, args ...string) (Result, error) {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	if f.Err != nil {
		return Result{Stderr: f.Stderr}, f.Err
	}
	return Result{Stdout: f.Outputs[name], Stderr: f.Stderr}, nil
}

func (f *Fake) RunInteractive(name string, args ...string) error {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	return f.Err
}
