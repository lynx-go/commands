# Changelog

## v0.1.0 (2026-09-04)

Initial public release.

- Verb registration, dispatch, and the `0`/`1`/`2` exit-code contract
- Declarative flag parsing via `Flagged.SetFlags`; the flag package's own
  diagnostics are silenced so a parse failure renders as a single stderr
  line through the `FlagError` hook
- Global root flag (`RootFlag`) accepted at any position — including
  before the verb name; repeated occurrences: last one wins; a `--`
  terminator ends stripping
- Help on every level: `help`, `help <verb>`, and `-h`/`-help` print to
  stdout and exit `0`; surfaces as `commands.ErrHelp` to hook-level
  consumers
- Nested subcommands via `Dispatch` / `SubDispatch`
- `Register` fails fast (panics) on the reserved name `help` and on
  duplicate verb names
