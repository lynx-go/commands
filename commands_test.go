package commands

// 框架自带契约测试：分发/声明式旗标/根旗标剥离/嵌套子动词/帮助面/退出码。
// 纯标准库、零仓库依赖——本包的零出边约束由 depcheck 门禁另行强制。

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
)

// echoCmd 最小动词：回显位置参数与旗标值。
type echoCmd struct {
	upper *bool
}

func (echoCmd) Name() string     { return "echo" }
func (echoCmd) Synopsis() string { return "回显参数" }
func (echoCmd) Usage() string    { return "echo [-upper] 文本…" }

func (c *echoCmd) SetFlags(fs *flag.FlagSet) {
	c.upper = fs.Bool("upper", false, "大写")
}

func (c *echoCmd) Run(_ context.Context, env *Environment, args []string) error {
	text := strings.Join(args, " ")
	if *c.upper {
		text = strings.ToUpper(text)
	}
	env.Stdout.Write([]byte(text + "\n"))
	return nil
}

// bareCmd 无旗主动词：不实现 Flagged，参数原样透传（未知旗标不报错）。
type bareCmd struct{}

func (bareCmd) Name() string     { return "bare" }
func (bareCmd) Synopsis() string { return "无旗标" }
func (bareCmd) Usage() string    { return "bare" }

func (bareCmd) Run(_ context.Context, env *Environment, args []string) error {
	env.Stdout.Write([]byte(strings.Join(args, ",") + "\n"))
	return nil
}

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
		t.Fatalf("退出码 = %d，期望 0", code)
	}
	if got := out.String(); got != "A B\n" {
		t.Fatalf("输出 = %q，期望 %q", got, "A B\n")
	}
}

func TestRootFlagStrippedAnywhere(t *testing.T) {
	// 根旗标在位置序列任意处都被剥离，动词本体只见剩余位置参数。
	for _, tc := range []struct {
		args    []string // spy 之后的部分
		want    string
		wantPos string
	}{
		{[]string{"-R", "/root", "a", "b"}, "/root", "a|b"},
		{[]string{"a", "-R", "/root", "b"}, "/root", "a|b"},
		{[]string{"a", "b", "-R", "/root"}, "/root", "a|b"},
		{[]string{"-R=/root", "a"}, "/root", "a"},
		{[]string{"--R=/root", "a"}, "/root", "a"},
		{[]string{"a"}, "", "a"},
		{[]string{"-R"}, "", ""}, // 悬空无值按未提供
	} {
		var root, pos string
		spy := posSpy{root: &root, pos: &pos}
		app := New()
		app.RootFlag = "R"
		app.Register(spy)
		args := append([]string{"spy"}, tc.args...)
		var out bytes.Buffer
		if code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out}, args); code != ExitOK {
			t.Fatalf("%v: 退出码 = %d", tc.args, code)
		}
		if root != tc.want {
			t.Fatalf("%v: Root = %q，期望 %q", tc.args, root, tc.want)
		}
		if strings.Join(strings.Fields(pos), "|") != tc.wantPos {
			t.Fatalf("%v: 位置参数 = %q，期望 %q", tc.args, pos, tc.wantPos)
		}
	}
}

// posSpy 记录 Run 所见 Root 与位置参数。
type posSpy struct {
	root *string
	pos  *string
}

func (posSpy) Name() string     { return "spy" }
func (posSpy) Synopsis() string { return "记录根" }
func (posSpy) Usage() string    { return "spy" }

func (r posSpy) Run(_ context.Context, env *Environment, args []string) error {
	*r.root = env.Root
	*r.pos = strings.Join(args, " ")
	return nil
}

// rootSpy 记录 Run 所见 Environment.Root。
type rootSpy struct{ root *string }

func (rootSpy) Name() string     { return "spy" }
func (rootSpy) Synopsis() string { return "记录根" }
func (rootSpy) Usage() string    { return "spy" }

func (r rootSpy) Run(_ context.Context, env *Environment, _ []string) error {
	*r.root = env.Root
	return nil
}

func TestRootFlagLandsInEnvironment(t *testing.T) {
	app := New()
	app.RootFlag = "R"
	var root, pos string
	app.Register(posSpy{root: &root, pos: &pos})
	var out bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &out}
	if code := app.Run(context.Background(), env, []string{"spy", "-R", "/data"}); code != ExitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if root != "/data" {
		t.Fatalf("Root = %q，期望 /data", root)
	}
}

func TestUnknownVerbIsUsageErrorWithHelp(t *testing.T) {
	app := newTestApp()
	var out, errOut bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &errOut}
	code := app.Run(context.Background(), env, []string{"nope"})
	if code != ExitUsage {
		t.Fatalf("退出码 = %d，期望 2", code)
	}
	var unknown *UnknownVerbError
	if !errors.As(app.Dispatch(context.Background(), env, []string{"nope"}), &unknown) || unknown.Name != "nope" {
		t.Fatal("Dispatch 未返回 UnknownVerbError")
	}
	if !strings.Contains(errOut.String(), "echo") {
		t.Fatal("未知动词应附帮助面（含动词列表）")
	}
}

func TestFlagErrorHookAndDefault(t *testing.T) {
	var out bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &out}
	app := newTestApp()
	if code := app.Run(context.Background(), env, []string{"echo", "--bogus"}); code != ExitError {
		t.Fatalf("退出码 = %d，期望 1", code)
	}
	if !strings.Contains(out.String(), "echo: 参数错误") {
		t.Fatalf("缺省 FlagError 形态缺失: %q", out.String())
	}

	app.FlagError = func(verb string, err error) error {
		return errors.New("custom:" + verb)
	}
	out.Reset()
	_ = app.Run(context.Background(), env, []string{"echo", "--bogus"})
	if !strings.Contains(out.String(), "custom:echo") {
		t.Fatalf("FlagError 钩子未生效: %q", out.String())
	}
}

func TestBareCommandIgnoresUnknownFlags(t *testing.T) {
	app := newTestApp()
	var out bytes.Buffer
	env := &Environment{Stdout: &out, Stderr: &out}
	if code := app.Run(context.Background(), env, []string{"bare", "--whatever", "x"}); code != ExitOK {
		t.Fatalf("无旗主动词应透传未知参数，退出码 = %d", code)
	}
	if got := out.String(); got != "--whatever,x\n" {
		t.Fatalf("透传形态 = %q", got)
	}
}

// parentCmd 演示嵌套子动词：内层 App 复用 Dispatch 机器。
type parentCmd struct{ subs *App }

func (parentCmd) Name() string     { return "parent" }
func (parentCmd) Synopsis() string { return "嵌套" }
func (parentCmd) Usage() string    { return "parent <子动词>" }

func (p *parentCmd) Run(ctx context.Context, env *Environment, args []string) error {
	if len(args) == 0 {
		return errors.New("parent: 需要子动词")
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
		t.Fatalf("嵌套分发退出码 = %d", code)
	}
	if got := out.String(); got != "HI\n" {
		t.Fatalf("嵌套旗标解析失效: %q", got)
	}

	out.Reset()
	errOut.Reset()
	code := app.Run(context.Background(), env, []string{"parent", "nope"})
	if code != ExitError {
		t.Fatalf("嵌套未知子动词退出码 = %d，期望 1（子动词未命中不是外层用法错误）", code)
	}
	if strings.Contains(errOut.String(), "commands:") {
		t.Fatal("嵌套未知子动词不应附外层帮助面")
	}
}

func TestHelpComposition(t *testing.T) {
	app := New()
	app.HelpHeader = "header"
	app.HelpFooter = "footer"
	app.Register(bareCmd{})
	var out bytes.Buffer
	if code := app.Run(context.Background(), &Environment{Stdout: &out, Stderr: &out}, nil); code != ExitOK {
		t.Fatalf("空参退出码 = %d", code)
	}
	help := out.String()
	for _, want := range []string{"header", "commands:", "bare", "footer"} {
		if !strings.Contains(help, want) {
			t.Fatalf("帮助面缺 %q: %q", want, help)
		}
	}
}
