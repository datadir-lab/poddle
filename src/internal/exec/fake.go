package exec

// Fake is a test Runner. It records each invocation as {name, args...} in Calls,
// returns Outputs[name] as stdout, and returns Err from both methods when set.
type Fake struct {
	Calls   [][]string
	Outputs map[string]string
	Err     error
}

func (f *Fake) Run(name string, args ...string) (Result, error) {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	if f.Err != nil {
		return Result{}, f.Err
	}
	return Result{Stdout: f.Outputs[name]}, nil
}

func (f *Fake) RunInteractive(name string, args ...string) error {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	return f.Err
}
