// Package js tokenizes JavaScript / TypeScript and evaluates the static parts
// route code is made of (string literals, templates, constants, `+`).
//
// It is a lexer, not a full parser: comments, strings, template literals and
// regex literals are recognised exactly, so a route inside a comment or a string
// is never mistaken for code. Framework rules then match patterns on tokens
// (e.g. Ident . get ( String , ... )). That covers how routes are written in
// practice, builds without cgo, and handles TypeScript's extra syntax by skipping it.
package js

import (
	"strings"
)

type Kind int

const (
	Ident    Kind = iota // identifiers and keywords
	Punct                // operators and brackets
	String               // '...' or "..."
	Template             // `...`, including any ${...}
	Regex                // /.../flags
	Number
)

type Token struct {
	Kind          Kind
	Text          string // raw source text
	Line          int    // 1-based line of the first character
	Pos           int    // byte offset in the source
	NewlineBefore bool   // a line break separates it from the previous token
}

func (t Token) Is(kind Kind, text string) bool { return t.Kind == kind && t.Text == text }
func (t Token) IsPunct(text string) bool       { return t.Kind == Punct && t.Text == text }
func (t Token) IsIdent(text string) bool       { return t.Kind == Ident && t.Text == text }
func (t Token) End() int                       { return t.Pos + len(t.Text) }

var puncts = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--", "+=", "-=", "*=", "/=",
	"%=", "&=", "|=", "^=", "**", "<<", ">>",
}

// A '/' after one of these (or at the start) begins a regex literal, not a division.
const regexPreceders = "(,=:[!&|?{};+-*%<>~^"

var regexKeywords = map[string]bool{
	"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true, "of": true,
	"new": true, "delete": true, "void": true, "throw": true, "yield": true, "await": true, "instanceof": true,
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c == '#' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }

// Tokenize splits source into tokens, dropping whitespace and comments.
func Tokenize(src string) []Token {
	var toks []Token
	line, newline := 1, false
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			line++
			newline = true
			i++
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
			continue
		case strings.HasPrefix(src[i:], "//"):
			if j := strings.IndexByte(src[i:], '\n'); j >= 0 {
				i += j
			} else {
				i = n
			}
			continue
		case strings.HasPrefix(src[i:], "/*"):
			end := n
			if j := strings.Index(src[i+2:], "*/"); j >= 0 {
				end = i + 2 + j + 2
			}
			if k := strings.Count(src[i:end], "\n"); k > 0 {
				line += k
				newline = true
			}
			i = end
			continue
		}

		start, kind := i, Punct
		switch {
		case isIdentStart(c):
			for i < n && isIdentPart(src[i]) {
				i++
			}
			kind = Ident
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(src[i+1])):
			for i < n && (isIdentPart(src[i]) || src[i] == '.' ||
				((src[i] == '+' || src[i] == '-') && (src[i-1] == 'e' || src[i-1] == 'E'))) {
				i++
			}
			kind = Number
		case c == '\'' || c == '"':
			i = skipString(src, i)
			kind = String
		case c == '`':
			i = skipTemplate(src, i)
			kind = Template
		case c == '/' && tokenRegexAllowed(toks):
			if end := skipRegex(src, i); end > i+1 {
				i, kind = end, Regex
			} else {
				i++
			}
		default:
			i += punctLength(src, i)
		}
		text := src[start:i]
		toks = append(toks, Token{Kind: kind, Text: text, Line: line, Pos: start, NewlineBefore: newline})
		line += strings.Count(text, "\n")
		newline = false
	}
	return toks
}

func punctLength(src string, i int) int {
	for _, p := range puncts {
		if strings.HasPrefix(src[i:], p) {
			if p == "?." && i+2 < len(src) && isDigit(src[i+2]) {
				continue // a ? .5 : b
			}
			return len(p)
		}
	}
	return 1
}

func tokenRegexAllowed(toks []Token) bool {
	if len(toks) == 0 {
		return true
	}
	prev := toks[len(toks)-1]
	switch prev.Kind {
	case Punct:
		return strings.IndexByte(regexPreceders, prev.Text[len(prev.Text)-1]) >= 0
	case Ident:
		return regexKeywords[prev.Text]
	}
	return false
}

// charRegexAllowed is the character-level version, for code inside template ${...}.
func charRegexAllowed(src string, i int) bool {
	j := i - 1
	for j >= 0 && strings.IndexByte(" \t\r\n", src[j]) >= 0 {
		j--
	}
	if j < 0 || strings.IndexByte(regexPreceders, src[j]) >= 0 {
		return true
	}
	if isIdentPart(src[j]) {
		k := j
		for k >= 0 && isIdentPart(src[k]) {
			k--
		}
		return regexKeywords[src[k+1:j+1]]
	}
	return false
}

func skipString(src string, i int) int {
	quote, j, n := src[i], i+1, len(src)
	for j < n {
		switch src[j] {
		case '\\':
			j += 2
		case quote:
			return j + 1
		case '\n':
			return j // unterminated: stop at the line end
		default:
			j++
		}
	}
	return n
}

func skipTemplate(src string, i int) int {
	j, n := i+1, len(src)
	for j < n {
		switch {
		case src[j] == '\\':
			j += 2
		case src[j] == '`':
			return j + 1
		case strings.HasPrefix(src[j:], "${"):
			j = SkipBalanced(src, j+1)
		default:
			j++
		}
	}
	return n
}

func skipRegex(src string, i int) int {
	j, n, inClass := i+1, len(src), false
	for j < n {
		c := src[j]
		switch {
		case c == '\\':
			j += 2
			continue
		case c == '\n':
			return i + 1 // not a regex after all
		case inClass:
			inClass = c != ']'
		case c == '[':
			inClass = true
		case c == '/':
			j++
			for j < n && ((src[j] >= 'a' && src[j] <= 'z') || (src[j] >= 'A' && src[j] <= 'Z')) {
				j++
			}
			return j
		}
		j++
	}
	return n
}

// SkipBalanced takes src[i] as an opening bracket and returns the index just past
// its matching closer, skipping strings, templates, regexes and comments.
func SkipBalanced(src string, i int) int {
	closers := map[byte]byte{'(': ')', '[': ']', '{': '}'}
	stack := []byte{closers[src[i]]}
	j, n := i+1, len(src)
	for j < n {
		c := src[j]
		switch {
		case c == '\'' || c == '"':
			j = skipString(src, j)
			continue
		case c == '`':
			j = skipTemplate(src, j)
			continue
		case strings.HasPrefix(src[j:], "//"):
			if k := strings.IndexByte(src[j:], '\n'); k >= 0 {
				j += k
			} else {
				j = n
			}
			continue
		case strings.HasPrefix(src[j:], "/*"):
			if k := strings.Index(src[j+2:], "*/"); k >= 0 {
				j += k + 4
			} else {
				j = n
			}
			continue
		case c == '/' && charRegexAllowed(src, j):
			if end := skipRegex(src, j); end > j+1 {
				j = end
				continue
			}
		case c == '(' || c == '[' || c == '{':
			stack = append(stack, closers[c])
		case c == ')' || c == ']' || c == '}':
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return j + 1
			}
		}
		j++
	}
	return n
}

// MatchClose returns the index of the token closing the bracket at toks[i], or -1.
func MatchClose(toks []Token, i int) int {
	depth := 0
	for k := i; k < len(toks); k++ {
		if toks[k].Kind != Punct {
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

// SplitTop splits on a punctuator that isn't nested in brackets; empty parts are dropped.
func SplitTop(toks []Token, sep string) [][]Token {
	var parts [][]Token
	depth, start := 0, 0
	for k, t := range toks {
		if t.Kind != Punct {
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

// ExprEnd finds where the expression starting at toks[start] ends: at a top-level
// ; or , or closing bracket, or at a line break that JavaScript would treat as the
// end of the statement.
func ExprEnd(toks []Token, start int) int {
	depth := 0
	for k := start; k < len(toks); k++ {
		t := toks[k]
		if t.Kind == Punct {
			switch t.Text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth == 0 {
					return k
				}
				depth--
			case ";", ",":
				if depth == 0 {
					return k
				}
			}
		}
		if depth == 0 && k > start && t.NewlineBefore && !continues(toks[k-1], t) {
			return k
		}
	}
	return len(toks)
}

var continuationStarts = map[string]bool{
	".": true, "?.": true, "?": true, ":": true, "+": true, "-": true, "*": true, "/": true, "%": true,
	"&&": true, "||": true, "??": true, "=>": true, "==": true, "===": true, "!=": true, "!==": true,
	"<": true, ">": true, "<=": true, ">=": true, "|": true, "&": true, "^": true, "**": true,
}

func continues(prev, next Token) bool {
	if prev.Kind == Punct && prev.Text != ")" && prev.Text != "]" && prev.Text != "}" &&
		prev.Text != "++" && prev.Text != "--" {
		return true
	}
	if prev.Kind == Ident && regexKeywords[prev.Text] {
		return true
	}
	return next.Kind == Punct && continuationStarts[next.Text]
}
