//go:build docgen

// Command docgen (built with the `docgen` build tag) walks the real poddle
// command tree — NewRootCmd(), the same tree the binary ships — and prints a
// JSON description of every command (path, usage, descriptions, flags) to
// stdout. The website's /docs page renders that JSON, so the published command
// reference is generated from the code and can never drift.
//
//	go run -tags docgen ./src/cli > src/web/site/src/data/cli.json
package main

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type flagDoc struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
}

type commandDoc struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Use     string    `json:"use"`
	Short   string    `json:"short"`
	Long    string    `json:"long,omitempty"`
	Example string    `json:"example,omitempty"`
	Flags   []flagDoc `json:"flags,omitempty"`
	Subs    []string  `json:"subcommands,omitempty"`
}

func docSkip(c *cobra.Command) bool {
	return c.Hidden || c.Name() == "help" || c.Name() == "completion"
}

func docWalk(cmd *cobra.Command, out *[]commandDoc) {
	doc := commandDoc{
		Path:    cmd.CommandPath(),
		Name:    cmd.Name(),
		Use:     cmd.Use,
		Short:   cmd.Short,
		Long:    cmd.Long,
		Example: cmd.Example,
	}
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" || f.Name == "version" {
			return
		}
		doc.Flags = append(doc.Flags, flagDoc{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
			Usage:     f.Usage,
		})
	})

	subs := append([]*cobra.Command(nil), cmd.Commands()...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
	for _, c := range subs {
		if docSkip(c) {
			continue
		}
		doc.Subs = append(doc.Subs, c.CommandPath())
	}

	*out = append(*out, doc)
	for _, c := range subs {
		if docSkip(c) {
			continue
		}
		docWalk(c, out)
	}
}

func main() {
	var docs []commandDoc
	docWalk(NewRootCmd(), &docs)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(docs); err != nil {
		os.Exit(1)
	}
}
