<p align="center"><img src="https://raw.githubusercontent.com/go-ruby-shellwords/brand/main/social/go-ruby-shellwords-shellwords.png" alt="go-ruby-shellwords/shellwords" width="720"></p>

# shellwords — go-ruby-shellwords

[![Docs](https://img.shields.io/badge/docs-mkdocs--material-DC2626)](https://go-ruby-shellwords.github.io/docs/)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

**A pure-Go (no cgo) reimplementation of Ruby's
[`shellwords`](https://docs.ruby-lang.org/en/master/Shellwords.html) standard
library** — the deterministic, interpreter-independent POSIX-shell word
splitting and escaping of MRI 4.0.5's `lib/shellwords.rb`. It splits a command
line into words the way the Bourne shell does, escapes a single argument so the
shell parses it back verbatim, and joins an argument list into one escaped
command line — **without any Ruby runtime**.

It is the `shellwords` backend for
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby), but is a
**standalone, reusable** module with no dependency on the Ruby runtime — a
sibling of [go-ruby-regexp](https://github.com/go-ruby-regexp/regexp) (the
Onigmo engine), [go-ruby-erb](https://github.com/go-ruby-erb/erb) (the ERB
compiler) and [go-ruby-yaml](https://github.com/go-ruby-yaml/yaml) (the Psych
port).

> **What it is.** `Shellwords` is small, pure computation: a regex-driven
> tokenizer plus two string transforms. Every byte of behaviour — the
> double/single/backslash quoting rules, the unmatched-quote `ArgumentError`
> message (offset and all), the empty-string `''`, the special newline handling
> in `shellescape` — is reproduced exactly, validated against the `ruby` binary.

## Features

Faithful port of `Shellwords.shellsplit` / `shellescape` / `shelljoin`,
validated against the `ruby` binary on every supported platform:

- **`Split`** tokenizes per MRI's scanner: bare segments, `'single-quoted'`,
  `"double-quoted"` (with POSIX 2.2.3 backslash rules — only `$` `` ` `` `"`
  `\` `<newline>` are escapes inside double quotes), and `\`-escaped chars, with
  concatenation within a word (`a"b"'c'` → `abc`).
- **Exact errors** — a trailing unmatched quote (or a NUL) raises the MRI
  `ArgumentError`, byte-for-byte: `Unmatched quote at 2: ...'`,
  `Nul character at 1: ...`, including the `...` truncation prefix and the
  character offset.
- **`Escape`** — empty string → `''`; otherwise every character outside
  `[A-Za-z0-9_-.,:+/@\n]` is backslash-escaped, and each newline is then
  rewritten as `'\n'` (a backslash cannot escape a newline, which the shell
  would treat as a line continuation). Multibyte runes are escaped as whole
  characters.
- **`Join`** = map `Escape` over the list, joined with a single space — and
  `Split(Join(x)) == x` round-trips.

CGO-free, dependency-free, **100% test coverage**, `gofmt` + `go vet` clean, and
green across the six 64-bit Go targets (amd64, arm64, riscv64, loong64, ppc64le,
s390x).

## Install

```sh
go get github.com/go-ruby-shellwords/shellwords
```

## Usage

```go
package main

import (
	"fmt"

	"github.com/go-ruby-shellwords/shellwords"
)

func main() {
	// Split — Shellwords.shellsplit / String#shellsplit
	words, _ := shellwords.Split(`three blind "mice" and a 'big cat'`)
	fmt.Printf("%q\n", words) // ["three" "blind" "mice" "and" "a" "big cat"]

	// Escape — Shellwords.shellescape / String#shellescape
	fmt.Println(shellwords.Escape("It's a test")) // It\'s\ a\ test
	fmt.Println(shellwords.Escape(""))            // ''

	// Join — Shellwords.shelljoin / Array#shelljoin
	fmt.Println(shellwords.Join([]string{"ls", "-l", "Funny GIFs"})) // ls -l Funny\ GIFs

	// A trailing unmatched quote is an error, exactly as in MRI.
	_, err := shellwords.Split(`a 'b`)
	fmt.Println(err) // Unmatched quote at 2: ...'
}
```

## API

```go
// Split splits line into tokens like the UNIX Bourne shell
// (Shellwords.shellsplit / String#shellsplit). An unmatched quote or a NUL
// yields an *ArgumentError whose message reproduces MRI's exactly.
func Split(line string) ([]string, error)

// Escape escapes s for safe use as one Bourne-shell argument
// (Shellwords.shellescape / String#shellescape). "" -> "''".
func Escape(s string) string

// Join escapes each element and joins with a space
// (Shellwords.shelljoin / Array#shelljoin).
func Join(words []string) string

// ArgumentError mirrors Ruby's ArgumentError; Error() is MRI's message.
type ArgumentError struct{ Message string }
func (e *ArgumentError) Error() string
```

| Ruby                                        | Go                  |
| ------------------------------------------- | ------------------- |
| `Shellwords.shellsplit` / `String#shellsplit` | `Split(line)`     |
| `Shellwords.shellescape` / `String#shellescape` | `Escape(s)`     |
| `Shellwords.shelljoin` / `Array#shelljoin`  | `Join(words)`       |
| `ArgumentError: Unmatched quote …`          | `*ArgumentError`    |

## Tests & coverage

The suite pairs deterministic, ruby-free golden vectors (which alone hold
coverage at 100%, so the qemu cross-arch and Windows lanes pass the gate) with a
**differential MRI oracle**: a broad corpus — empties, embedded
quotes/spaces/newlines/specials, multibyte, and the unmatched-quote error — is
run through both this package and the system `ruby` (`Shellwords.shellsplit` /
`shellescape` / `shelljoin`) and compared byte-for-byte, including the
`split(join(x)) == x` round trip. The oracle exchanges inputs and outputs as
base64 over a binmode stdin/stdout so embedded newlines survive, gates on MRI
≥ 4.0, and skips itself where `ruby` is absent.

```sh
COVERPKG=$(go list ./... | paste -sd, -)
go test -race -coverpkg="$COVERPKG" -coverprofile=cover.out ./...
go tool cover -func=cover.out | tail -1   # 100.0%
```

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-ruby-shellwords/shellwords authors.
