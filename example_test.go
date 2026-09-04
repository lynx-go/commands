package commands

// Runnable documentation: the quick-start app from the README, executed by
// go test and rendered on pkg.go.dev.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

type exampleEcho struct{ upper bool }

func (*exampleEcho) Name() string     { return "echo" }
func (*exampleEcho) Synopsis() string { return "print its arguments" }
func (*exampleEcho) Usage() string    { return "echo [-upper] words..." }

func (c *exampleEcho) SetFlags(fs *flag.FlagSet) {
	fs.BoolVar(&c.upper, "upper", false, "uppercase the output")
}

func (c *exampleEcho) Run(_ context.Context, env *Environment, args []string) error {
	out := strings.Join(args, " ")
	if c.upper {
		out = strings.ToUpper(out)
	}
	fmt.Fprintln(env.Stdout, out)
	return nil
}

func Example() {
	app := New()
	app.RootFlag = "R" // accept -R/--R anywhere, value lands in Environment.Root
	app.Register(&exampleEcho{})
	env := &Environment{Stdout: os.Stdout, Stderr: os.Stderr}
	fmt.Println("exit:", app.Run(context.Background(), env, []string{"-R", "/root", "echo", "-upper", "hello", "world"}))
	// Output: HELLO WORLD
	// exit: 0
}

func ExampleApp_Run() {
	app := New()
	app.Register(bareCmd{})
	env := &Environment{Stdout: os.Stdout, Stderr: os.Stderr}
	fmt.Println("exit:", app.Run(context.Background(), env, []string{"help", "bare"}))
	// Output: usage: bare
	// exit: 0
}
