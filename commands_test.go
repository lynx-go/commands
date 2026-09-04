package commands

// Contract tests for the framework: dispatch, declarative flags, root-flag
// stripping (any position, including before the verb name), nested
// subcommands, help screens, help/-h behavior, and exit codes. Standard
// library only, zero repo dependencies — the no-outgoing-dependency
// constraint is enforced by the CI dependency check
// (.github/workflows/ci.yml).

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"
)

// echoCmd is the minimal verb: echoes positional arguments, honors -upper.
type echoCmd struct {
	upper *bool
}

func (echoCmd) Name() string     { return "echo" }
func (echoCmd) Synopsis() string { return "echo arguments" }
func (echoCmd) Usage() string    { return "echo [-upper] words..." }

func (c *echoCmd) SetFlags(fs *flag.FlagSet) {
	c.upper = fs.Bool("upper", false, "uppercase the output")
}

func (c *echoCmd) Run(_ context.Context, env *Environment, args []string) error {
	text := strings.Join(args, " ")
	if *c.upper {
		text = strings.ToUpper(text)
	}
	env.Stdout.Write([]byte(text + "\n"))
	return nil
}

// bareCmd is a bare verb: it does not implement Flagged, so its arguments
// pass through untouched (unknown flags included).
type bareCmd struct{}

func (bareCmd) Name() string     { return "bare" }
func (bareCmd) Synopsis() string { return "no flags" }
func (bareCmd) Usage() string    { return "bare" }

func (bareCmd) Run(_ context.Context, env *Environment, args []string) error {
	env.Stdout.Write([]byte(strings.Join(args, ",") + "\n"))
	return nil
}

// stubCmd is a name-only verb for table-shape tests.
type stubCmd string

func (s stubCmd) Name() string     { return string(s) }
func (s stubCmd) Synopsis() string { return "stub " + string(s) }
func (s stubCmd) Usage() string    { return string(s) }

func (stubCmd) Run(_ context.Context, _ *Environment, _ []string) error { return nil }

func newTestApp() *App {
	app := New()
	app.RootFlag = "R"
	app.Register(&echoCmd{}, bareCmd{})
	return app
}

func TestDispatchFlagsAndPositionals(t *testing.T) {
	app := newTestApp()
	var out bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &out}
	if code := app.Run(context.Background(), env, []string{"echo", "-upper", "a", "b"}); code != ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := out.String(); got != "A B\n" {
		t.Fatalf("output = %q, want %q", got, "A B\n")
	}
}

func TestRootFlagStrippedAnywhere(t *testing.T) {
	// The root flag is stripped wherever it appears in the verb's
	// arguments; the verb sees only the remaining positionals.
	for _, tc := range []struct {
		args    []string // the part after the verb
		want    string
		wantPos string
	}{
		{[]string{"-R", "/root", "a", "b"}, "/root", "a|b"},
		{[]string{"a", "-R", "/root", "b"}, "/root", "a|b"},
		{[]string{"a", "b", "-R", "/root"}, "/root", "a|b"},
		{[]string{"-R=/root", "a"}, "/root", "a"},
		{[]string{"--R=/root", "a"}, "/root", "a"},
		{[]string{"a"}, "", "a"},
		{[]string{"-R"}, "", ""}, // dangling: counts as not provided
		// A "--" terminator ends stripping: the rest is literal.
		{[]string{"a", "--", "-R", "/root"}, "", "a|--|-R|/root"},
	} {
		var root, pos string
		app := New()
		app.RootFlag = "R"
		app.Register(posSpy{root: &root, pos: &pos})
		args := append([]string{"spy"}, tc.args...)
		var out bytes.Buffer
		if code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out}, args); code != ExitOK {
			t.Fatalf("%v: exit code = %d", tc.args, code)
		}
		if root != tc.want {
			t.Fatalf("%v: Root = %q, want %q", tc.args, root, tc.want)
		}
		if strings.Join(strings.Fields(pos), "|") != tc.wantPos {
			t.Fatalf("%v: positionals = %q, want %q", tc.args, pos, tc.wantPos)
		}
	}
}

// posSpy records the Root and positionals seen by Run.
type posSpy struct {
	root *string
	pos  *string
}

func (posSpy) Name() string     { return "spy" }
func (posSpy) Synopsis() string { return "record root" }
func (posSpy) Usage() string    { return "spy" }

func (r posSpy) Run(_ context.Context, env *Environment, args []string) error {
	*r.root = env.Root
	*r.pos = strings.Join(args, " ")
	return nil
}

func TestRootFlagBeforeVerb(t *testing.T) {
	// The README-documented form `-R /root echo hi` must dispatch to the
	// verb, not report unknown verb "-R".
	var root, pos string
	app := New()
	app.RootFlag = "R"
	app.Register(posSpy{root: &root, pos: &pos})
	var out bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &out}
	code := app.Run(context.Background(), env, []string{"-R", "/root", "spy", "a"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if root != "/root" {
		t.Fatalf("Root = %q, want /root", root)
	}
	if pos != "a" {
		t.Fatalf("positionals = %q, want %q", pos, "a")
	}
}

func TestRootFlagLastOccurrenceWins(t *testing.T) {
	var root, pos string
	app := New()
	app.RootFlag = "R"
	app.Register(posSpy{root: &root, pos: &pos})
	var out bytes.Buffer
	code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out},
		[]string{"-R", "/a", "spy", "-R", "/b", "x"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if root != "/b" {
		t.Fatalf("Root = %q, want /b (last occurrence wins)", root)
	}
	if pos != "x" {
		t.Fatalf("positionals = %q, want %q", pos, "x")
	}
}

func TestRootFlagAbsentPreservesEnvironmentRoot(t *testing.T) {
	// A caller may pre-set Root programmatically; an absent root flag must
	// not clobber it.
	var root string
	app := New()
	app.RootFlag = "R"
	app.Register(posSpy{root: &root, pos: new(string)})
	var out bytes.Buffer
	code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out, Root: "/preset"}, []string{"spy", "a"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if root != "/preset" {
		t.Fatalf("Root = %q, want /preset (preserved)", root)
	}
}

func TestUnknownVerbIsUsageErrorWithHelp(t *testing.T) {
	app := newTestApp()
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}
	code := app.Run(context.Background(), env, []string{"nope"})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), `unknown verb "nope"`) {
		t.Fatalf("stderr = %q, want unknown-verb message", errOut.String())
	}
	if !strings.Contains(errOut.String(), "echo") {
		t.Fatal("unknown verb should append the help screen (verb list)")
	}
	var unknown *UnknownVerbError
	if !errors.As(app.Dispatch(context.Background(), env, []string{"nope"}), &unknown) || unknown.Name != "nope" {
		t.Fatal("Dispatch did not return UnknownVerbError")
	}
}

func TestFlagErrorRendersSingleLine(t *testing.T) {
	// The flag package's own diagnostics are silenced; the mapped FlagError
	// line is the single rendering point on stderr. Since v0.2.0 the
	// default mapping is a UsageError: the verb's usage line is appended
	// and the exit code is 2 (a parse failure is a usage problem).
	app := newTestApp()
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}
	code := app.Run(context.Background(), env, []string{"echo", "--bogus"})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
	want := "echo: bad arguments: flag provided but not defined: -bogus\nusage: echo [-upper] words...\n"
	if errOut.String() != want {
		t.Fatalf("stderr = %q, want exactly %q", errOut.String(), want)
	}
	if out.String() != "" {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestFlagErrorHook(t *testing.T) {
	app := newTestApp()
	app.FlagError = func(verb string, err error) error {
		return errors.New("custom:" + verb)
	}
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}
	if code := app.Run(context.Background(), env, []string{"echo", "--bogus"}); code != ExitError {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if errOut.String() != "custom:echo\n" {
		t.Fatalf("FlagError hook not in effect, stderr = %q", errOut.String())
	}
}

func TestBareCommandIgnoresUnknownFlags(t *testing.T) {
	app := newTestApp()
	var out bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &out}
	if code := app.Run(context.Background(), env, []string{"bare", "--whatever", "x"}); code != ExitOK {
		t.Fatalf("bare verb should pass unknown arguments through, exit code = %d", code)
	}
	if got := out.String(); got != "--whatever,x\n" {
		t.Fatalf("passthrough = %q", got)
	}
}

func TestHelpCommand(t *testing.T) {
	app := newTestApp()
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}

	t.Run("general", func(t *testing.T) {
		out.Reset()
		if code := app.Run(context.Background(), env, []string{"help"}); code != ExitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		for _, want := range []string{"commands:", "echo", "bare"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("help screen missing %q: %q", want, out.String())
			}
		}
	})

	t.Run("per verb", func(t *testing.T) {
		out.Reset()
		if code := app.Run(context.Background(), env, []string{"help", "echo"}); code != ExitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		for _, want := range []string{"usage: echo [-upper] words...", "-upper", "uppercase the output"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("verb help missing %q: %q", want, out.String())
			}
		}
		if errOut.String() != "" {
			t.Fatalf("stderr = %q, want empty", errOut.String())
		}
	})

	t.Run("unknown verb", func(t *testing.T) {
		out.Reset()
		errOut.Reset()
		code := app.Run(context.Background(), env, []string{"help", "bogus"})
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want 2", code)
		}
		if !strings.Contains(errOut.String(), `unknown verb "bogus"`) {
			t.Fatalf("stderr = %q, want unknown-verb message", errOut.String())
		}
		if !strings.Contains(errOut.String(), "commands:") {
			t.Fatal("help <unknown> should append the help screen")
		}
	})
}

func TestHelpFlag(t *testing.T) {
	app := newTestApp()
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}

	for _, arg := range []string{"-h", "-help", "--help"} {
		out.Reset()
		if code := app.Run(context.Background(), env, []string{arg}); code != ExitOK {
			t.Fatalf("%s: exit code = %d, want 0", arg, code)
		}
		if !strings.Contains(out.String(), "commands:") {
			t.Fatalf("%s: general help missing on stdout: %q", arg, out.String())
		}
	}

	t.Run("per verb", func(t *testing.T) {
		out.Reset()
		errOut.Reset()
		if code := app.Run(context.Background(), env, []string{"echo", "-h"}); code != ExitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if !strings.Contains(out.String(), "usage: echo [-upper] words...") {
			t.Fatalf("verb help missing on stdout: %q", out.String())
		}
		if errOut.String() != "" {
			t.Fatalf("stderr = %q, want empty", errOut.String())
		}
	})

	t.Run("after positional passes through", func(t *testing.T) {
		// Standard flag semantics: -h is recognized only before the first
		// positional, so it passes through as an argument here.
		out.Reset()
		if code := app.Run(context.Background(), env, []string{"echo", "hi", "-h"}); code != ExitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if got := out.String(); got != "hi -h\n" {
			t.Fatalf("output = %q, want passthrough %q", got, "hi -h\n")
		}
	})
}

func TestErrHelpFromParseFlags(t *testing.T) {
	app := newTestApp()
	echo, _ := app.Lookup("echo")
	var out bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &out}

	if _, err := app.ParseFlags(echo, env, []string{"-h"}); !errors.Is(err, ErrHelp) {
		t.Fatalf("ParseFlags(-h) error = %v, want ErrHelp", err)
	}
	if !strings.Contains(out.String(), "usage: echo") {
		t.Fatalf("verb help missing on stdout: %q", out.String())
	}

	rest, err := app.ParseFlags(echo, env, []string{"-upper", "x"})
	if err != nil || strings.Join(rest, ",") != "x" {
		t.Fatalf("ParseFlags positionals = %v, err = %v", rest, err)
	}

	bare, _ := app.Lookup("bare")
	rest, err = app.ParseFlags(bare, env, []string{"--whatever"})
	if err != nil || strings.Join(rest, ",") != "--whatever" {
		t.Fatalf("bare ParseFlags passthrough = %v, err = %v", rest, err)
	}
}

// parentCmd demonstrates nested subcommands: the inner App reuses the
// Dispatch machinery.
type parentCmd struct{ subs *App }

func (parentCmd) Name() string     { return "parent" }
func (parentCmd) Synopsis() string { return "nested" }
func (parentCmd) Usage() string    { return "parent <subcommand>" }

func (p *parentCmd) Run(ctx context.Context, env *Environment, args []string) error {
	if len(args) == 0 {
		return errors.New("parent: subcommand required")
	}
	return p.subs.SubDispatch(ctx, env, args)
}

func TestNestedSubcommands(t *testing.T) {
	subs := New()
	subs.Register(&echoCmd{}, bareCmd{})
	app := New()
	app.RootFlag = "R"
	app.Register(&parentCmd{subs: subs})
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}

	if code := app.Run(context.Background(), env, []string{"parent", "echo", "-upper", "hi"}); code != ExitOK {
		t.Fatalf("nested dispatch exit code = %d", code)
	}
	if got := out.String(); got != "HI\n" {
		t.Fatalf("nested flag parsing broken: %q", got)
	}

	// ErrHelp crosses the nesting boundary as success.
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), env, []string{"parent", "echo", "-h"}); code != ExitOK {
		t.Fatalf("nested -h exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "usage: echo") {
		t.Fatalf("nested verb help missing: %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	code := app.Run(context.Background(), env, []string{"parent", "nope"})
	if code != ExitError {
		t.Fatalf("nested unknown subcommand exit code = %d, want 1 (a subcommand miss is not an outer usage error)", code)
	}
	if !strings.Contains(errOut.String(), `unknown subcommand "nope"`) {
		t.Fatalf("stderr = %q, want unknown-subcommand message", errOut.String())
	}
	if strings.Contains(errOut.String(), "commands:") {
		t.Fatal("nested unknown subcommand must not append the outer help screen")
	}
	if err := subs.SubDispatch(context.Background(), env, []string{"nope"}); err == nil || err.Error() != `unknown subcommand "nope"` {
		t.Fatalf("SubDispatch error = %v, want unknown subcommand %q", err, "nope")
	}
}

// boolSpy records the RootBools (copied), the Root, and the positionals
// seen by Run. root may be nil.
type boolSpy struct {
	booleans map[string]bool
	root     *string
	pos      *string
}

func (boolSpy) Name() string     { return "spy" }
func (boolSpy) Synopsis() string { return "record bools" }
func (boolSpy) Usage() string    { return "spy" }

func (b boolSpy) Run(_ context.Context, env *Environment, args []string) error {
	for k, v := range env.RootBools {
		b.booleans[k] = v
	}
	if b.root != nil {
		*b.root = env.Root
	}
	*b.pos = strings.Join(args, " ")
	return nil
}

func TestRootBoolFlags(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantJSON bool
		wantSet  bool // whether RootBools has the key at all
		wantPos  string
	}{
		{"before verb", []string{"--json", "spy", "a"}, true, true, "a"},
		{"after verb", []string{"spy", "--json", "a"}, true, true, "a"},
		{"short form", []string{"spy", "-json", "a"}, true, true, "a"},
		{"explicit true", []string{"spy", "--json=true", "a"}, true, true, "a"},
		{"explicit false", []string{"spy", "--json=false", "a"}, false, true, "a"},
		{"last wins", []string{"spy", "--json=false", "--json", "a"}, true, true, "a"},
		{"absent", []string{"spy", "a"}, false, false, "a"},
		{"unparsable value passes through", []string{"spy", "--json=yes", "a"}, false, false, "--json=yes|a"},
		{"empty value passes through", []string{"spy", "--json=", "a"}, false, false, "--json=|a"},
		{"terminator is literal", []string{"spy", "a", "--", "--json"}, false, false, "a|--|--json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saw := map[string]bool{}
			pos := ""
			app := New()
			app.RootBoolFlags = []string{"json"}
			app.Register(boolSpy{booleans: saw, pos: &pos})
			var out bytes.Buffer
			code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out}, tc.args)
			if code != ExitOK {
				t.Fatalf("exit code = %d, want 0", code)
			}
			// A key is present only when the flag was provided: provided
			// false (wantSet, !wantJSON) must stay distinct from absent.
			got, set := saw["json"]
			if set != tc.wantSet {
				t.Fatalf("RootBools json key present = %v, want %v (saw %v)", set, tc.wantSet, saw)
			}
			if set && got != tc.wantJSON {
				t.Fatalf("RootBools[json] = %v, want %v", got, tc.wantJSON)
			}
			if tc.wantPos != "" && strings.Join(strings.Fields(pos), "|") != tc.wantPos {
				t.Fatalf("positionals = %q, want %q", pos, tc.wantPos)
			}
		})
	}
}

func TestRootBoolFlagsMergeWithPreset(t *testing.T) {
	// A caller may pre-set RootBools programmatically; absent flags must
	// not clobber it, and a provided flag must not mutate the caller's map.
	preset := map[string]bool{"verbose": true}
	saw := map[string]bool{}
	pos := ""
	app := New()
	app.RootBoolFlags = []string{"json"}
	app.Register(boolSpy{booleans: saw, pos: &pos})
	var out bytes.Buffer
	code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out, RootBools: preset}, []string{"spy", "--json"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !preset["verbose"] || len(preset) != 1 {
		t.Fatalf("caller's RootBools mutated: %v", preset)
	}
}

func TestRootBoolFlagEmptyValueStaysInPlace(t *testing.T) {
	// The empty "=value" does not parse as a boolean, so the token stays
	// in place per the documented contract — wherever it appears.
	app := New()
	app.RootBoolFlags = []string{"json"}
	app.Register(&echoCmd{})
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}

	// Before the verb it is an ordinary token: a dispatch miss.
	code := app.Run(context.Background(), env, []string{"--json=", "echo", "hi"})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), `unknown verb "--json="`) {
		t.Fatalf("stderr = %q, want unknown-verb message", errOut.String())
	}

	// After the verb, verb-level parsing owns the rejection.
	errOut.Reset()
	code = app.Run(context.Background(), env, []string{"echo", "--json=", "hi"})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "flag provided but not defined: -json") {
		t.Fatalf("stderr = %q, want the verb-level flag error", errOut.String())
	}
}

func TestRootFlagValueConsumesDeclaredBoolFlag(t *testing.T) {
	// A value-carrying -R blindly takes the next token, even when it is a
	// declared bool root flag (v0.1.0 semantics, kept by the combined
	// pass): the bool flag is not set.
	var root string
	saw := map[string]bool{}
	pos := ""
	app := New()
	app.RootFlag = "R"
	app.RootBoolFlags = []string{"json"}
	app.Register(boolSpy{booleans: saw, root: &root, pos: &pos})
	var out bytes.Buffer
	code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out},
		[]string{"-R", "--json", "spy", "a"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if root != "--json" {
		t.Fatalf("Root = %q, want %q (blind next-token consumption)", root, "--json")
	}
	if len(saw) != 0 {
		t.Fatalf("RootBools = %v, want empty (the token was consumed as -R's value)", saw)
	}
	if pos != "a" {
		t.Fatalf("positionals = %q, want %q", pos, "a")
	}
}

func TestRootConfigValidationPanics(t *testing.T) {
	app := New()
	app.RootBoolFlags = []string{"json", "json"}
	mustPanic(t, "duplicate root flag", func() {
		app.Run(context.Background(), &Environment{}, []string{"spy"})
	})
	app2 := New()
	app2.RootFlag = "json"
	app2.RootBoolFlags = []string{"json"}
	mustPanic(t, "duplicate root flag", func() {
		app2.Run(context.Background(), &Environment{}, []string{"spy"})
	})
	app3 := New()
	app3.RootBoolFlags = []string{""}
	mustPanic(t, "empty root bool flag name", func() {
		app3.Run(context.Background(), &Environment{}, []string{"spy"})
	})
}

// usageCmd returns a UsageError, optionally wrapped the way real verbs
// report through layers (fmt.Errorf("%w")) and optionally without a usage
// hint.
type usageCmd struct {
	hint bool // include the usage hint
	wrap bool // wrap in fmt.Errorf("%w") before returning
}

func (usageCmd) Name() string     { return "fussy" }
func (usageCmd) Synopsis() string { return "validates its arguments" }
func (usageCmd) Usage() string    { return "fussy --config FILE" }
func (u usageCmd) Run(_ context.Context, _ *Environment, _ []string) error {
	err := error(&UsageError{Err: errors.New("fussy: --config is required")})
	if u.hint {
		err = &UsageError{Usage: "fussy --config FILE", Err: errors.New("fussy: --config is required")}
	}
	if u.wrap {
		return fmt.Errorf("while running: %w", err)
	}
	return err
}

func TestUsageErrorExitsTwoWithHint(t *testing.T) {
	app := newTestApp()
	app.Register(usageCmd{hint: true})
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}
	code := app.Run(context.Background(), env, []string{"fussy"})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
	want := "fussy: --config is required\nusage: fussy --config FILE\n"
	if errOut.String() != want {
		t.Fatalf("stderr = %q, want exactly %q", errOut.String(), want)
	}
}

func TestUsageErrorWrappedAndEmptyVerb(t *testing.T) {
	app := newTestApp()
	app.Register(usageCmd{wrap: true})
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}
	// Wrap the verb error the way real verbs do (fmt.Errorf("%w")): the
	// UsageError must still be detected through the chain; the empty Verb
	// means exit 2 without a usage hint.
	code := app.Run(context.Background(), env, []string{"fussy"})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if errOut.String() != "while running: fussy: --config is required\n" {
		t.Fatalf("stderr = %q, want the wrapped error without a hint", errOut.String())
	}
}

func TestUsageErrorCrossesNesting(t *testing.T) {
	// An inner verb's UsageError passes through SubDispatch untouched: the
	// outer Run still exits 2 with the verb's usage hint.
	subs := New()
	subs.Register(usageCmd{hint: true})
	app := New()
	app.RootFlag = "R"
	app.Register(&parentCmd{subs: subs})
	var out, errOut bytes.Buffer
	code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &errOut}, []string{"parent", "fussy"})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage: fussy --config FILE") {
		t.Fatalf("stderr = %q, want the inner verb's usage hint", errOut.String())
	}
}

func TestHelpComposition(t *testing.T) {
	app := New()
	app.HelpHeader = "header"
	app.HelpFooter = "footer"
	app.Register(bareCmd{})
	var out bytes.Buffer
	if code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out}, nil); code != ExitOK {
		t.Fatalf("empty-args exit code = %d", code)
	}
	help := out.String()
	for _, want := range []string{"header", "commands:", "bare", "footer"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help screen missing %q: %q", want, help)
		}
	}
}

func TestNamesSorted(t *testing.T) {
	app := New()
	app.Register(stubCmd("c"), stubCmd("a"), stubCmd("b"))
	got := strings.Join(app.Names(), ",")
	if got != "a,b,c" {
		t.Fatalf("Names() = %q, want %q", got, "a,b,c")
	}
}

func TestRegisterPanics(t *testing.T) {
	mustPanic(t, "reserved", func() { New().Register(stubCmd("help")) })
	app := New()
	app.Register(stubCmd("echo"))
	mustPanic(t, "duplicate", func() { app.Register(stubCmd("echo")) })
}

func mustPanic(t *testing.T, wantSubstring string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic containing %q, got none", wantSubstring)
		}
		if s, ok := r.(string); !ok || !strings.Contains(s, wantSubstring) {
			t.Fatalf("panic = %v, want containing %q", r, wantSubstring)
		}
	}()
	f()
}
