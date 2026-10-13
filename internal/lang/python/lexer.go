// Package python tokenizes Python source and evaluates the static parts route
// code is made of (string literals, f-strings over constants, `+`).
//
// Tokens carry their logical line's indentation, which is enough to tell which
// methods belong to a class and which decorators sit on which function, without
// a full parser. Comments, all string forms (prefixes, triple quotes, f-strings
// with nested quotes) and implicit line joining inside brackets are handled exactly.
package python

import (
	"strings"
)

type Kind int

const (
	Name   Kind = iota // identifiers and keywords
	Number             //
	String             // any string literal, prefix and quotes included
	Op                 // operators, brackets, punctuation
)

type Token struct {
	Kind      Kind
	Text      string
	Line      int
	Indent    int  // indentation of the logical line the token belongs to
	LineStart bool // first token of its logical line
}

func (t Token) IsOp(text string) bool   { return t.Kind == Op && t.Text == text }
func (t Token) IsName(text string) bool { return t.Kind == Name && t.Text == text }

var ops = []string{"**=", "//=", ">>=", "<<=", "...", "->", ":=", "**", "//", "<<", ">>", "<=", ">=",
	"==", "!=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "@="}

func isNameStart(c byte) bool {
	return c == '_' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isNamePart(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }

// stringPrefix reports whether name is a valid string prefix (r, b, f, u, t and combinations).
func stringPrefix(name string) bool {
	if len(name) > 2 {
		return false
	}
	seen := map[byte]bool{}
	for i := 0; i < len(name); i++ {
		c := name[i] | 0x20 // lower-case
		if !strings.ContainsRune("rbfut", rune(c)) || seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}

// Tokenize splits Python source into tokens, dropping comments and blank lines.
func Tokenize(src string) []Token {
	var toks []Token
	i, n, line := 0, len(src), 1
	depth := 0
	atLineStart, indent := true, 0
	for i < n {
		if atLineStart && depth == 0 {
			col := 0
			for i < n && (src[i] == ' ' || src[i] == '\t' || src[i] == '\f') {
				if src[i] == '\t' {
					col = (col/8 + 1) * 8
				} else {
					col++
				}
				i++
			}
			if i >= n {
				break
			}
			if src[i] == '\n' || src[i] == '\r' || src[i] == '#' { // blank or comment-only line
				for i < n && src[i] != '\n' {
					i++
				}
				if i < n {
					i++
					line++
				}
				continue
			}
			indent = col
		}
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
			if depth == 0 {
				atLineStart = true
			}
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			i++
			continue
		case c == '#':
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		case c == '\\' && i+1 < n && (src[i+1] == '\n' || src[i+1] == '\r'): // explicit line joining
			i++
			if src[i] == '\r' {
				i++
			}
			if i < n && src[i] == '\n' {
				i++
			}
			line++
			continue
		}

		start, kind := i, Op
		switch {
		case isNameStart(c):
			for i < n && isNamePart(src[i]) {
				i++
			}
			kind = Name
			if i < n && (src[i] == '\'' || src[i] == '"') && stringPrefix(src[start:i]) {
				i = skipString(src, i, strings.ContainsAny(src[start:i], "fFtT"))
				kind = String
			}
		case c >= '0' && c <= '9' || (c == '.' && i+1 < n && src[i+1] >= '0' && src[i+1] <= '9'):
			for i < n && (isNamePart(src[i]) || src[i] == '.' ||
				((src[i] == '+' || src[i] == '-') && (src[i-1] == 'e' || src[i-1] == 'E'))) {
				i++
			}
			kind = Number
		case c == '\'' || c == '"':
			i = skipString(src, i, false)
			kind = String
		default:
			i += opLength(src, i)
			switch src[start] {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				if depth > 0 {
					depth--
				}
			}
		}
		text := src[start:i]
		toks = append(toks, Token{Kind: kind, Text: text, Line: line, Indent: indent, LineStart: atLineStart})
		line += strings.Count(text, "\n")
		atLineStart = false
	}
	return toks
}

func opLength(src string, i int) int {
	for _, op := range ops {
		if strings.HasPrefix(src[i:], op) {
			return len(op)
		}
	}
	return 1
}

// skipString returns the index just past the string literal whose quote is at src[i].
// In f-strings, quotes inside {...} replacement fields don't end the string (Python 3.12+).
func skipString(src string, i int, fstring bool) int {
	n := len(src)
	quote := src[i : i+1]
	if strings.HasPrefix(src[i:], strings.Repeat(quote, 3)) {
		quote = strings.Repeat(quote, 3)
	}
	j := i + len(quote)
	for j < n {
		switch {
		case src[j] == '\\':
			j += 2
		case strings.HasPrefix(src[j:], quote):
			return j + len(quote)
		case src[j] == '\n' && len(quote) == 1:
			return j // unterminated
		case fstring && src[j] == '{':
			if j+1 < n && src[j+1] == '{' {
				j += 2
			} else {
				j = skipField(src, j)
			}
		default:
			j++
		}
	}
	return n
}

// skipField skips an f-string replacement field {...}, which may contain strings and brackets.
func skipField(src string, i int) int {
	depth, j, n := 0, i, len(src)
	for j < n {
		c := src[j]
		switch {
		case c == '{' || c == '(' || c == '[':
			depth++
		case c == '}' || c == ')' || c == ']':
			depth--
			if depth == 0 {
				return j + 1
			}
		case c == '\'' || c == '"':
			j = skipString(src, j, false)
			continue
		case c == '\n':
			return j
		}
		j++
	}
	return n
}

// MatchClose returns the index of the token closing the bracket at toks[i], or -1.
func MatchClose(toks []Token, i int) int {
	depth := 0
	for k := i; k < len(toks); k++ {
		if toks[k].Kind != Op {
			continue
		}
		switch toks[k].Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}

// SplitTop splits on an operator that isn't nested in brackets; empty parts are dropped.
func SplitTop(toks []Token, sep string) [][]Token {
	var parts [][]Token
	depth, start := 0, 0
	for k, t := range toks {
		if t.Kind != Op {
			continue
		}
		switch t.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		case sep:
			if depth == 0 {
				if k > start {
					parts = append(parts, toks[start:k])
				}
				start = k + 1
			}
		}
	}
	if start < len(toks) {
		parts = append(parts, toks[start:])
	}
	return parts
}

// Lines groups tokens into logical lines.
func Lines(toks []Token) [][]Token {
	var lines [][]Token
	start := 0
	for k := 1; k <= len(toks); k++ {
		if k == len(toks) || toks[k].LineStart {
			lines = append(lines, toks[start:k])
			start = k
		}
	}
	return lines
}

// Dotted returns "a.b.c" if toks are exactly a name chain.
func Dotted(toks []Token) (string, bool) {
	if len(toks) == 0 || len(toks)%2 == 0 {
		return "", false
	}
	parts := make([]string, 0, len(toks)/2+1)
	for k, t := range toks {
		if k%2 == 0 {
			if t.Kind != Name {
				return "", false
			}
			parts = append(parts, t.Text)
		} else if !t.IsOp(".") {
			return "", false
		}
	}
	return strings.Join(parts, "."), true
}

// Args is a parsed call argument list.
type Args struct {
	Pos [][]Token
	Kw  map[string][]Token
}

// Arg returns positional argument i, or else keyword argument name (nil if neither).
func (a Args) Arg(i int, name string) []Token {
	if i >= 0 && i < len(a.Pos) {
		return a.Pos[i]
	}
	return a.Kw[name]
}

// ParseArgs reads the call arguments starting at the "(" at toks[open].
func ParseArgs(toks []Token, open int) (Args, int) {
	args := Args{Kw: map[string][]Token{}}
	close := MatchClose(toks, open)
	if close < 0 {
		return args, -1
	}
	for _, part := range SplitTop(toks[open+1:close], ",") {
		switch {
		case len(part) >= 2 && part[0].Kind == Name && part[1].IsOp("="):
			args.Kw[part[0].Text] = part[2:]
		case part[0].IsOp("*") || part[0].IsOp("**"):
		default:
			args.Pos = append(args.Pos, part)
		}
	}
	return args, close
}
