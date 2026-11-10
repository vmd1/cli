// Package cli is the tiny command framework behind `vmd1`. A Module is a group of commands (`vmd1 agent ...`, later
// `vmd1 node ...`); commands nest, so `agent path pick` is Module "agent" -> Command "path" -> Command "pick". Standard library only.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Env is what a command gets: its output streams and the global settings. Tests build their own.
type Env struct {
	Out, Err io.Writer
	Getenv   func(string) string
}

func DefaultEnv() *Env { return &Env{Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv} }

// Command is a leaf (Run set) or a group (Commands set).
type Command struct {
	Name     string
	Summary  string
	Usage    string // arguments, shown after the command path in help, e.g. "<name|default>"
	Run      func(ctx context.Context, env *Env, args []string) error
	Commands []Command
}

// Module is one area of the tool. Register it in cmd/vmd1/main.go.
type Module struct {
	Name     string
	Summary  string
	Commands []Command
}

type Root struct {
	Name    string
	Version string
	Modules []Module
}

// UsageError is printed with the help for the command that was being run, and exits with status 2.
type UsageError struct{ Msg string }

func (e UsageError) Error() string { return e.Msg }

// ExitError ends the process with Code without printing anything: the command already reported its error itself (an embedded
// tool such as nodectl). Code 0 is not an error.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Run dispatches args (without the program name) and returns the process exit status.
func (r *Root) Run(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) > 1 && args[0] == "help" {
			return r.help(env, args[1:])
		}
		r.rootHelp(env.Out)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintf(env.Out, "%s %s\n", r.Name, r.Version)
		return 0
	}
	for _, m := range r.Modules {
		if m.Name == args[0] {
			return r.runIn(ctx, env, m.Name, m.Commands, args[1:])
		}
	}
	fmt.Fprintf(env.Err, "unknown command %q\n\n", args[0])
	r.rootHelp(env.Err)
	return 2
}

func (r *Root) runIn(ctx context.Context, env *Env, path string, cmds []Command, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		r.groupHelp(env.Out, path, cmds)
		return 0
	}
	for _, c := range cmds {
		if c.Name != args[0] {
			continue
		}
		p := path + " " + c.Name
		if c.Run == nil {
			return r.runIn(ctx, env, p, c.Commands, args[1:])
		}
		if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
			fmt.Fprintf(env.Out, "usage: %s %s %s\n\n%s\n", r.Name, p, c.Usage, c.Summary)
			return 0
		}
		if err := c.Run(ctx, env, args[1:]); err != nil {
			if ue, ok := err.(UsageError); ok {
				fmt.Fprintf(env.Err, "%s\nusage: %s %s %s\n", ue.Msg, r.Name, p, c.Usage)
				return 2
			}
			if ee, ok := err.(ExitError); ok {
				return ee.Code
			}
			fmt.Fprintf(env.Err, "%s: %v\n", r.Name, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(env.Err, "unknown command %q in %q\n\n", args[0], path)
	r.groupHelp(env.Err, path, cmds)
	return 2
}

func (r *Root) help(env *Env, path []string) int {
	for _, m := range r.Modules {
		if m.Name != path[0] {
			continue
		}
		cmds, p := m.Commands, m.Name
		for _, seg := range path[1:] {
			found := false
			for _, c := range cmds {
				if c.Name == seg && c.Run == nil {
					cmds, p, found = c.Commands, p+" "+seg, true
				}
			}
			if !found {
				fmt.Fprintf(env.Err, "no such command group %q\n", strings.Join(path, " "))
				return 2
			}
		}
		r.groupHelp(env.Out, p, cmds)
		return 0
	}
	fmt.Fprintf(env.Err, "unknown command %q\n", path[0])
	return 2
}

func (r *Root) rootHelp(w io.Writer) {
	fmt.Fprintf(w, "usage: %s <command> ...\n\ncommands:\n", r.Name)
	ms := append([]Module(nil), r.Modules...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	for _, m := range ms {
		fmt.Fprintf(w, "  %-10s %s\n", m.Name, m.Summary)
	}
	fmt.Fprintf(w, "  %-10s %s\n  %-10s %s\n", "version", "print the version", "help", "help for a command: "+r.Name+" help <command>")
}

func (r *Root) groupHelp(w io.Writer, path string, cmds []Command) {
	fmt.Fprintf(w, "usage: %s %s <command>\n\ncommands:\n", r.Name, path)
	for _, c := range cmds {
		name := c.Name
		if c.Usage != "" {
			name += " " + c.Usage
		}
		fmt.Fprintf(w, "  %-22s %s\n", name, c.Summary)
	}
}
