// Package prompt is poddle's tiny interactive-prompt seam. The composition root
// wires a real TUI (charmbracelet/huh) when stdin is a terminal; everywhere
// else the Prompter is nil (no prompting) and tests use FakePrompter.
package prompt

import "github.com/charmbracelet/huh"

// Prompter asks the user to choose from a list or type a line.
type Prompter interface {
	// Select shows options (arrow-key navigable) and returns the chosen index.
	Select(label string, options []string) (int, error)
	// Input reads a line of free text.
	Input(label string) (string, error)
}

// Huh is a Prompter backed by charmbracelet/huh.
type Huh struct{}

// NewHuh returns a huh-backed Prompter.
func NewHuh() *Huh { return &Huh{} }

func (Huh) Select(label string, options []string) (int, error) {
	opts := make([]huh.Option[int], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, i)
	}
	var chosen int
	err := huh.NewSelect[int]().Title(label).Options(opts...).Value(&chosen).Run()
	return chosen, err
}

func (Huh) Input(label string) (string, error) {
	var v string
	err := huh.NewInput().Title(label).Value(&v).Run()
	return v, err
}
