// Package shellwords is a pure-Go, CGO-free reimplementation of Ruby's
// `shellwords` standard library (MRI's lib/shellwords.rb).
//
// It manipulates strings according to the word-parsing rules of the UNIX
// Bourne shell: splitting a command line into words ([Split]), escaping a
// single argument so the shell parses it back verbatim ([Escape]), and
// joining an argument list into a single escaped command line ([Join]).
//
// The behaviour is matched byte-for-byte against MRI Ruby 4.x. The mapping to
// the Ruby API is:
//
//	Split  <-> Shellwords.shellsplit / Shellwords.split / String#shellsplit
//	Escape <-> Shellwords.shellescape / Shellwords.escape / String#shellescape
//	Join   <-> Shellwords.shelljoin / Shellwords.join / Array#shelljoin
package shellwords

import (
	"strings"
	"unicode/utf8"
)

// ArgumentError mirrors Ruby's ArgumentError as raised by Shellwords.shellsplit
// (and shellescape). [Split] returns it for an unmatched quote, a dangling
// backslash that forms an invalid token, or a NUL character; [Escape] panics
// with it via a Go error only when used through the strict helpers — see
// individual functions. Its Error string reproduces MRI's message exactly,
// e.g. "Unmatched quote at 0: \"".
type ArgumentError struct {
	// Message is the full Ruby-compatible message, e.g.
	// "Unmatched quote at 2: ...'" or "Nul character at 0: \x00".
	Message string
}

func (e *ArgumentError) Error() string { return e.Message }

// Split splits line into an array of tokens the same way the UNIX Bourne shell
// does. It is the exact analogue of Ruby's Shellwords.shellsplit /
// String#shellsplit.
//
// Quotes ('single' and "double") and backslash escapes are honoured;
// concatenation within a single word is supported (a"b"c -> abc). A trailing
// unmatched quote (or any other character that cannot start a token) yields an
// *ArgumentError with the message "Unmatched quote at N: ...". A NUL character
// yields "Nul character at N: ...". line must not contain NUL characters
// because of the nature of the exec(2) system call.
//
// Note that this is not a full command-line parser: shell metacharacters other
// than the quotes and backslash (such as | or >) are not treated specially.
func Split(line string) ([]string, error) {
	var words []string
	var field strings.Builder

	pos := 0
	n := len(line)
	for pos < n {
		start := pos // start of the whole match, including leading whitespace

		// \s* : consume leading shell whitespace. If only whitespace remains,
		// \G\s*(...)(\s|\z)? still matches via the \z arm with no token, so the
		// loop simply ends (any in-progress word was already flushed by the
		// separator arm below, so there is nothing to emit here).
		pos = skipSpace(line, pos)
		if pos >= n {
			break
		}

		segStart := pos // first byte of the chosen alternative
		c := line[pos]

		switch {
		case c == '\'':
			// '([^\0\']*)'
			end, ok := scanSingle(line, pos)
			if !ok {
				return nil, garbage(line, start, segStart)
			}
			field.WriteString(line[pos+1 : end])
			pos = end + 1

		case c == '"':
			// "((?:[^\0\"\\]|\\[^\0])*)"
			body, end, ok := scanDouble(line, pos)
			if !ok {
				return nil, garbage(line, start, segStart)
			}
			field.WriteString(body)
			pos = end + 1

		case c == '\\':
			// (\\[^\0]?) : backslash, optionally one non-NUL char. The
			// esc.gsub(/\\(.)/, '\1') drops the backslash and keeps the
			// (possibly multibyte) following char; a lone trailing backslash, or
			// a backslash before NUL (which [^\0]? cannot consume), stays "\".
			if pos+1 < n && line[pos+1] != 0 {
				field.WriteByte(line[pos+1])
				pos += 2
			} else {
				field.WriteByte('\\')
				pos++
			}

		case c == 0:
			// NUL can never start a token: it is the "garbage" branch.
			return nil, garbage(line, start, segStart)

		default:
			// ([^\0\s\\'"]+) : a bare word run (at least this one char).
			end := scanBare(line, pos)
			field.WriteString(line[pos:end])
			pos = end
		}

		// (\s|\z)? : an optional trailing separator ends the current word. End of
		// input also ends it via the \z arm.
		if pos >= n {
			words = append(words, field.String())
			field.Reset()
		} else if isSpace(line[pos]) {
			words = append(words, field.String())
			field.Reset()
			pos++ // consume the single separating whitespace char
		}
	}

	return words, nil
}

// garbage builds the *ArgumentError for the regex "garbage" branch, where a
// single character cannot start any valid token (an unmatched quote, or a NUL).
// It reproduces MRI's message:
//
//	b    = $~.begin(0)              -> char offset of the whole match
//	line = $~[0]                    -> matched text = leading WS + the one char
//	line = "..." + line if b > 0
//	"#{nul ? 'Nul character' : 'Unmatched quote'} at #{b}: #{line}"
func garbage(line string, start, segStart int) *ArgumentError {
	ch := line[segStart] // the single offending char (a quote or NUL)
	// $~[0] is the consumed leading whitespace plus exactly one character.
	_, sz := utf8.DecodeRuneInString(line[segStart:])
	matched := line[start : segStart+sz]
	prefix := ""
	if start > 0 {
		prefix = "..." // b > 0
	}
	kind := "Unmatched quote"
	if ch == 0 {
		kind = "Nul character"
	}
	// MRI reports the offset in characters; any earlier multibyte char would be
	// a valid bare-word char, so the count is taken over runes to match exactly.
	b := utf8.RuneCountInString(line[:start])
	return &ArgumentError{Message: kind + " at " + itoa(b) + ": " + prefix + matched}
}

// itoa renders a non-negative int without importing strconv, keeping the
// package's dependency surface to the standard string/unicode helpers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// scanSingle matches '([^\0\']*)' starting at the opening quote at i. It
// returns the index of the closing quote and ok=true, or ok=false if the quote
// is never closed (or a NUL appears before it).
func scanSingle(line string, i int) (end int, ok bool) {
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '\'':
			return j, true
		case 0:
			return 0, false
		}
	}
	return 0, false
}

// scanDouble matches "((?:[^\0\"\\]|\\[^\0])*)" starting at the opening quote
// at i. It returns the de-escaped body, the index of the closing quote, and
// ok=true; ok=false if the quote is never closed (or a NUL appears).
//
// Per POSIX 2.2.3, inside double quotes a backslash only escapes one of
// $ ` " \ <newline>; before any other character the backslash is retained.
func scanDouble(line string, i int) (body string, end int, ok bool) {
	var b strings.Builder
	j := i + 1
	for j < len(line) {
		c := line[j]
		switch c {
		case '"':
			return b.String(), j, true
		case 0:
			return "", 0, false
		case '\\':
			if j+1 >= len(line) {
				// Trailing backslash with no following char: the inner
				// alternation \\[^\0] cannot match, so the closing quote is
				// never found -> unmatched.
				return "", 0, false
			}
			nc := line[j+1]
			if nc == 0 {
				return "", 0, false
			}
			switch nc {
			case '$', '`', '"', '\\', '\n':
				b.WriteByte(nc) // gsub(/\\([$`"\\\n])/, '\1')
			default:
				b.WriteByte('\\')
				b.WriteByte(nc)
			}
			j += 2
		default:
			b.WriteByte(c)
			j++
		}
	}
	return "", 0, false
}

// scanBare matches ([^\0\s\\'"]+) starting at i and returns the index just past
// the run (which is >= i+1 for a valid bare-word start).
func scanBare(line string, i int) int {
	j := i
	for j < len(line) {
		c := line[j]
		if c == 0 || isSpace(c) || c == '\\' || c == '\'' || c == '"' {
			break
		}
		j++
	}
	return j
}

// skipSpace advances past a run of shell whitespace (\s), returning the new
// index.
func skipSpace(line string, i int) int {
	for i < len(line) && isSpace(line[i]) {
		i++
	}
	return i
}

// isSpace reports whether b is part of Ruby's regex \s class: space, tab,
// newline, carriage return, form feed and vertical tab.
func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

// Escape escapes s so that it can be safely used as a single argument in a
// Bourne shell command line. It is the exact analogue of Ruby's
// Shellwords.shellescape / String#shellescape.
//
// An empty string returns "”" (two single quotes). Otherwise every character
// outside the safe set [A-Za-z0-9_\-.,:+/@\n] is backslash-escaped, and each
// newline is then rewritten as '\n' (a backslash cannot escape a newline,
// since backslash+newline is a shell line continuation).
//
// Multibyte characters are treated as whole characters, not bytes: a non-safe
// rune is prefixed with a single backslash. It is the caller's responsibility
// to encode the string in the right encoding for the target shell.
func Escape(s string) string {
	// An empty argument would be skipped by the shell, so return empty quotes.
	if s == "" {
		return "''"
	}

	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c == '\n' {
				// \n is in the safe set, so the first gsub leaves it; the
				// second gsub rewrites it as '\n'.
				b.WriteString("'\n'")
			} else if isSafe(c) {
				b.WriteByte(c)
			} else {
				b.WriteByte('\\')
				b.WriteByte(c)
			}
			i++
			continue
		}
		// Multibyte rune: escape the whole rune with one leading backslash.
		_, sz := utf8.DecodeRuneInString(s[i:])
		b.WriteByte('\\')
		b.WriteString(s[i : i+sz])
		i += sz
	}
	return b.String()
}

// isSafe reports whether the ASCII byte c is in MRI's safe set
// [A-Za-z0-9_\-.,:+/@\n] (excluding \n, which Escape handles specially).
func isSafe(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '_', '-', '.', ',', ':', '+', '/', '@':
		return true
	}
	return false
}

// Join builds a command-line string from words, escaping each element with
// [Escape] and joining them with a single space. It is the exact analogue of
// Ruby's Shellwords.shelljoin / Array#shelljoin. An empty slice yields "".
func Join(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return Escape(words[0])
	}
	var b strings.Builder
	for i, w := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(Escape(w))
	}
	return b.String()
}
