# commands

**English** | [中文](#中文文档)

A zero-dependency Go library for building subcommand-style CLIs: verb
registration and dispatch, declarative flag parsing, a global root flag
accepted anywhere, nested subcommands, and a conventional `0/1/2`
exit-code contract — all on the standard library.

## Features

- **Zero dependencies** — pure standard library (`context`, `errors`, `flag`, `fmt`, `io`, `sort`, `strings`)
- **Declarative flags** — verbs implement `Flagged.SetFlags`; the framework owns FlagSet construction and parse-error mapping, so the message shape isn't copy-pasted per verb. Verbs that don't implement `Flagged` get their arguments passed through untouched
- **Global root flag** — with `RootFlag: "R"`, the forms `-R <value>` / `-R=<value>` / `--R=<value>` are accepted at *any* position, stripped before verb flag parsing, and delivered via `Environment.Root`
- **Nested subcommands** — an inner `App` reuses the same dispatch machinery through `Dispatch` / `SubDispatch`
- **Assembly-time product voice** — `FlagError`, `RenderError`, and help composition (`HelpHeader` / `VerbTitle` / `HelpFooter`) are injectable; the framework ships no opinionated copy
- **Exit-code convention** — `0` success, `1` command error, `2` usage error (unknown verb)

## Installation

```bash
go get github.com/lynx-go/commands
```

## Quick start

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lynx-go/commands"
)

type echoCmd struct {
	upper bool
}

func (c *echoCmd) Name() string     { return "echo" }
func (c *echoCmd) Synopsis() string { return "print its arguments" }
func (c *echoCmd) Usage() string    { return "echo [-upper] words..." }

// SetFlags declares the verb's flags declaratively; App owns parsing.
func (c *echoCmd) SetFlags(fs *flag.FlagSet) {
	fs.BoolVar(&c.upper, "upper", false, "uppercase the output")
}

func (c *echoCmd) Run(ctx context.Context, env *commands.Environment, args []string) error {
	out := strings.Join(args, " ")
	if c.upper {
		out = strings.ToUpper(out)
	}
	fmt.Fprintln(env.Stdout, out)
	return nil
}

func main() {
	app := commands.New()
	app.RootFlag = "R" // accept -R/--R anywhere, value lands in Environment.Root
	app.Register(&echoCmd{})

	env := &commands.Environment{Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(app.Run(context.Background(), env, os.Args[1:]))
}
```

```console
$ mytool echo hello world
hello world
$ mytool echo -upper hello world
HELLO WORLD
$ mytool -R /some/root echo hi   # root flag stripped, lands in Environment.Root
hi
$ mytool help
commands:
  echo       print its arguments
$ mytool bogus
unknown verb "bogus"
commands:
  echo       print its arguments
$ echo $?
2
```

## Concepts

### `Environment`

The execution environment of one command (injectable for tests):

```go
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer
	Root   string // value of the global root flag; empty = not provided
}
```

`Root` is the landing spot of the global root flag (`App.RootFlag`, e.g.
`"R"`). The framework extracts it but does not interpret it — its meaning
(repository root, workspace root, …) is defined by the verb.

### `Command` and `Flagged`

```go
type Command interface {
	Name() string
	Synopsis() string
	Usage() string
	Run(ctx context.Context, env *Environment, args []string) error
}

type Flagged interface {
	SetFlags(fs *flag.FlagSet)
}
```

`Run` receives the positional arguments that remain after root-flag
extraction and flag parsing. The error returned by `Run` is rendered to
stderr by `App.Run` (via `RenderError`) and maps to exit code `1`.
Verbs that also implement `Flagged` get declarative flag registration:
pointers stored in the verb struct, fields read inside `Run`. Verbs that
don't implement `Flagged` receive their arguments untouched (the
bare-verb semantics of ignoring unknown flags).

### `App`

The zero value is usable; `New()` additionally sets the default
`VerbTitle`.

| Member | Purpose |
|---|---|
| `New() *App` | Create an empty app (`VerbTitle` defaults to `"commands:"`) |
| `Register(cmds ...Command)` | Register verbs |
| `Lookup(name string) (Command, bool)` | Find a verb by name |
| `Names() []string` | All verb names, sorted — help order is registration-independent |
| `Run(ctx, env, args) int` | Process top-level entry; returns the exit code |
| `Dispatch(ctx, env, args) error` | Dispatch one verb and return its error (no rendering, no exit code) |
| `SubDispatch(ctx, env, args) error` | Like `Dispatch`, but rewrites an inner `UnknownVerbError` to `unknown subcommand %q` |
| `ParseFlags(cmd, env, rest) ([]string, error)` | Unified flag parsing for `Flagged` verbs; returns positionals. Identical to production dispatch — tests can consume it directly |

`Run` behavior: no arguments or `help` prints the help screen and returns
`0`; an unknown verb renders the error, appends the help screen to
stderr, and returns `2`; any other verb error is rendered and returns
`1`.

Assembly-time hooks and knobs:

| Field | Default | Purpose |
|---|---|---|
| `RootFlag` | `""` (off) | Global root flag name; `"R"` matches `-R v` / `-R=v` / `--R=v` anywhere; value lands in `Environment.Root`. Leave empty on nested apps — the outer layer already stripped it |
| `FlagError` | `"%s: 参数错误: %v"` | Maps a flag parse failure to a business error |
| `RenderError` | `err.Error()` | Renders a verb error as a single stderr line |
| `HelpHeader` / `VerbTitle` / `HelpFooter` | `""` / `"commands:"` / `""` | Compose the help screen |

### Root flag forms

With `RootFlag: "R"`, all of these are stripped wherever they appear and
set `Environment.Root`:

```console
mytool -R /root echo hi
mytool echo -R /root hi
mytool echo hi -R=/root
mytool echo hi --R=/root
```

A dangling `-R` with no following token counts as *not provided* (empty
`Root`) and is left to the verb's own usage validation to report.

### Nested subcommands

An outer verb forwards its remaining arguments to an inner `App` via
`SubDispatch`, reusing the same flag machinery. The inner app should
leave `RootFlag` empty — the outer app already stripped it:

```go
type remoteCmd struct {
	sub *commands.App
}

func newRemoteCmd() *remoteCmd {
	sub := commands.New()
	sub.Register(&remoteAddCmd{}, &remoteRemoveCmd{})
	return &remoteCmd{sub: sub}
}

func (c *remoteCmd) Name() string     { return "remote" }
func (c *remoteCmd) Synopsis() string { return "manage remotes" }
func (c *remoteCmd) Usage() string    { return "remote <subcommand> [flags] [args]" }

func (c *remoteCmd) Run(ctx context.Context, env *commands.Environment, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, c.Usage())
		return fmt.Errorf("remote: missing subcommand")
	}
	return c.sub.SubDispatch(ctx, env, args)
}
```

An unknown *sub*command is a command error of the inner table (exit code
`1`, message `unknown subcommand %q`), not a usage error of the outer
layer — so the outer help screen is not appended.

### Exit codes

| Code | Constant | Meaning |
|---|---|---|
| `0` | `ExitOK` | Success (also: no arguments / `help`) |
| `1` | `ExitError` | Verb returned an error (incl. unknown nested subcommand) |
| `2` | `ExitUsage` | Unknown verb at this layer; help appended to stderr |

`UnknownVerbError{Name}` is the error shape for a dispatch miss;
`App.Run` selects exit code `2` and appends help based on it, and its
message can be rewritten through `RenderError`.

## Testing

```bash
go test ./...
```

The suite is table-driven on the standard `testing` package and covers
flag/positional dispatch, root-flag stripping at any position, usage
errors with help, flag-error hooks, bare-verb passthrough, nested
subcommands, and help composition.

## License

[MIT](LICENSE)

---

## 中文文档

[English](#commands)

一个零依赖的 Go 子命令 CLI 框架库：动词注册与分发、声明式旗标解析、
任意位置可用的全局根旗标、嵌套子动词，以及 `0/1/2` 退出码约定——
全部基于标准库实现。

## 特性

- **零依赖** —— 纯标准库（`context`、`errors`、`flag`、`fmt`、`io`、`sort`、`strings`）
- **声明式旗标** —— 动词实现 `Flagged.SetFlags` 即可；FlagSet 构造与解析错误映射由 App 单点承担，消息形态不再靠每动词样板复制。未实现 `Flagged` 的动词参数原样透传
- **全局根旗标** —— 置 `RootFlag: "R"` 后，`-R <值>` / `-R=<值>` / `--R=<值>` 在**任意位置**都被剥离（先于动词旗标解析），值经 `Environment.Root` 送达
- **嵌套子动词** —— 内层 `App` 通过 `Dispatch` / `SubDispatch` 复用同一套分发机器
- **装配期注入产品口径** —— `FlagError`、`RenderError` 与帮助面组装（`HelpHeader` / `VerbTitle` / `HelpFooter`）均可注入，框架不带自己的文案
- **退出码约定** —— `0` 成功；`1` 命令错误；`2` 用法错误（未知动词）

## 安装

```bash
go get github.com/lynx-go/commands
```

## 快速开始

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lynx-go/commands"
)

type echoCmd struct {
	upper bool
}

func (c *echoCmd) Name() string     { return "echo" }
func (c *echoCmd) Synopsis() string { return "打印参数" }
func (c *echoCmd) Usage() string    { return "echo [-upper] words..." }

// SetFlags 声明式注册旗标；解析由 App 统一承担。
func (c *echoCmd) SetFlags(fs *flag.FlagSet) {
	fs.BoolVar(&c.upper, "upper", false, "输出转大写")
}

func (c *echoCmd) Run(ctx context.Context, env *commands.Environment, args []string) error {
	out := strings.Join(args, " ")
	if c.upper {
		out = strings.ToUpper(out)
	}
	fmt.Fprintln(env.Stdout, out)
	return nil
}

func main() {
	app := commands.New()
	app.RootFlag = "R" // 任意位置接受 -R/--R，值落 Environment.Root
	app.Register(&echoCmd{})

	env := &commands.Environment{Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(app.Run(context.Background(), env, os.Args[1:]))
}
```

```console
$ mytool echo hello world
hello world
$ mytool echo -upper hello world
HELLO WORLD
$ mytool -R /some/root echo hi   # 根旗标被剥离，值落 Environment.Root
hi
$ mytool help
commands:
  echo       打印参数
$ mytool bogus
unknown verb "bogus"
commands:
  echo       打印参数
$ echo $?
2
```

## 核心概念

### `Environment`

一次命令执行的运行环境（测试可注入）：

```go
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer
	Root   string // 全局根旗标的值；空 = 未提供
}
```

`Root` 是全局根旗标（`App.RootFlag`，如 `"R"`）的落点——框架只摘取
不解释，语义（仓库根/工作区根……）由动词定义。

### `Command` 与 `Flagged`

```go
type Command interface {
	Name() string
	Synopsis() string
	Usage() string
	Run(ctx context.Context, env *Environment, args []string) error
}

type Flagged interface {
	SetFlags(fs *flag.FlagSet)
}
```

`Run` 收到的是根旗标剥离、旗标解析之后的位置参数。`Run` 返回的
error 由 `App.Run` 经 `RenderError` 渲染到 stderr 并映射退出码 `1`。
额外实现 `Flagged` 的动词获得声明式旗标注册：指针存动词结构体字段，
`Run` 里读字段。未实现者参数原样透传（无旗动词忽略未知参数的语义
不动）。

### `App`

零值可用；`New()` 额外给出缺省 `VerbTitle`。

| 成员 | 用途 |
|---|---|
| `New() *App` | 建空表实例（`VerbTitle` 缺省 `"commands:"`） |
| `Register(cmds ...Command)` | 注册动词 |
| `Lookup(name string) (Command, bool)` | 按名查找动词 |
| `Names() []string` | 全部动词名，字典序——帮助面顺序与注册序无关 |
| `Run(ctx, env, args) int` | 进程顶层入口；返回退出码 |
| `Dispatch(ctx, env, args) error` | 分发一个动词并返回其错误（不渲染、不涉退出码） |
| `SubDispatch(ctx, env, args) error` | 同 `Dispatch`，但把内层 `UnknownVerbError` 转写为 `unknown subcommand %q` |
| `ParseFlags(cmd, env, rest) ([]string, error)` | 对 `Flagged` 动词统一旗标解析，返回位置参数。与生产分发同构，测试可直接消费 |

`Run` 行为：空参数或 `help` 打帮助面返回 `0`；未知动词渲染错误、
向 stderr 附帮助面并返回 `2`；其余动词错误渲染后返回 `1`。

装配期钩子与旋钮：

| 字段 | 缺省 | 用途 |
|---|---|---|
| `RootFlag` | `""`（关闭） | 全局根旗标名；`"R"` 匹配任意位置的 `-R v` / `-R=v` / `--R=v`，值落 `Environment.Root`。嵌套内层应置空——外层已剥离 |
| `FlagError` | `"%s: 参数错误: %v"` | 把旗标解析失败映射为业务错误 |
| `RenderError` | `err.Error()` | 把动词错误渲染为 stderr 单行文案 |
| `HelpHeader` / `VerbTitle` / `HelpFooter` | `""` / `"commands:"` / `""` | 组装帮助面 |

### 根旗标形态

置 `RootFlag: "R"` 后，以下形态无论出现在哪里都会被剥离并写入
`Environment.Root`：

```console
mytool -R /root echo hi
mytool echo -R /root hi
mytool echo hi -R=/root
mytool echo hi --R=/root
```

悬空的 `-R`（后面没有值）按*未提供*处理（`Root` 为空），交由动词
自己的用法校验报错。

### 嵌套子动词

外层动词把剩余参数交给内层 `App` 的 `SubDispatch`，即可复用同一套
旗标机器。内层应置空 `RootFlag`——外层已经剥离过：

```go
type remoteCmd struct {
	sub *commands.App
}

func newRemoteCmd() *remoteCmd {
	sub := commands.New()
	sub.Register(&remoteAddCmd{}, &remoteRemoveCmd{})
	return &remoteCmd{sub: sub}
}

func (c *remoteCmd) Name() string     { return "remote" }
func (c *remoteCmd) Synopsis() string { return "管理远端" }
func (c *remoteCmd) Usage() string    { return "remote <subcommand> [flags] [args]" }

func (c *remoteCmd) Run(ctx context.Context, env *commands.Environment, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, c.Usage())
		return fmt.Errorf("remote: 缺少子命令")
	}
	return c.sub.SubDispatch(ctx, env, args)
}
```

未知*子*动词是内层表的命令错误（退出码 `1`，文案
`unknown subcommand %q`），不是外层的用法错误——因此不会附外层
帮助面。

### 退出码

| 码 | 常量 | 含义 |
|---|---|---|
| `0` | `ExitOK` | 成功（含：空参数 / `help`） |
| `1` | `ExitError` | 动词返回错误（含未知嵌套子动词） |
| `2` | `ExitUsage` | 本层未知动词；向 stderr 附帮助面 |

`UnknownVerbError{Name}` 是分发未命中的错误形态；`App.Run` 据此
选用法退出码并附帮助面，文案可经 `RenderError` 重写。

## 测试

```bash
go test ./...
```

测试基于标准 `testing` 包、表驱动，覆盖：旗标/位置参数分发、根旗标
任意位置剥离、用法错误附帮助面、旗标错误钩子、无旗动词透传、嵌套
子动词、帮助面组装。

## 许可

[MIT](LICENSE)
