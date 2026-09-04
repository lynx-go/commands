# commands

**English** | [中文](#中文文档)

A zero-dependency Go library for building subcommand-style CLIs: verb
registration and dispatch, declarative flag parsing, a global root flag
accepted anywhere, nested subcommands, help on every level (`help`,
`help <verb>`, `-h`), and a conventional `0/1/2` exit-code contract — all
on the standard library.

## Features

- **Zero dependencies** — pure standard library (`context`, `errors`, `flag`, `fmt`, `io`, `sort`, `strconv`, `strings`)
- **Declarative flags** — verbs implement `Flagged.SetFlags`; the framework owns FlagSet construction and parse-error mapping, and the flag package's own diagnostics are silenced, so a parse failure renders as a *single* stderr line through the `FlagError` hook. Verbs that don't implement `Flagged` get their arguments passed through untouched
- **Global root flag** — with `RootFlag: "R"`, the forms `-R <value>` / `-R=<value>` / `--R=<value>` are accepted at *any* position — including before the verb name — stripped before verb flag parsing, and delivered via `Environment.Root`. Stripping stops at a `--` terminator
- **Global bool root flags** — `RootBoolFlags: []string{"json"}` declares valueless global flags (`-json`/`--json`, plus `=true`/`=false` forms) stripped anywhere and delivered via `Environment.RootBools`
- **`UsageError`** — a verb's argument-validation failures exit `2` with its usage line appended (detected through `%w` wrapping and nested dispatch); the default `FlagError` mapping uses it too
- **Help everywhere** — `help` prints the command list, `help <verb>` prints that verb's `Usage()` line plus its flag defaults, and `-h`/`-help` works at the top level and per verb — all to stdout with exit code `0` (`ErrHelp` for hook-level consumers)
- **Nested subcommands** — an inner `App` reuses the same dispatch machinery through `Dispatch` / `SubDispatch`
- **Assembly-time product voice** — `FlagError`, `RenderError`, and help composition (`HelpHeader` / `VerbTitle` / `HelpFooter`) are injectable; the framework ships no opinionated copy
- **Exit-code convention** — `0` success, `1` command error, `2` usage error (unknown verb, flag parse failure, verb-declared `UsageError`)

## Installation

```bash
go get github.com/lynx-go/commands
```

Requires a tagged release (`v0.1.0` or later) and Go 1.21+.

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
$ mytool -R /some/root echo hi   # root flag stripped, even before the verb
hi
$ mytool help
commands:
  echo       print its arguments
$ mytool help echo
usage: echo [-upper] words...

flags:
  -upper
    	uppercase the output
$ mytool echo -h                 # same output as "help echo", exit 0
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
	Stdout    io.Writer
	Stderr    io.Writer
	Root      string           // value of the global root flag; empty = not provided
	RootBools map[string]bool  // parsed values of the global bool root flags
}
```

`Root` is the landing spot of the global root flag (`App.RootFlag`, e.g.
`"R"`). The framework extracts it but does not interpret it — its meaning
(repository root, workspace root, …) is defined by the verb. If the flag
is absent, a programmatically pre-set `Root` is preserved.

`RootBools` holds the parsed values of the global bool root flags
(`App.RootBoolFlags`, e.g. `[]string{"json"}`). A key is present only
when the flag was provided; read it as `env.RootBools["json"]` (a
missing key reads as `false`). Preset values are preserved, and parsing
never mutates a caller-supplied map.

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
`Synopsis()` feeds the command list; `Usage()` is printed by
`help <verb>` and verb-level `-h`. Verbs that also implement `Flagged`
get declarative flag registration: pointers stored in the verb struct,
fields read inside `Run`. Verbs that don't implement `Flagged` receive
their arguments untouched (the bare-verb semantics of ignoring unknown
flags).

### `App`

The zero value is usable; `New()` additionally sets the default
`VerbTitle`.

| Member | Purpose |
|---|---|
| `New() *App` | Create an empty app (`VerbTitle` defaults to `"commands:"`) |
| `Register(cmds ...Command)` | Register verbs; panics on the reserved name `help` or a duplicate name (assembly-time fail-fast) |
| `Lookup(name string) (Command, bool)` | Find a verb by name |
| `Names() []string` | All verb names, sorted — help order is registration-independent |
| `Run(ctx, env, args) int` | Process top-level entry; returns the exit code |
| `Dispatch(ctx, env, args) error` | Dispatch one verb and return its error (no rendering, no exit code) |
| `SubDispatch(ctx, env, args) error` | Like `Dispatch`, but rewrites an inner `UnknownVerbError` to `unknown subcommand %q` |
| `ParseFlags(cmd, env, rest) ([]string, error)` | Unified flag parsing for `Flagged` verbs; returns positionals, or `ErrHelp` (verb help already printed to stdout). Identical to production dispatch — tests can consume it directly |

`Run` behavior: no arguments, `help`, or a `-h`/`-help` flag prints the
help screen to stdout and returns `0`; `help <verb>` prints that verb's
usage and returns `0`; an unknown verb (or `help <unknown>`) renders the
error, appends the help screen to stderr, and returns `2`; any other
verb error is rendered and returns `1`.

Assembly-time hooks and knobs:

| Field | Default | Purpose |
|---|---|---|
| `RootFlag` | `""` (off) | Global root flag name; `"R"` matches `-R v` / `-R=v` / `--R=v` anywhere, including before the verb name; value lands in `Environment.Root`. Leave empty on nested apps — the outer layer already stripped it |
| `RootBoolFlags` | `nil` (off) | Global valueless bool flag names; `"json"` matches `-json` / `--json` / `--json=<bool>` anywhere, landing in `Environment.RootBools`. Leave empty on nested apps — the outer layer already stripped them |
| `FlagError` | `"%s: bad arguments: %v"` | Maps a flag parse failure to a business error (the single stderr line — the flag package's own output is silenced) |
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

Details:

- If the flag appears more than once, the last occurrence wins (matching
  the flag package's repeated-flag convention).
- Stripping stops at a `--` terminator: `mytool echo hi -- -R /root`
  passes `-R /root` to the verb as literal arguments.
- A dangling `-R` with no following token counts as *not provided* (empty
  `Root`) and is left to the verb's own usage validation to report.

### Bool root flags

`RootBoolFlags: []string{"json"}` declares valueless global bool flags,
stripped wherever they appear (verb position included) and landing in
`Environment.RootBools`:

```console
mytool --json verify .
mytool verify --json .
mytool verify --json=false .   # explicit false
```

Details:

- An `=value` form is honored when the value parses as a boolean
  (`strconv.ParseBool`); anything else — e.g. `--json=yes`, or the empty
  value `--json=` — stays in place for verb-level parsing to reject.
- Last occurrence wins per flag; a `--` terminator ends stripping.
- `Run` panics on an empty name, a duplicate name, or a collision with
  `RootFlag` — assembly-time bugs, like `Register`'s panics.

### `UsageError`

A verb flags its own argument-validation failures as usage problems —
rendered like any error, with the verb's usage line appended, and mapped
to exit code `2` (instead of `1`):

```go
func (c *configCmd) Run(_ context.Context, _ *commands.Environment, args []string) error {
	if c.path == "" {
		return &commands.UsageError{Usage: c.Usage(), Err: errors.New("config: --config is required")}
	}
	// ...
}
```

The error is detected through `fmt.Errorf("%w")` wrapping and across
nested `SubDispatch`. Since v0.2.0 the default `FlagError` mapping also
returns a `UsageError`, so a flag parse failure exits `2` with the verb's
usage hint (custom `FlagError` hooks keep full control — whatever error
they return decides the exit path).

### Help and `-h`

| Invocation | Output | Exit |
|---|---|---|
| `mytool` / `mytool help` / `mytool -h` | command list to stdout | `0` |
| `mytool help echo` / `mytool echo -h` | verb usage + flag defaults to stdout | `0` |
| `mytool help bogus` | error + command list to stderr | `2` |

Verb-level `-h` follows standard flag semantics: it is recognized before
the first positional argument (`mytool echo -h`, not `mytool echo hi -h`)
and only for verbs that implement `Flagged`. For hook-level consumers the
parse surfaces as `commands.ErrHelp`; `App.Run` maps it to exit code `0`
without rendering.

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
| `0` | `ExitOK` | Success (also: no arguments / `help` / `help <verb>` / `-h`) |
| `1` | `ExitError` | Verb returned an error (incl. unknown nested subcommand) |
| `2` | `ExitUsage` | Usage error: unknown verb at this layer (help appended), a flag parse failure (usage line appended, since v0.2.0), or a verb-declared `UsageError` (usage hint appended) |

`UnknownVerbError{Name}` is the error shape for a dispatch miss;
`App.Run` selects exit code `2` and appends help based on it, and its
message can be rewritten through `RenderError`. `UsageError{Usage, Err}`
carries the same treatment for verb-level argument problems — see
[`UsageError`](#usageerror).

## Testing

```bash
go test ./...
```

The suite is table-driven on the standard `testing` package and covers
flag/positional dispatch, root-flag stripping at any position (before the
verb included, `--` terminator, last-occurrence-wins, Root preservation),
bool root flag stripping (all forms, unparsable and empty `=value`
passthrough, preset merge, configuration panics, value-form `-R`
consuming a following token), usage errors with help, single-line
flag-error rendering, the `FlagError` hook, bare-verb passthrough,
`help` / `help <verb>` / `-h` behavior, nested subcommands, help
composition, `Names()` ordering, and `Register` fail-fast panics. The
`example_test.go` examples double as the README quick-start smoke test.

## License

[MIT](LICENSE)

---

## 中文文档

[English](#commands)

一个零依赖的 Go 子命令 CLI 框架库：动词注册与分发、声明式旗标解析、
任意位置可用的全局根旗标（值形式 + 无值 bool 形式）、动词级用法错误
（`UsageError` → 退出码 2 + usage 提示）、嵌套子动词、各级 help（`help`、
`help <动词>`、`-h`），以及 `0/1/2` 退出码约定——全部基于标准库实现。

## 特性

- **零依赖** —— 纯标准库（`context`、`errors`、`flag`、`fmt`、`io`、`sort`、`strconv`、`strings`）
- **声明式旗标** —— 动词实现 `Flagged.SetFlags` 即可；FlagSet 构造与解析错误映射由 App 单点承担，flag 包自身的诊断输出被静默，解析失败只经 `FlagError` 钩子渲染为 stderr 上的**单行**错误。未实现 `Flagged` 的动词参数原样透传
- **全局根旗标** —— 置 `RootFlag: "R"` 后，`-R <值>` / `-R=<值>` / `--R=<值>` 在**任意位置**（含动词名之前）都被剥离（先于动词旗标解析），值经 `Environment.Root` 送达；遇到 `--` 终结符即停止剥离
- **全局 bool 根旗标** —— `RootBoolFlags: []string{"json"}` 声明无值全局旗标（`-json`/`--json`，支持 `=true`/`=false`），任意位置剥离，值经 `Environment.RootBools` 送达
- **`UsageError`** —— 动词的参数校验失败退出码为 `2` 并附 usage 提示行（可穿透 `%w` 包装与嵌套分发）；v0.2.0 起默认 `FlagError` 映射同样走 `UsageError`
- **各级 help** —— `help` 打命令列表，`help <动词>` 打该动词的 `Usage()` 行与旗标缺省值，`-h`/`-help` 在顶层与动词级都可用——全部输出到 stdout、退出码 `0`（钩子层消费者会收到 `ErrHelp`）
- **嵌套子动词** —— 内层 `App` 通过 `Dispatch` / `SubDispatch` 复用同一套分发机器
- **装配期注入产品口径** —— `FlagError`、`RenderError` 与帮助面组装（`HelpHeader` / `VerbTitle` / `HelpFooter`）均可注入，框架不带自己的文案
- **退出码约定** —— `0` 成功；`1` 命令错误；`2` 用法错误（未知动词 / 旗标解析失败 / 动词声明的 `UsageError`）

## 安装

```bash
go get github.com/lynx-go/commands
```

需要有已打的版本标签（`v0.1.0` 及以上）与 Go 1.21+。

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
$ mytool -R /some/root echo hi   # 根旗标被剥离——放在动词名之前也行
hi
$ mytool help
commands:
  echo       打印参数
$ mytool help echo
usage: echo [-upper] words...

flags:
  -upper
    	输出转大写
$ mytool echo -h                 # 与 "help echo" 相同，退出码 0
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
	Stdout    io.Writer
	Stderr    io.Writer
	Root      string          // 全局根旗标的值；空 = 未提供
	RootBools map[string]bool // 全局 bool 根旗标的解析结果
}
```

`Root` 是全局根旗标（`App.RootFlag`，如 `"R"`）的落点——框架只摘取
不解释，语义（仓库根/工作区根……）由动词定义。旗标未出现时，调用方
预先程序化设置的 `Root` 会被保留。

`RootBools` 是全局 bool 根旗标（`App.RootBoolFlags`，如
`[]string{"json"}`）的落点。key 仅在旗标被提供时存在；按
`env.RootBools["json"]` 读取（缺 key 读作 `false`）。预设值会被保留，
解析也绝不改动调用方传入的 map。

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
`Synopsis()` 供命令列表使用；`Usage()` 由 `help <动词>` 与动词级
`-h` 打印。额外实现 `Flagged` 的动词获得声明式旗标注册：指针存动词
结构体字段，`Run` 里读字段。未实现者参数原样透传（无旗动词忽略未知
参数的语义不动）。

### `App`

零值可用；`New()` 额外给出缺省 `VerbTitle`。

| 成员 | 用途 |
|---|---|
| `New() *App` | 建空表实例（`VerbTitle` 缺省 `"commands:"`） |
| `Register(cmds ...Command)` | 注册动词；动词名为保留字 `help` 或重名时 panic（装配期快速失败） |
| `Lookup(name string) (Command, bool)` | 按名查找动词 |
| `Names() []string` | 全部动词名，字典序——帮助面顺序与注册序无关 |
| `Run(ctx, env, args) int` | 进程顶层入口；返回退出码 |
| `Dispatch(ctx, env, args) error` | 分发一个动词并返回其错误（不渲染、不涉退出码） |
| `SubDispatch(ctx, env, args) error` | 同 `Dispatch`，但把内层 `UnknownVerbError` 转写为 `unknown subcommand %q` |
| `ParseFlags(cmd, env, rest) ([]string, error)` | 对 `Flagged` 动词统一旗标解析，返回位置参数；`-h` 时返回 `ErrHelp`（动词帮助已打到 stdout）。与生产分发同构，测试可直接消费 |

`Run` 行为：空参数、`help` 或 `-h`/`-help` 打帮助面到 stdout 返回
`0`；`help <动词>` 打该动词用法返回 `0`；未知动词（或
`help <未知>`）渲染错误、向 stderr 附帮助面并返回 `2`；其余动词错误
渲染后返回 `1`。

装配期钩子与旋钮：

| 字段 | 缺省 | 用途 |
|---|---|---|
| `RootFlag` | `""`（关闭） | 全局根旗标名；`"R"` 匹配任意位置的 `-R v` / `-R=v` / `--R=v`（含动词名之前），值落 `Environment.Root`。嵌套内层应置空——外层已剥离 |
| `RootBoolFlags` | `nil`（关闭） | 全局无值 bool 旗标名；`"json"` 匹配任意位置的 `-json` / `--json` / `--json=<bool>`，值落 `Environment.RootBools`。嵌套内层应置空——外层已剥离 |
| `FlagError` | `"%s: bad arguments: %v"` | 把旗标解析失败映射为业务错误（stderr 上的唯一一行——flag 包自身输出已静默） |
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

细节：

- 旗标出现多次时**最后一次生效**（与 flag 包重复旗标的惯例一致）。
- 遇到 `--` 终结符即停止剥离：`mytool echo hi -- -R /root` 中
  `-R /root` 作为字面参数传给动词。
- 悬空的 `-R`（后面没有值）按*未提供*处理（`Root` 为空），交由动词
  自己的用法校验报错。

### Bool 根旗标

`RootBoolFlags: []string{"json"}` 声明无值全局 bool 旗标，任意位置
（含动词名之前）剥离，值落入 `Environment.RootBools`：

```console
mytool --json verify .
mytool verify --json .
mytool verify --json=false .   # 显式 false
```

细节：

- `=值` 形式仅在值能解析为布尔时生效（`strconv.ParseBool`）；其余
  ——如 `--json=yes`，或空值 `--json=`——原样留在参数里，交由动词级
  解析去报错。
- 每个旗标最后一次生效；遇到 `--` 终结符即停止剥离。
- `Run` 在名字为空、重名、或与 `RootFlag` 冲突时 panic——都是装配期
  问题，与 `Register` 的快速失败同一精神。

### `UsageError`

动词把自己的参数校验失败标记为用法问题——照常渲染，并附上该动词的
usage 行，退出码映射为 `2`（而非 `1`）：

```go
func (c *configCmd) Run(_ context.Context, _ *commands.Environment, args []string) error {
	if c.path == "" {
		return &commands.UsageError{Usage: c.Usage(), Err: errors.New("config: --config is required")}
	}
	// ...
}
```

该错误可穿透 `fmt.Errorf("%w")` 包装与嵌套 `SubDispatch` 被识别。
v0.2.0 起默认 `FlagError` 映射同样返回 `UsageError`，旗标解析失败
因此退出 `2` 并附动词 usage 提示（自定义 `FlagError` 钩子仍握有
完全控制权——返回什么错误就走什么退出路径）。

### help 与 `-h`

| 调用 | 输出 | 退出码 |
|---|---|---|
| `mytool` / `mytool help` / `mytool -h` | 命令列表到 stdout | `0` |
| `mytool help echo` / `mytool echo -h` | 动词用法 + 旗标缺省值到 stdout | `0` |
| `mytool help bogus` | 错误 + 命令列表到 stderr | `2` |

动词级 `-h` 遵循标准 flag 语义：只在第一个位置参数之前被识别
（`mytool echo -h` 可以，`mytool echo hi -h` 不行），且仅对实现了
`Flagged` 的动词生效。钩子层消费者会收到 `commands.ErrHelp`；
`App.Run` 把它映射为退出码 `0`，不做渲染。

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
| `0` | `ExitOK` | 成功（含：空参数 / `help` / `help <动词>` / `-h`） |
| `1` | `ExitError` | 动词返回错误（含未知嵌套子动词） |
| `2` | `ExitUsage` | 用法错误：本层未知动词（附帮助面）、旗标解析失败（附 usage 行，v0.2.0 起）、动词声明的 `UsageError`（附 usage 提示） |

`UnknownVerbError{Name}` 是分发未命中的错误形态；`App.Run` 据此
选用法退出码并附帮助面，文案可经 `RenderError` 重写。
`UsageError{Usage, Err}` 让动词级参数问题得到同样待遇——见
[`UsageError`](#usageerror)。

## 测试

```bash
go test ./...
```

测试基于标准 `testing` 包、表驱动，覆盖：旗标/位置参数分发、根旗标
任意位置剥离（含动词名之前、`--` 终结符、重复取最后一次、`Root`
保留）、bool 根旗标剥离（全部形态、不可解析与空 `=值` 透传、预设
合并、配置 panic、值形式 `-R` 吞掉后续 token）、用法错误附帮助面、
旗标错误单行渲染、`FlagError` 钩子、无旗动词透传、`help` /
`help <动词>` / `-h` 行为、嵌套子动词、帮助面组装、`Names()` 排序、
`Register` 快速失败 panic。`example_test.go` 中的示例同时充当
README 快速开始的冒烟测试。

## 许可

[MIT](LICENSE)
