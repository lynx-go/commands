// Package commands is a small, zero-dependency framework for building
// subcommand-style CLIs: verb registration and dispatch, declarative flag
// parsing (Flagged/SetFlags), nested subcommand reuse (Dispatch),
// configurable global root flags (a value-carrying RootFlag plus optional
// valueless bool RootBoolFlags), verb-declared usage errors (UsageError →
// exit 2 with a usage hint), and hooks for error rendering. It uses only
// the standard library — error copy, help text, and the root-flag names
// are product decisions injected by the consumer at assembly time.
package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Environment is the execution environment of one command (injectable in
// tests). Root is the landing spot of the global root flag (App.RootFlag,
// e.g. "R") — the framework extracts it without interpreting it; its
// meaning (repository root, workspace root, …) is defined by the verb.
// Empty means "not provided". RootBools holds the parsed values of the
// global bool root flags (App.RootBoolFlags, e.g. "json"): a key is
// present only when the flag was provided; read it as env.RootBools[name]
// (a missing key reads as false).
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer
	Root   string

	// RootBools is nil until a bool root flag is provided (or a caller
	// pre-sets it programmatically; Dispatch preserves preset values).
	RootBools map[string]bool
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
// (unknown verb, flag parse failure, or a verb-declared UsageError).
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

// UsageError marks a verb-level usage problem — a missing required flag, a
// bad positional — as distinct from a command failure: App.Run renders it
// to stderr like any error and, when Usage is non-empty, appends it as a
// "usage:" hint line, then exits with ExitUsage (2). Carrying the usage
// string (not a verb name) keeps the hint intact across nested dispatch —
// the verb knows its own Usage() at construction. Wrap it (or return it
// directly) from Run for argument-validation failures; a non-nil Err is
// required. Usage may be empty to exit 2 without a hint.
type UsageError struct {
	Usage string
	Err   error
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

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

	// RootBoolFlags are global valueless bool flags ("json" ⇒ matches
	// -json/--json anywhere, landing in Environment.RootBools["json"];
	// -json=false/--json=false also parse and set false). Like RootFlag
	// they are stripped wherever they appear, last occurrence wins, and a
	// "--" terminator ends stripping; an =value that does not parse as a
	// boolean — including the empty value ("--json=") — is left in place
	// for verb-level parsing. Nested inner apps should leave it empty.
	// Run panics on an empty name, a duplicate entry, or a collision with
	// RootFlag — all assembly-time bugs.
	RootBoolFlags []string

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
	a.validateRoot()
	e := *env
	args, e = a.stripRootEnv(args, e)
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
	var usage *UsageError
	if errors.As(err, &usage) {
		if usage.Usage != "" {
			fmt.Fprintln(e.Stderr, "usage:", usage.Usage)
		}
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
	rest, e = a.stripRootEnv(rest, e)
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
		return nil, a.flagError(cmd, err)
	}
	return fs.Args(), nil
}

func (a *App) flagError(cmd Command, err error) error {
	if a.FlagError != nil {
		return a.FlagError(cmd.Name(), err)
	}
	return &UsageError{
		Usage: cmd.Usage(),
		Err:   fmt.Errorf("%s: bad arguments: %v", cmd.Name(), err),
	}
}

func (a *App) render(err error) string {
	if a.RenderError != nil {
		return a.RenderError(err)
	}
	return err.Error()
}

// validateRoot fails fast on root-flag configuration bugs: an empty or
// duplicate bool root-flag name, or a name colliding with RootFlag. Run
// calls it once at the top level, mirroring Register's assembly-time
// panics.
func (a *App) validateRoot() {
	seen := map[string]bool{}
	if a.RootFlag != "" {
		seen[a.RootFlag] = true
	}
	for _, n := range a.RootBoolFlags {
		if n == "" {
			panic("commands: empty root bool flag name")
		}
		if n == "h" || n == "help" {
			panic(fmt.Sprintf("commands: root bool flag %q collides with the reserved help flag", n))
		}
		if seen[n] {
			panic(fmt.Sprintf("commands: duplicate root flag %q", n))
		}
		seen[n] = true
	}
}

// stripRootEnv strips the configured root flags (RootFlag's value form and
// RootBoolFlags' bool forms) from args in one pass, writing the parsed
// values into a copy of env and returning the remaining arguments with it.
func (a *App) stripRootEnv(args []string, e Environment) ([]string, Environment) {
	if a.RootFlag == "" && len(a.RootBoolFlags) == 0 {
		return args, e
	}
	bools := map[string]bool{}
	for _, n := range a.RootBoolFlags {
		bools[n] = false
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--" {
			return append(out, args[i:]...), e
		}
		if name, val, hasValue, ok := splitFlag(tok); ok {
			if _, isBool := bools[name]; isBool {
				if !hasValue {
					// Valueless form: -json / --json mean true.
					e.RootBools = setBool(e.RootBools, name, true)
					continue
				}
				if b, err := strconv.ParseBool(val); err == nil {
					e.RootBools = setBool(e.RootBools, name, b)
					continue
				}
				// An =value that does not parse as a boolean — including
				// the empty one ("--json=") — stays in place: verb-level
				// parsing owns the error message.
				out = append(out, tok)
				continue
			}
		}
		if a.RootFlag != "" {
			short, long := "-"+a.RootFlag, "--"+a.RootFlag
			if tok == short || tok == long {
				if i+1 >= len(args) {
					// Dangling: counts as provided-but-empty and stops the
					// scan (same as v0.1.0), leaving validation to the verb.
					e.Root = ""
					return out, e
				}
				i++
				e.Root = args[i]
				continue
			}
			if v, ok := strings.CutPrefix(tok, short+"="); ok {
				e.Root = v
				continue
			}
			if v, ok := strings.CutPrefix(tok, long+"="); ok {
				e.Root = v
				continue
			}
		}
		out = append(out, tok)
	}
	return out, e
}

// setBool returns an updated RootBools map without mutating the incoming
// one (a preset Environment belongs to the caller).
func setBool(booleans map[string]bool, name string, value bool) map[string]bool {
	out := make(map[string]bool, len(booleans)+1)
	for k, v := range booleans {
		out[k] = v
	}
	out[name] = value
	return out
}

// splitFlag splits a "-name" / "--name" token into its name and value;
// hasValue reports whether an "=value" suffix was present — "-flag=" is a
// present-but-empty value, distinct from the valueless "-flag".
func splitFlag(tok string) (name, value string, hasValue, ok bool) {
	s := tok
	switch {
	case strings.HasPrefix(s, "--"):
		s = s[2:]
	case strings.HasPrefix(s, "-"):
		s = s[1:]
	default:
		return "", "", false, false
	}
	name, value, hasValue = strings.Cut(s, "=")
	return name, value, hasValue, true
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
