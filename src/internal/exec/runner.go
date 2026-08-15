// Package exec is a thin abstraction over running external commands (podman,
// ssh, ...), so provider code can be unit-tested against a fake without the
// real binaries installed.
package exec

import (
	"bytes"
	"os"
	osexec "os/exec"
)

// Result is the captured outcome of a non-interactive command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs external commands. Run captures output; RunInteractive wires the
// child straight to the current process's stdio (for shells / attach).
type Runner interface {
	Run(name string, args ...string) (Result, error)
	RunInteractive(name string, args ...string) error
}

// OS is the real Runner, backed by os/exec.
type OS struct{}

func (OS) Run(name string, args ...string) (Result, error) {
	cmd := osexec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if ee, ok := err.(*osexec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
	}
	return res, err
}

func (OS) RunInteractive(name string, args ...string) error {
	cmd := osexec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
