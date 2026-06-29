// Copyright (c) the go-ruby-shellwords/shellwords authors
//
// SPDX-License-Identifier: BSD-3-Clause

package shellwords

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// rubyBin locates a usable `ruby` whose MRI is >= 4.0 once. The oracle tests
// skip themselves when ruby is absent (the qemu cross-arch lanes and the
// Windows lane) or too old, so the deterministic golden suite alone drives the
// 100% coverage gate there.
func rubyBin(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not on PATH; skipping MRI oracle")
	}
	// Gate the oracle on MRI >= 4.0 (org Windows-CI lesson): older rubyies may
	// carry a different shellwords version with subtly different behaviour.
	out, err := exec.Command(path, "-e", "print(RUBY_VERSION >= '4.0' ? 'ok' : 'old')").CombinedOutput()
	if err != nil {
		t.Skipf("ruby version probe failed (%v); skipping MRI oracle", err)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		t.Skip("ruby < 4.0; skipping MRI oracle")
	}
	return path
}

// oracleResult is one element of the JSON array emitted by the Ruby oracle. For
// split, either Words (on success) or Err (the ArgumentError message) is set.
type oracleResult struct {
	Words []string `json:"words"`
	Err   string   `json:"err"`
	OK    bool     `json:"ok"`
}

// runRubyOracle feeds the inputs to a single Ruby process (one spawn for the
// whole corpus) over stdin as a JSON array of base64-encoded byte strings, and
// reads back a JSON array of results. Both stdin and stdout are put in binary
// mode so embedded newlines and NULs survive the Windows... (the oracle never
// runs on Windows, but binmode is the standing org rule for byte-faithful IO).
func runRubyOracle(t *testing.T, bin, op string, inputs []string) []oracleResult {
	t.Helper()

	enc := make([]string, len(inputs))
	for i, s := range inputs {
		enc[i] = b64(s)
	}
	in, err := json.Marshal(enc)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}

	// The script reads the whole stdin, base64-decodes each input, applies the
	// requested operation, and prints a JSON array of {ok, words|err}.
	script := `
require 'shellwords'
require 'json'
require 'base64'
$stdin.binmode
$stdout.binmode
op = ARGV[0]
# Tag decoded bytes as UTF-8 so shellescape treats multibyte sequences as whole
# characters (one backslash per rune), matching the Go string (UTF-8) semantics
# the public API exposes. Without this, a binary (ASCII-8BIT) string would be
# escaped byte-by-byte.
inputs = JSON.parse($stdin.read).map { |b| Base64.strict_decode64(b).force_encoding('UTF-8') }
out = inputs.map do |s|
  begin
    case op
    when 'split'
      { 'ok' => true, 'words' => Shellwords.shellsplit(s).map { |w| Base64.strict_encode64(w) } }
    when 'escape'
      { 'ok' => true, 'words' => [Base64.strict_encode64(Shellwords.shellescape(s))] }
    end
  rescue ArgumentError => e
    { 'ok' => false, 'err' => Base64.strict_encode64(e.message) }
  end
end
$stdout.write(JSON.generate(out))
`
	cmd := exec.Command(bin, "-e", script, op)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ruby oracle (%s) failed: %v\nstderr:\n%s", op, err, stderr.String())
	}

	var raw []oracleResult
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode oracle output: %v\nraw:\n%s", err, stdout.String())
	}
	// base64-decode the payloads back to raw bytes.
	for i := range raw {
		if raw[i].OK {
			for j := range raw[i].Words {
				raw[i].Words[j] = unb64(t, raw[i].Words[j])
			}
		} else {
			raw[i].Err = unb64(t, raw[i].Err)
		}
	}
	return raw
}

func b64(s string) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var b strings.Builder
	data := []byte(s)
	for i := 0; i < len(data); i += 3 {
		var n uint32
		k := 0
		for ; k < 3 && i+k < len(data); k++ {
			n |= uint32(data[i+k]) << (16 - 8*k)
		}
		b.WriteByte(tbl[(n>>18)&0x3f])
		b.WriteByte(tbl[(n>>12)&0x3f])
		if k > 1 {
			b.WriteByte(tbl[(n>>6)&0x3f])
		} else {
			b.WriteByte('=')
		}
		if k > 2 {
			b.WriteByte(tbl[n&0x3f])
		} else {
			b.WriteByte('=')
		}
	}
	return b.String()
}

func unb64(t *testing.T, s string) string {
	t.Helper()
	out, err := decodeBase64(s)
	if err != nil {
		t.Fatalf("base64 decode %q: %v", s, err)
	}
	return string(out)
}

func decodeBase64(s string) ([]byte, error) {
	var dec [256]int8
	for i := range dec {
		dec[i] = -1
	}
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for i := 0; i < len(tbl); i++ {
		dec[tbl[i]] = int8(i)
	}
	var out []byte
	var buf uint32
	var nbits int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' {
			break
		}
		v := dec[c]
		if v < 0 {
			continue
		}
		buf = (buf << 6) | uint32(v)
		nbits += 6
		if nbits >= 8 {
			nbits -= 8
			out = append(out, byte(buf>>uint(nbits)))
		}
	}
	return out, nil
}

// oracleCorpus is the shared differential corpus for split and escape. It spans
// empties, embedded quotes/spaces/newlines/specials, multibyte, and unmatched
// quotes (split only). It deliberately excludes NUL for the escape direction,
// where MRI raises a different ("NUL character") error this Go API does not
// model (Escape never errors).
func oracleCorpus() []string {
	return []string{
		"", " ", "   ", "\t", "\n", "\r\n", "\f", "\v",
		"a", "ab", "a b", "a b c", "  a  b  ",
		"x=1 y=2", "a;b&c|d", "a > b < c", "ruby my_prog.rb | less",
		`three blind "mice"`, `here are "two words"`, `"foo bar" baz`,
		`""`, `a"b"c`, `"a\$b"`, "\"a\\`b\"", `"a\"b"`, `"a\\b"`,
		"\"a\\\nb\"", `"a\tb"`, `"mixed 'quotes'"`,
		`'single with spaces'`, `''`, `a''b`, `a'b'c`, `'a\b'`, `'a"b'`,
		`foo\ bar`, `a\ b\ c`, `\n`, `\\`, `\"`, `a\`, `\`,
		`a"b"'c'\ d`, `pre"mid"post`, "tab\there",
		"It's better to give", "Don't rock the boat",
		"café latte", "日本語 テスト", "100%", "a😀b 😀",
		"$HOME", "`backtick`", "a|b", "a&b", "a;b", "a*b?c[d]",
		"-flag", "--long=value", "path/to/file.txt", "a.b,c:d+e/f@g-h_i",
		"\\backslash", "back\\slash", "trailing ",
		// Unmatched-quote and trailing-quote error cases (split only).
		`'`, `"`, `'unmatched`, `"unmatched`, `ab 'cd`, `  "x`, `a "x`,
		"they all ran after the farmer's wife",
	}
}

// TestOracleSplit checks Split matches MRI's Shellwords.shellsplit byte-for-byte
// across the corpus, including the exact ArgumentError message for unmatched
// quotes.
func TestOracleSplit(t *testing.T) {
	bin := rubyBin(t)
	corpus := oracleCorpus()
	want := runRubyOracle(t, bin, "split", corpus)
	if len(want) != len(corpus) {
		t.Fatalf("oracle returned %d results for %d inputs", len(want), len(corpus))
	}
	for i, in := range corpus {
		got, err := Split(in)
		w := want[i]
		if w.OK {
			if err != nil {
				t.Errorf("Split(%q) = error %v, MRI = %#v", in, err, w.Words)
				continue
			}
			if !eqStrings(got, w.Words) {
				t.Errorf("Split(%q) = %#v, MRI = %#v", in, got, w.Words)
			}
		} else {
			if err == nil {
				t.Errorf("Split(%q) = %#v, MRI raised %q", in, got, w.Err)
				continue
			}
			if err.Error() != w.Err {
				t.Errorf("Split(%q) err = %q, MRI = %q", in, err.Error(), w.Err)
			}
		}
	}
}

// TestOracleEscape checks Escape matches MRI's Shellwords.shellescape
// byte-for-byte, and that Split(Escape(x)) == [x] for every non-empty corpus
// entry that MRI itself round-trips (the documented escape guarantee).
func TestOracleEscape(t *testing.T) {
	bin := rubyBin(t)
	corpus := oracleCorpus()
	want := runRubyOracle(t, bin, "escape", corpus)
	if len(want) != len(corpus) {
		t.Fatalf("oracle returned %d results for %d inputs", len(want), len(corpus))
	}
	for i, in := range corpus {
		got := Escape(in)
		w := want[i]
		if !w.OK {
			// MRI's shellescape only errors on NUL, which the corpus omits.
			t.Fatalf("unexpected MRI escape error for %q: %q", in, w.Err)
		}
		if len(w.Words) != 1 {
			t.Fatalf("escape oracle returned %d words for %q", len(w.Words), in)
		}
		if got != w.Words[0] {
			t.Errorf("Escape(%q) = %q, MRI = %q", in, got, w.Words[0])
		}
	}
}

// TestOracleJoin checks Join matches MRI's Shellwords.shelljoin for a set of
// argument lists, and that the result splits back to the original list.
func TestOracleJoin(t *testing.T) {
	bin := rubyBin(t)
	lists := [][]string{
		{},
		{""},
		{"a"},
		{"a", "b c", "Don't"},
		{"ls", "-lta", "--", "Funny GIFs"},
		{"There's", "a", "time", "and", "place"},
		{"café", "日本", "100%"},
		{"$x", "`y`", "a|b", "a&b"},
		{"multi\nline", "tab\there"},
		{"", "x", ""},
	}
	for _, list := range lists {
		got := Join(list)
		want := strings.TrimRight(rubyJoin(t, bin, list), "\n")
		if got != want {
			t.Errorf("Join(%#v) = %q, MRI = %q", list, got, want)
		}
		// And the join must split back to the original list.
		back, err := Split(got)
		if err != nil {
			t.Errorf("Split(Join(%#v)=%q): %v", list, got, err)
			continue
		}
		if len(list) != 0 && !eqStrings(back, list) {
			t.Errorf("round-trip: Split(Join(%#v)) = %#v", list, back)
		}
	}
}

// rubyJoin invokes MRI's Shellwords.shelljoin on one argument list via a single
// process, exchanging base64 to stay byte-faithful.
func rubyJoin(t *testing.T, bin string, list []string) string {
	t.Helper()
	enc := make([]string, len(list))
	for i, s := range list {
		enc[i] = b64(s)
	}
	in, err := json.Marshal(enc)
	if err != nil {
		t.Fatalf("marshal join list: %v", err)
	}
	script := `
require 'shellwords'
require 'json'
require 'base64'
$stdin.binmode
$stdout.binmode
args = JSON.parse($stdin.read).map { |b| Base64.strict_decode64(b).force_encoding('UTF-8') }
$stdout.write(Base64.strict_encode64(Shellwords.shelljoin(args)))
`
	cmd := exec.Command(bin, "-e", script)
	cmd.Stdin = strings.NewReader(string(in))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ruby shelljoin failed: %v\nstderr:\n%s", err, stderr.String())
	}
	return unb64(t, stdout.String())
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
