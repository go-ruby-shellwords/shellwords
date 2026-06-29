package shellwords

import (
	"errors"
	"reflect"
	"testing"
)

// splitCase is one deterministic, ruby-free golden vector captured from MRI
// Ruby 4.x (`ruby -rshellwords -e`). These keep the package at 100% coverage
// on the no-ruby / qemu / Windows CI lanes, where the differential oracle in
// oracle_test.go skips itself.
type splitCase struct {
	in     string
	out    []string
	errMsg string // non-empty => Split must return *ArgumentError with this message
}

var splitGolden = []splitCase{
	// Empty and whitespace-only lines yield no words.
	{in: "", out: nil},
	{in: " ", out: nil},
	{in: "   ", out: nil},
	{in: "\t", out: nil},
	{in: "\n", out: nil},
	{in: "\r", out: nil},
	{in: "\f", out: nil},
	{in: "\v", out: nil},
	// Bare words.
	{in: "a", out: []string{"a"}},
	{in: "a b c", out: []string{"a", "b", "c"}},
	{in: "  a  b  ", out: []string{"a", "b"}},
	{in: "x=1 y=2", out: []string{"x=1", "y=2"}},
	{in: "a;b&c|d", out: []string{"a;b&c|d"}},
	{in: "a > b < c", out: []string{"a", ">", "b", "<", "c"}},
	{in: "ruby my_prog.rb | less", out: []string{"ruby", "my_prog.rb", "|", "less"}},
	// Multibyte bare words.
	{in: "café latte", out: []string{"café", "latte"}},
	{in: "100%", out: []string{"100%"}},
	// Double quotes.
	{in: `three blind "mice"`, out: []string{"three", "blind", "mice"}},
	{in: `here are "two words"`, out: []string{"here", "are", "two words"}},
	{in: `"foo bar" baz`, out: []string{"foo bar", "baz"}},
	{in: `""`, out: []string{""}},
	{in: `a"b"c`, out: []string{"abc"}},
	// POSIX 2.2.3: inside dq, backslash only escapes $ ` " \ <newline>.
	{in: `"a\$b"`, out: []string{"a$b"}},
	{in: "\"a\\`b\"", out: []string{"a`b"}},
	{in: `"a\"b"`, out: []string{`a"b`}},
	{in: `"a\\b"`, out: []string{`a\b`}},
	{in: "\"a\\\nb\"", out: []string{"a\nb"}},
	{in: `"a\tb"`, out: []string{`a\tb`}}, // \t retained verbatim
	// Single quotes (no escapes inside).
	{in: `'single with spaces'`, out: []string{"single with spaces"}},
	{in: `''`, out: []string{""}},
	{in: `a''b`, out: []string{"ab"}},
	{in: `a'b'c`, out: []string{"abc"}},
	{in: `'a\b'`, out: []string{`a\b`}}, // backslash literal inside sq
	// Backslash escapes outside quotes.
	{in: `foo\ bar`, out: []string{"foo bar"}},
	{in: `a\ b\ c`, out: []string{"a b c"}},
	{in: `\n`, out: []string{"n"}},
	{in: `\\`, out: []string{`\`}},
	{in: `\"`, out: []string{`"`}},
	{in: `a\`, out: []string{`a\`}}, // trailing lone backslash kept
	{in: `\`, out: []string{`\`}},
	// Concatenation across token kinds within a single word. A backslash-escaped
	// space does not terminate the word, so the whole thing is one token.
	{in: `a"b"'c'\ d`, out: []string{"abc d"}},
	{in: `pre"mid"post`, out: []string{"premidpost"}},
	// Tabs between words.
	{in: "tab\there", out: []string{"tab", "here"}},
	// Errors: unmatched quotes and NUL.
	{in: `'`, errMsg: `Unmatched quote at 0: '`},
	{in: `"`, errMsg: `Unmatched quote at 0: "`},
	{in: `'unmatched`, errMsg: `Unmatched quote at 0: '`},
	{in: `"unmatched`, errMsg: `Unmatched quote at 0: "`},
	{in: `ab 'cd`, errMsg: `Unmatched quote at 3: ...'`},
	{in: `  "x`, errMsg: "Unmatched quote at 0:   \""},
	{in: `a "x`, errMsg: `Unmatched quote at 2: ..."`},
	{in: "\x00", errMsg: "Nul character at 0: \x00"},
	{in: "a\x00b", errMsg: "Nul character at 1: ...\x00"},
	{in: "\"a\x00b\"", errMsg: "Unmatched quote at 0: \""}, // nul in dq -> quote garbage
	{in: "'a\x00b'", errMsg: "Unmatched quote at 0: '"},    // nul in sq -> quote garbage
	{in: "\"a\x00b", errMsg: "Unmatched quote at 0: \""},
	// Backslash-then-NUL inside double quotes: the inner \\[^\0] arm cannot
	// consume the NUL, so the quote never closes -> unmatched.
	{in: "\"a\\\x00b\"", errMsg: "Unmatched quote at 0: \""},
	// Trailing backslash inside double quotes (no following char) -> unmatched.
	{in: "\"a\\", errMsg: "Unmatched quote at 0: \""},
	// Escaped quote with no real closing quote -> unmatched.
	{in: `"a\"`, errMsg: `Unmatched quote at 0: "`},
	// Backslash then NUL: the (\\[^\0]?) token is just "\" (NUL not consumed),
	// then the NUL is hit as garbage at the next position.
	{in: "a\\\x00b", errMsg: "Nul character at 2: ...\x00"},
	{in: "\\\x00", errMsg: "Nul character at 1: ...\x00"},
}

func TestSplit(t *testing.T) {
	for _, tc := range splitGolden {
		got, err := Split(tc.in)
		if tc.errMsg != "" {
			if err == nil {
				t.Errorf("Split(%q) = %q, want error %q", tc.in, got, tc.errMsg)
				continue
			}
			var ae *ArgumentError
			if !errors.As(err, &ae) {
				t.Errorf("Split(%q) error type = %T, want *ArgumentError", tc.in, err)
				continue
			}
			if ae.Error() != tc.errMsg {
				t.Errorf("Split(%q) err = %q, want %q", tc.in, ae.Error(), tc.errMsg)
			}
			continue
		}
		if err != nil {
			t.Errorf("Split(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.out) {
			t.Errorf("Split(%q) = %#v, want %#v", tc.in, got, tc.out)
		}
	}
}

type escapeCase struct {
	in  string
	out string
}

var escapeGolden = []escapeCase{
	{in: "", out: "''"},
	{in: "simple", out: "simple"},
	{in: "a b", out: `a\ b`},
	{in: "It's", out: `It\'s`},
	{in: "a.b,c:d+e/f@g-h_i", out: "a.b,c:d+e/f@g-h_i"}, // all safe
	{in: "abcXYZ012", out: "abcXYZ012"},
	{in: "a$b`c\"d", out: `a\$b\` + "`" + `c\"d`},
	{in: "tab\there", out: "tab\\\there"},
	{in: "100%", out: `100\%`},
	{in: "a*b?c[d]", out: `a\*b\?c\[d\]`},
	// Newline is special: it is in the safe set, then rewritten as '\n'.
	{in: "\n", out: "'\n'"},
	{in: "a\nb", out: "a'\n'b"},
	{in: "\n\n", out: "'\n''\n'"},
	// Multibyte: each non-safe rune gets a single leading backslash.
	{in: "café", out: `caf\é`},
	{in: "日本", out: `\日\本`},
	{in: "a😀b", out: `a\😀b`},
}

func TestEscape(t *testing.T) {
	for _, tc := range escapeGolden {
		if got := Escape(tc.in); got != tc.out {
			t.Errorf("Escape(%q) = %q, want %q", tc.in, got, tc.out)
		}
	}
}

type joinCase struct {
	in  []string
	out string
}

var joinGolden = []joinCase{
	{in: nil, out: ""},
	{in: []string{}, out: ""},
	{in: []string{""}, out: "''"},
	{in: []string{"a"}, out: "a"},
	{in: []string{"a", "b c", "Don't"}, out: `a b\ c Don\'t`},
	{in: []string{"There's", "a", "place"}, out: `There\'s a place`},
	{in: []string{"", ""}, out: "'' ''"},
	{in: []string{"ls", "-lta", "--", "Funny GIFs"}, out: `ls -lta -- Funny\ GIFs`},
}

func TestJoin(t *testing.T) {
	for _, tc := range joinGolden {
		if got := Join(tc.in); got != tc.out {
			t.Errorf("Join(%#v) = %q, want %q", tc.in, got, tc.out)
		}
	}
}

// TestArgumentError exercises the error value's String/Error method directly.
func TestArgumentError(t *testing.T) {
	e := &ArgumentError{Message: "Unmatched quote at 0: \""}
	if e.Error() != "Unmatched quote at 0: \"" {
		t.Errorf("Error() = %q", e.Error())
	}
}

// TestSplitJoinRoundTrip checks the documented invariant Split(Join(x)) == x for
// a range of awkward inputs, matching MRI's guarantee that escape produces a
// form the shell parses back to the original argument.
func TestSplitJoinRoundTrip(t *testing.T) {
	corpus := [][]string{
		{"a", "b", "c"},
		{"a b", "c d"},
		{"It's", "a \"quote\""},
		{"", "x", ""},
		{"tab\there", "new\nline"},
		{"$HOME", "`cmd`", "a|b", "a&b", "a;b"},
		{"café", "日本", "100%"},
		{"-flag", "--long=value", "path/to/file.txt"},
		{"\\backslash", "back\\slash"},
		{"a*b", "c?d", "[e]"},
	}
	for _, words := range corpus {
		joined := Join(words)
		got, err := Split(joined)
		if err != nil {
			t.Errorf("Split(Join(%#v)=%q) error: %v", words, joined, err)
			continue
		}
		// Normalise nil vs empty for comparison.
		if len(got) == 0 && len(words) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, words) {
			t.Errorf("round-trip mismatch: Join=%q Split=%#v want %#v", joined, got, words)
		}
	}
}
