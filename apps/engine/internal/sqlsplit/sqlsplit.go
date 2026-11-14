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

var triggerStart = regexp.MustCompile(`(?is)^\s*CREATE\s+(TEMP\s+|TEMPORARY\s+)?TRIGGER\b`)

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

// IsWordByte reports whether c can be part of a bare SQL word (a keyword or
// an unquoted identifier, ASCII only).
func IsWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// blockClosed reports whether a CREATE TRIGGER statement's body has ended:
// every BEGIN and CASE outside quotes and comments is matched by an END.
func blockClosed(stmt string) bool {
	depth, opened := 0, false
	for _, w := range words(stmt) {
		switch strings.ToUpper(w) {
		case "BEGIN":
			depth++
			opened = true
		case "CASE":
			depth++
		case "END":
			depth--
		}
	}
	return opened && depth <= 0
}

// words returns the bare words of SQL text, skipping quoted strings,
// quoted identifiers and comments.
func words(s string) []string {
	var out []string
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\'' || c == '"' || c == '`' || c == '[':
			end := c
			if c == '[' {
				end = ']'
			}
			for i++; i < len(rs) && rs[i] != end; i++ {
			}
		case c == '-' && i+1 < len(rs) && rs[i+1] == '-':
			for ; i < len(rs) && rs[i] != '\n'; i++ {
			}
		case c == '/' && i+1 < len(rs) && rs[i+1] == '*':
			for i += 2; i+1 < len(rs) && !(rs[i] == '*' && rs[i+1] == '/'); i++ {
			}
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(rs) && (rs[j] == '_' || rs[j] == '$' || rs[j] >= 'a' && rs[j] <= 'z' || rs[j] >= 'A' && rs[j] <= 'Z' || rs[j] >= '0' && rs[j] <= '9') {
				j++
			}
			out = append(out, string(rs[i:j]))
			i = j - 1
		}
	}
	return out
}

// Split returns the complete statements at the start of text, and the rest:
// an unfinished statement, or "" when only whitespace and comments remain.
// A statement is complete at a semicolon outside quotes and comments, and a
// CREATE TRIGGER only once its body's END is reached.
func Split(text string) ([]string, string) {
	r := NewReader(strings.NewReader(text))
	var out []string
	var pending strings.Builder
	for {
		chunk, err := r.Next()
		pending.WriteString(chunk)
		cur := pending.String()
		if err != nil {
			if strings.TrimSpace(StripComments(cur)) == "" {
				return out, ""
			}
			return out, cur
		}
		body := StripComments(cur)
		if triggerStart.MatchString(body) && !blockClosed(cur) {
			continue
		}
		if strings.TrimSpace(body) != "" {
			out = append(out, strings.TrimSpace(cur))
		}
		pending.Reset()
	}
}

// Statements splits text into statements, dropping ones that hold only
// comments and whitespace. Unfinished text at the end becomes the last
// statement, so the server reports what's wrong with it.
func Statements(text string) []string {
	out, rest := Split(text)
	if rest != "" {
		out = append(out, strings.TrimSpace(rest))
	}
	return out
}
