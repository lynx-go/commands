// Package commands 是通用子命令 CLI 框架：动词注册/分发、声明式旗标解析
// （Flagged/SetFlags）、嵌套子动词复用（Dispatch）、可配置全局根旗标与错误
// 渲染钩子。零业务依赖（纯标准库）——错误包络形态、帮助文案、根旗标名等
// 产品语义由消费方在装配期注入。
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

// Environment 是一次命令执行的运行环境（测试可注入）。Root 是全局根旗标
// （App.RootFlag，如 "R"）的落点——框架只摘取不解释，语义（仓库根/工作区
// 根）由动词定义；空 = 未提供。
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer
	Root   string
}

// Command 是子动词契约。Run 返回的 error 由 App.Run 经 RenderError 渲染到
// stderr 并以退出码 1 结束（渲染与退出策略可经钩子定制）。
type Command interface {
	Name() string
	Synopsis() string
	Usage() string
	Run(ctx context.Context, env *Environment, args []string) error
}

// Flagged 由带旗标的动词实现：SetFlags 声明式注册旗标（指针存动词结构体
// 字段，Run 读字段），FlagSet 构造与 Parse 错误映射由 App 单点承担——消息
// 形态不再靠每动词样板复制。未实现者参数原样透传（无旗主动词忽略未知参数
// 的语义不动）。
type Flagged interface {
	SetFlags(fs *flag.FlagSet)
}

// 退出码约定：0 成功；1 命令错误；2 用法错误（未知动词）。
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// UnknownVerbError 是分发未命中动词（含嵌套子动词）的错误形态。App.Run
// 据此选用法退出码并附帮助面；文案经 RenderError 钩子可重写（产品口径）。
type UnknownVerbError struct{ Name string }

func (e *UnknownVerbError) Error() string { return fmt.Sprintf("unknown verb %q", e.Name) }

// App 持有已注册动词。零值可用；New 给出缺省钩子形态。
type App struct {
	commands map[string]Command

	// RootFlag 是全局根旗标名（"R" ⇒ 匹配 -R/--R/-R=/--R=，任意位置预剥离，
	// 值落 Environment.Root）。空 = 不剥离。嵌套子动词表应置空——外层已剥离。
	RootFlag string

	// FlagError 把旗标解析失败映射为业务错误。缺省 "%s: 参数错误: %v"。
	FlagError func(verb string, err error) error

	// RenderError 把动词错误渲染为 stderr 单行文案。缺省 err.Error()。
	RenderError func(error) string

	// HelpHeader / VerbTitle / HelpFooter 组装帮助面（printHelp）。
	HelpHeader string
	VerbTitle  string
	HelpFooter string
}

// New 建空表框架实例（动词标题缺省 "commands:"）。
func New() *App {
	return &App{
		commands:  map[string]Command{},
		VerbTitle: "commands:",
	}
}

func (a *App) Register(cmds ...Command) {
	if a.commands == nil {
		a.commands = map[string]Command{}
	}
	for _, c := range cmds {
		a.commands[c.Name()] = c
	}
}

func (a *App) Lookup(name string) (Command, bool) {
	c, ok := a.commands[name]
	return c, ok
}

// Names 返回全部动词名（字典序——帮助面顺序与注册序无关）。
func (a *App) Names() []string {
	names := make([]string, 0, len(a.commands))
	for n := range a.commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Run 是进程顶层入口：空参数/help 打帮助返回 0；未知动词渲染错误并附帮助
// 面、退出码 2；其余错误渲染后退出码 1。
func (a *App) Run(ctx context.Context, env *Environment, args []string) int {
	if len(args) == 0 || args[0] == "help" {
		a.printHelp(env.Stdout)
		return ExitOK
	}
	err := a.Dispatch(ctx, env, args)
	if err == nil {
		return ExitOK
	}
	fmt.Fprintln(env.Stderr, a.render(err))
	var unknown *UnknownVerbError
	if errors.As(err, &unknown) {
		a.printHelp(env.Stderr)
		return ExitUsage
	}
	return ExitError
}

// Dispatch 分发一个动词并返回其错误（不渲染、不涉及退出码）——嵌套子动词
// 表复用同一机器：外层动词的 Run 把剩余参数交给内层 App.Dispatch 即获得
// 同构的未知子动词报错与旗标解析。args 至少一个元素（空参由外层 Run 或
// 外层动词自行处置）。
func (a *App) Dispatch(ctx context.Context, env *Environment, args []string) error {
	cmd, ok := a.commands[args[0]]
	if !ok {
		return &UnknownVerbError{Name: args[0]}
	}
	rest := args[1:]
	// 全局根旗标预剥离（-X 值 / -X=值 / --X=值，任意位置）——派生 Environment
	// 分发（不改动调用方传入的 env）。
	e := *env
	if a.RootFlag != "" {
		rest, e.Root = extractRootFlag(a.RootFlag, rest)
	}
	rest, err := a.ParseFlags(cmd, &e, rest)
	if err != nil {
		return err
	}
	return cmd.Run(ctx, &e, rest)
}

// SubDispatch 分发嵌套子动词并转写内层 UnknownVerbError——外层 Run 的用法
// 错误判定（退出码 2 + 帮助面）只认本层动词未命中，子动词未命中是子表内
// 的命令错误（退出码 1）。
func (a *App) SubDispatch(ctx context.Context, env *Environment, args []string) error {
	err := a.Dispatch(ctx, env, args)
	var unknown *UnknownVerbError
	if errors.As(err, &unknown) {
		return fmt.Errorf("unknown subcommand %q", unknown.Name)
	}
	return err
}

// ParseFlags 对实现 Flagged 的动词统一执行旗标解析：建 FlagSet → SetFlags
// 声明注册 → Parse → FlagError 映射。返回 Parse 后的位置参数（fs.Args()）。
// 与生产分发同构，测试可直接消费。
func (a *App) ParseFlags(cmd Command, env *Environment, rest []string) ([]string, error) {
	flagged, ok := cmd.(Flagged)
	if !ok {
		return rest, nil
	}
	fs := flag.NewFlagSet(cmd.Name(), flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	flagged.SetFlags(fs)
	if err := fs.Parse(rest); err != nil {
		return nil, a.flagError(cmd.Name(), err)
	}
	return fs.Args(), nil
}

func (a *App) flagError(verb string, err error) error {
	if a.FlagError != nil {
		return a.FlagError(verb, err)
	}
	return fmt.Errorf("%s: 参数错误: %v", verb, err)
}

func (a *App) render(err error) string {
	if a.RenderError != nil {
		return a.RenderError(err)
	}
	return err.Error()
}

// extractRootFlag 摘除参数序列中的全局根旗标（`-X 值` / `-X=值` / `--X=值`），
// 返回剩余参数与值（未提供 = 空串；`-X` 悬空无值按未提供处理，交由动词的
// 用法校验报错）。
func extractRootFlag(name string, args []string) ([]string, string) {
	short, long := "-"+name, "--"+name
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == short || a == long {
			if i+1 >= len(args) {
				return args[:i:i], ""
			}
			return append(args[:i:i], args[i+2:]...), args[i+1]
		}
		if v, ok := strings.CutPrefix(a, short+"="); ok {
			return append(args[:i:i], args[i+1:]...), v
		}
		if v, ok := strings.CutPrefix(a, long+"="); ok {
			return append(args[:i:i], args[i+1:]...), v
		}
	}
	return args, ""
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
