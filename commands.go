// Package commands is a small, zero-dependency framework for building
// subcommand-style CLIs: verb registration and dispatch, declarative flag
// parsing (Flagged/SetFlags), nested subcommand reuse (Dispatch), a
// configurable global root flag, and hooks for error rendering. It uses
// only the standard library — error copy, help text, and the root-flag
// name are product decisions injected by the consumer at assembly time.
package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Environment is the execution environment of one command (injectable in
// tests). Root is the landing spot of the global root flag (App.RootFlag,
// e.g. "R") — the framework extracts it without interpreting it; its
// meaning (repository root, workspace root, …) is defined by the verb.
// Empty means "not provided".
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer
	Root   string
}

// Command is the verb contract. The error returned by Run is rendered to
// stderr by App.Run via RenderError and ends the process with exit code 1
// (both the rendering and the exit policy are hook-customizable).
type Command interface {
	Name() string
	Synopsis() string
	Usage() string
	Run(ctx context.Context, env *Environment, args []string) error
}

// Flagged is implemented by verbs that take flags: SetFlags declares them
// (pointers stored in the verb struct, fields read inside Run); FlagSet
// construction and parse-error mapping are owned by App in one place, so
// the message shape is not copy-pasted per verb. Verbs that do not
// implement Flagged get their arguments passed through untouched (bare
// verbs keep their ignore-unknown-arguments semantics).
type Flagged interface {
	SetFlags(fs *flag.FlagSet)
}

// Exit-code convention: 0 success; 1 command error; 2 usage error
// (unknown verb).
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// ErrHelp reports that help output has already been written to
// Environment.Stdout: a verb-level -h/-help was parsed in App.ParseFlags.
// App.Run maps it to ExitOK without rendering; consumers driving Dispatch
// directly should treat it as success.
var ErrHelp = errors.New("help requested")

// UnknownVerbError is the error shape of a dispatch miss. App.Run uses it
// to pick the usage exit code and append the help screen; its message can
// be rewritten through the RenderError hook (product voice).
type UnknownVerbError struct{ Name string }

func (e *UnknownVerbError) Error() string { return fmt.Sprintf("unknown verb %q", e.Name) }

// App holds the registered verbs. The zero value is usable; New
// additionally sets the default verb title.
type App struct {
	commands map[string]Command

	// RootFlag is the global root-flag name ("R" ⇒ matches -R/--R/-R=/--R=,
	// stripped wherever it appears — including before the verb name — with
	// the value landing in Environment.Root). Empty = no stripping. Nested
	// inner apps should leave it empty — the outer layer already stripped
	// it.
	RootFlag string

	// FlagError maps a flag parse failure to a business error. Default
	// "%s: bad arguments: %v".
	FlagError func(verb string, err error) error

	// RenderError renders a verb error as a single stderr line. Default
	// err.Error().
	RenderError func(error) string

	// HelpHeader / VerbTitle / HelpFooter compose the help screen
	// (printHelp).
	HelpHeader string
	VerbTitle  string
	HelpFooter string
}

// New returns an empty app (verb title defaults to "commands:").
func New() *App {
	return &App{
		commands:  map[string]Command{},
		VerbTitle: "commands:",
	}
}

// Register adds verbs to the dispatch table. It panics on a verb named
// "help" (reserved for the built-in help screen) or on a duplicate name —
// both are assembly-time bugs best caught immediately, in the spirit of
// regexp.MustCompile.
func (a *App) Register(cmds ...Command) {
	if a.commands == nil {
		a.commands = map[string]Command{}
	}
	for _, c := range cmds {
		name := c.Name()
		if name == "help" {
			panic(fmt.Sprintf("commands: verb name %q is reserved", name))
		}
		if _, dup := a.commands[name]; dup {
			panic(fmt.Sprintf("commands: duplicate verb %q", name))
		}
		a.commands[name] = c
	}
}

// Lookup finds a verb by name.
func (a *App) Lookup(name string) (Command, bool) {
	c, ok := a.commands[name]
	return c, ok
}

// Names returns all verb names sorted alphabetically — help-screen order
// is independent of registration order.
func (a *App) Names() []string {
	names := make([]string, 0, len(a.commands))
	for n := range a.commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Run is the process top-level entry; it returns the exit code:
//
//   - no arguments, "help", or a -h/-help flag prints the help screen to
//     stdout and returns ExitOK; "help <verb>" prints that verb's usage
//     and flag defaults to stdout and returns ExitOK
//   - an unknown verb (or "help <unknown>") renders the error, appends
//     the help screen to stderr, and returns ExitUsage
//   - any other verb error is rendered to stderr and returns ExitError
//
// If RootFlag is set, its forms are stripped from the full argument list
// first, so the root flag may also precede the verb name. A verb-level
// -h (parsed in ParseFlags) has already printed the verb's help to
// stdout; Run maps ErrHelp to ExitOK without rendering.
func (a *App) Run(ctx context.Context, env *Environment, args []string) int {
	e := *env
	if a.RootFlag != "" {
		if stripped, root, found := extractRootFlag(a.RootFlag, args); found {
			args, e.Root = stripped, root
		}
	}
	if len(args) == 0 || isHelpFlag(args[0]) {
		a.printHelp(e.Stdout)
		return ExitOK
	}
	if args[0] == "help" {
		if len(args) > 1 {
			cmd, ok := a.Lookup(args[1])
			if !ok {
				fmt.Fprintln(e.Stderr, a.render(&UnknownVerbError{Name: args[1]}))
				a.printHelp(e.Stderr)
				return ExitUsage
			}
			a.printVerbHelp(e.Stdout, cmd)
			return ExitOK
		}
		a.printHelp(e.Stdout)
		return ExitOK
	}
	err := a.Dispatch(ctx, &e, args)
	if err == nil || errors.Is(err, ErrHelp) {
		return ExitOK
	}
	fmt.Fprintln(e.Stderr, a.render(err))
	var unknown *UnknownVerbError
	if errors.As(err, &unknown) {
		a.printHelp(e.Stderr)
		return ExitUsage
	}
	return ExitError
}

// Dispatch dispatches one verb and returns its error (no rendering, no
// exit code) — nested subcommand tables reuse the same machinery: an
// outer verb's Run hands its remaining arguments to an inner App.Dispatch
// and gets the same unknown-verb error shape and flag parsing. args must
// contain at least one element (empty input is handled by the outer Run
// or the outer verb itself).
//
// When RootFlag is set but no root flag is present in args, an incoming
// Environment.Root is preserved (a caller may set it programmatically);
// if the flag appears more than once, the last occurrence wins.
func (a *App) Dispatch(ctx context.Context, env *Environment, args []string) error {
	cmd, ok := a.commands[args[0]]
	if !ok {
		return &UnknownVerbError{Name: args[0]}
	}
	rest := args[1:]
	e := *env
	if a.RootFlag != "" {
		if stripped, root, found := extractRootFlag(a.RootFlag, rest); found {
			rest, e.Root = stripped, root
		}
	}
	rest, err := a.ParseFlags(cmd, &e, rest)
	if err != nil {
		return err
	}
	return cmd.Run(ctx, &e, rest)
}

// SubDispatch dispatches a nested subcommand and rewrites the inner
// UnknownVerbError — the outer Run's usage-error decision (exit code 2
// plus help screen) only recognizes a miss at its own level; a subcommand
// miss is a command error of the inner table (exit code 1).
func (a *App) SubDispatch(ctx context.Context, env *Environment, args []string) error {
	err := a.Dispatch(ctx, env, args)
	var unknown *UnknownVerbError
	if errors.As(err, &unknown) {
		return fmt.Errorf("unknown subcommand %q", unknown.Name)
	}
	return err
}

// ParseFlags performs unified flag parsing for verbs implementing
// Flagged: build FlagSet → SetFlags declares → Parse → FlagError maps.
// It returns the positional arguments left after parsing (fs.Args()).
// The flag package's own diagnostics are silenced, making FlagError the
// single rendering point for parse failures. A parsed -h/-help prints
// the verb's usage to env.Stdout and returns ErrHelp. Identical to
// production dispatch; tests can consume it directly.
func (a *App) ParseFlags(cmd Command, env *Environment, rest []string) ([]string, error) {
	flagged, ok := cmd.(Flagged)
	if !ok {
		return rest, nil
	}
	fs := flag.NewFlagSet(cmd.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	flagged.SetFlags(fs)
	err := fs.Parse(rest)
	if errors.Is(err, flag.ErrHelp) {
		a.printVerbHelp(env.Stdout, cmd)
		return nil, ErrHelp
	}
	if err != nil {
		return nil, a.flagError(cmd.Name(), err)
	}
	return fs.Args(), nil
}

func (a *App) flagError(verb string, err error) error {
	if a.FlagError != nil {
		return a.FlagError(verb, err)
	}
	return fmt.Errorf("%s: bad arguments: %v", verb, err)
}

func (a *App) render(err error) string {
	if a.RenderError != nil {
		return a.RenderError(err)
	}
	return err.Error()
}

// extractRootFlag removes the global root flag (-X value / -X=value /
// --X=value) from the argument list, returning the remaining arguments
// and the value; found reports whether any form was present. A dangling
// -X with no following token counts as provided-but-empty and is left to
// the verb's own usage validation. Stripping stops at a "--" terminator:
// everything after it is literal.
func extractRootFlag(name string, args []string) (rest []string, value string, found bool) {
	short, long := "-"+name, "--"+name
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return args, "", false
		}
		if a == short || a == long {
			if i+1 >= len(args) {
				return args[:i:i], "", true
			}
			return append(args[:i:i], args[i+2:]...), args[i+1], true
		}
		if v, ok := strings.CutPrefix(a, short+"="); ok {
			return append(args[:i:i], args[i+1:]...), v, true
		}
		if v, ok := strings.CutPrefix(a, long+"="); ok {
			return append(args[:i:i], args[i+1:]...), v, true
		}
	}
	return args, "", false
}

// isHelpFlag reports whether arg is one of the conventional help flags
// accepted wherever a verb name is expected.
func isHelpFlag(arg string) bool {
	switch arg {
	case "-h", "--h", "-help", "--help":
		return true
	}
	return false
}

func (a *App) printHelp(w io.Writer) {
	if a.HelpHeader != "" {
		fmt.Fprintln(w, a.HelpHeader)
		fmt.Fprintln(w)
	}
	title := a.VerbTitle
	if title == "" {
		title = "commands:"
	}
	fmt.Fprintln(w, title)
	for _, n := range a.Names() {
		fmt.Fprintf(w, "  %-10s %s\n", n, a.commands[n].Synopsis())
	}
	if a.HelpFooter != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, a.HelpFooter)
	}
}

// printVerbHelp writes one verb's usage line and flag defaults — the help
// behind "help <verb>" and verb-level -h.
func (a *App) printVerbHelp(w io.Writer, cmd Command) {
	fmt.Fprintln(w, "usage:", cmd.Usage())
	flagged, ok := cmd.(Flagged)
	if !ok {
		return
	}
	fs := flag.NewFlagSet(cmd.Name(), flag.ContinueOnError)
	flagged.SetFlags(fs)
	declared := 0
	fs.VisitAll(func(*flag.Flag) { declared++ })
	if declared == 0 {
		return
	}
	fs.SetOutput(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "flags:")
	fs.PrintDefaults()
}
