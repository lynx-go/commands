# Changelog

## v0.2.0 (2026-09-04)

Global bool root flags and verb-declared usage errors — both driven by
dogfooding the library in the Eventboat CLI.

### Added

- **`RootBoolFlags`**: valueless global bool flags (`-json`/`--json`,
  plus `=true`/`=false` forms parsed with `strconv.ParseBool`) stripped
  at any position like `RootFlag`, delivered via the new
  `Environment.RootBools` map (a key is present only when provided;
  preset values are preserved and never mutated). An `=value` that does
  not parse as a boolean stays in place for verb-level parsing.
  `Run` panics on duplicate names or a collision with `RootFlag`.
- **`UsageError{Usage, Err}`**: verbs mark their own argument-validation
  failures — rendered like any error with the `Usage` string appended as
  a `usage:` hint, exiting `2`. Detected through `fmt.Errorf("%w")`
  wrapping and across nested `SubDispatch`; the hint carries the usage
  *string* (resolved at construction) rather than a verb name, so it
  survives dispatch layers that cannot look the verb up.

### Changed

- **Flag parse failures now exit `2`** (was `1`): the default `FlagError`
  mapping returns a `UsageError` carrying the verb's usage line — a parse
  failure is a usage problem. Custom `FlagError` hooks keep full control:
  whatever error they return decides the exit path.
- Root-flag stripping is now a single combined pass; previously a second
  same-level root-flag occurrence leaked through as a positional (only
  the first was stripped per pass). Last-occurrence-wins now holds within
  one pass as documented.

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
