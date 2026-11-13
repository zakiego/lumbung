// Package sqlsplit cuts SQL text into statements: after each semicolon
// outside quotes, bracketed identifiers and comments, and keeping a
// CREATE TRIGGER body (which holds semicolons) whole up to its END.
package sqlsplit

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// Reader yields chunks that end at a semicolon outside quotes and comments.
// A chunk may stop inside a trigger body; Statements joins those.
type Reader struct {
	r    *bufio.Reader
	Read int64 // bytes consumed so far
}

// NewReader wraps r.
func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReaderSize(r, 64<<10)} }

// Next returns the next chunk, including its semicolon. At the end of input
// it returns the remainder and io.EOF.
func (s *Reader) Next() (string, error) {
	var b strings.Builder
	var quote rune // ', ", ` or ] while inside one
	lineComment, blockComment := false, false
	var prev rune
	for {
		c, size, err := s.r.ReadRune()
		if err != nil {
			return b.String(), err
		}
		s.Read += int64(size)
		b.WriteRune(c)
		switch {
		case lineComment:
			if c == '\n' {
				lineComment = false
			}
		case blockComment:
			if prev == '*' && c == '/' {
				blockComment = false
				c = 0
			}
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '[':
			quote = ']'
		case prev == '-' && c == '-':
			lineComment = true
		case prev == '/' && c == '*':
			blockComment = true
			c = 0
		case c == ';':
			return b.String(), nil
		}
		prev = c
	}
}

var (
	triggerStart = regexp.MustCompile(`(?is)^\s*CREATE\s+(TEMP\s+|TEMPORARY\s+)?TRIGGER\b`)
	endKeyword   = regexp.MustCompile(`(?is)\bEND\s*;\s*$`)
)

// StripComments removes leading comments and whitespace, to look at how a
// statement starts.
func StripComments(s string) string {
	for {
		t := strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(t, "--"):
			i := strings.IndexByte(t, '\n')
			if i < 0 {
				return ""
			}
			s = t[i+1:]
		case strings.HasPrefix(t, "/*"):
			i := strings.Index(t, "*/")
			if i < 0 {
				return ""
			}
			s = t[i+2:]
		default:
			return t
		}
	}
}

// Statements splits text into statements, dropping ones that hold only
// comments and whitespace.
func Statements(text string) []string {
	r := NewReader(strings.NewReader(text))
	var out []string
	var pending strings.Builder
	for {
		chunk, err := r.Next()
		pending.WriteString(chunk)
		cur := pending.String()
		body := StripComments(cur)
		done := err != nil
		inTrigger := triggerStart.MatchString(body) && !endKeyword.MatchString(strings.TrimRight(cur, " \t\r\n"))
		if !inTrigger || done {
			if strings.TrimSpace(body) != "" {
				out = append(out, strings.TrimSpace(cur))
			}
			pending.Reset()
		}
		if done {
			return out
		}
	}
}
