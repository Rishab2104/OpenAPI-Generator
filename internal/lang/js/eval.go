package js

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Unescape interprets JavaScript string escapes. Like JS, an unnecessary escape
// drops the backslash: '\d' is "d" (which is why Express docs write '\\d+').
func Unescape(body string) string {
	if !strings.Contains(body, "\\") {
		return body
	}
	var b strings.Builder
	for i := 0; i < len(body); {
		if body[i] != '\\' || i+1 >= len(body) {
			b.WriteByte(body[i])
			i++
			continue
		}
		c := body[i+1]
		i += 2
		switch c {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '0':
			b.WriteByte(0)
		case '\n': // line continuation
		case '\r':
			if i < len(body) && body[i] == '\n' {
				i++
			}
		case 'x':
			if i+2 <= len(body) {
				if v, err := strconv.ParseUint(body[i:i+2], 16, 8); err == nil {
					b.WriteRune(rune(v))
					i += 2
					continue
				}
			}
			b.WriteByte('x')
		case 'u':
			if i < len(body) && body[i] == '{' {
				if end := strings.IndexByte(body[i:], '}'); end > 0 {
					if v, err := strconv.ParseUint(body[i+1:i+end], 16, 32); err == nil {
						b.WriteRune(rune(v))
						i += end + 1
						continue
					}
				}
			} else if i+4 <= len(body) {
				if v, err := strconv.ParseUint(body[i:i+4], 16, 32); err == nil {
					b.WriteRune(rune(v))
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		default:
			r, size := utf8.DecodeRuneInString(body[i-1:])
			b.WriteRune(r)
			i += size - 1
		}
	}
	return b.String()
}

// Constants maps names to statically known string values.
type Constants map[string]string

// Static evaluates a string expression: 'a', `b${C}`, A + '/x', ('x'), or a known constant.
func Static(toks []Token, consts Constants) (string, bool) {
	toks = trimTypeAssertion(toks)
	if len(toks) == 0 {
		return "", false
	}
	if toks[0].IsPunct("(") && MatchClose(toks, 0) == len(toks)-1 {
		return Static(toks[1:len(toks)-1], consts)
	}
	if parts := SplitTop(toks, "+"); len(parts) > 1 {
		var b strings.Builder
		for _, part := range parts {
			v, ok := Static(part, consts)
			if !ok {
				return "", false
			}
			b.WriteString(v)
		}
		return b.String(), true
	}
	if len(toks) != 1 {
		return "", false
	}
	t := toks[0]
	switch t.Kind {
	case String:
		if len(t.Text) >= 2 && t.Text[len(t.Text)-1] == t.Text[0] {
			return Unescape(t.Text[1 : len(t.Text)-1]), true
		}
	case Template:
		return evalTemplate(t.Text, consts)
	case Ident:
		v, ok := consts[t.Text]
		return v, ok
	}
	return "", false
}

// StaticText is Static on source text.
func StaticText(expr string, consts Constants) (string, bool) { return Static(Tokenize(expr), consts) }

// trimTypeAssertion drops a trailing TypeScript `as T` / `satisfies T`.
func trimTypeAssertion(toks []Token) []Token {
	depth := 0
	for k, t := range toks {
		switch {
		case t.Kind == Punct && strings.Contains("([{", t.Text):
			depth++
		case t.Kind == Punct && strings.Contains(")]}", t.Text):
			depth--
		case depth == 0 && k > 0 && (t.IsIdent("as") || t.IsIdent("satisfies")):
			return toks[:k]
		}
	}
	return toks
}

func evalTemplate(raw string, consts Constants) (string, bool) {
	if len(raw) < 2 || raw[len(raw)-1] != '`' {
		return "", false
	}
	body := raw[1 : len(raw)-1]
	var b strings.Builder
	pos := 0
	for j := 0; j < len(body); j++ {
		if body[j] == '\\' {
			j++
			continue
		}
		if !strings.HasPrefix(body[j:], "${") {
			continue
		}
		end := SkipBalanced(body, j+1) // just past the closing }
		if end > len(body) || body[end-1] != '}' {
			return "", false
		}
		v, ok := StaticText(body[j+2:end-1], consts)
		if !ok {
			return "", false
		}
		b.WriteString(Unescape(body[pos:j]))
		b.WriteString(v)
		pos, j = end, end-1
	}
	b.WriteString(Unescape(body[pos:]))
	return b.String(), true
}

// PathValues is the path(s) a route argument stands for: arrays give several.
func PathValues(toks []Token, consts Constants) ([]string, bool) {
	if len(toks) >= 2 && toks[0].IsPunct("[") && MatchClose(toks, 0) == len(toks)-1 {
		items := SplitTop(toks[1:len(toks)-1], ",")
		if len(items) == 0 {
			return nil, false
		}
		var out []string
		for _, item := range items {
			v, ok := Static(item, consts)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return out, true
	}
	v, ok := Static(toks, consts)
	if !ok {
		return nil, false
	}
	return []string{v}, true
}

// Dotted returns "a.b.c" if toks are exactly an identifier chain.
func Dotted(toks []Token) (string, bool) {
	if len(toks) == 0 || len(toks)%2 == 0 {
		return "", false
	}
	parts := make([]string, 0, len(toks)/2+1)
	for k, t := range toks {
		if k%2 == 0 {
			if t.Kind != Ident {
				return "", false
			}
			parts = append(parts, t.Text)
		} else if !t.IsPunct(".") {
			return "", false
		}
	}
	return strings.Join(parts, "."), true
}

// RequireCall recognises require('x') and require('x').attr.
func RequireCall(toks []Token) (spec, attr string, ok bool) {
	if len(toks) < 4 || !toks[0].IsIdent("require") || !toks[1].IsPunct("(") || toks[2].Kind != String || !toks[3].IsPunct(")") {
		return "", "", false
	}
	spec, ok = Static(toks[2:3], nil)
	switch {
	case !ok:
		return "", "", false
	case len(toks) == 4:
		return spec, "", true
	case len(toks) == 6 && toks[4].IsPunct(".") && toks[5].Kind == Ident:
		return spec, toks[5].Text, true
	}
	return "", "", false
}

// IsFunction reports whether an argument is a function literal (a route handler).
func IsFunction(toks []Token) bool {
	if len(toks) == 0 {
		return false
	}
	if toks[0].IsIdent("async") && len(toks) > 1 {
		toks = toks[1:]
	}
	switch {
	case toks[0].IsIdent("function"):
		return true
	case toks[0].Kind == Ident && len(toks) > 1 && toks[1].IsPunct("=>"):
		return true
	case toks[0].IsPunct("("):
		close := MatchClose(toks, 0)
		for k := close + 1; close > 0 && k < len(toks); k++ { // skip a TS return type
			if toks[k].IsPunct("=>") {
				return true
			}
			if toks[k].IsPunct("{") {
				return false
			}
		}
	}
	return false
}

// HandlerName gives a readable name for a handler argument: getUser, users.list, or <inline>.
func HandlerName(toks []Token) string {
	if name, ok := Dotted(toks); ok {
		return name
	}
	k := 0
	if len(toks) > 0 && toks[0].IsIdent("async") {
		k++
	}
	if k < len(toks) && toks[k].IsIdent("function") {
		for k++; k < len(toks) && toks[k].IsPunct("*"); k++ {
		}
		if k < len(toks) && toks[k].Kind == Ident {
			return toks[k].Text
		}
	}
	// wrapper(handler), e.g. asyncHandler(getUser)
	for k := 1; k < len(toks); k++ {
		if toks[k].IsPunct("(") {
			if _, ok := Dotted(toks[:k]); ok && MatchClose(toks, k) == len(toks)-1 {
				if args := SplitTop(toks[k+1:len(toks)-1], ","); len(args) > 0 {
					return HandlerName(args[len(args)-1])
				}
			}
			break
		}
	}
	return "<inline>"
}

// Source returns the original text a token range was read from.
func Source(src string, toks []Token) string {
	if len(toks) == 0 {
		return ""
	}
	return src[toks[0].Pos:toks[len(toks)-1].End()]
}

var resolveExtensions = []string{".js", ".ts", ".mjs", ".cjs", ".jsx", ".tsx", ".mts", ".cts"}

// ResolveModule maps a relative require/import specifier to a project file, roughly as Node does.
// contains reports whether a cleaned absolute path is a file of the project.
func ResolveModule(contains func(string) bool, fromFile, spec string) (string, bool) {
	if !strings.HasPrefix(spec, ".") {
		return "", false // a package from node_modules, not project code
	}
	base := filepath.Clean(filepath.Join(filepath.Dir(fromFile), filepath.FromSlash(spec)))
	candidates := []string{base}
	for _, ext := range resolveExtensions {
		candidates = append(candidates, base+ext)
	}
	for _, ext := range resolveExtensions {
		candidates = append(candidates, filepath.Join(base, "index"+ext))
	}
	switch filepath.Ext(base) {
	case ".js", ".mjs", ".cjs", ".jsx": // TypeScript ESM imports "./users.js" for users.ts
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		for _, ext := range []string{".ts", ".tsx", ".mts", ".cts"} {
			candidates = append(candidates, stem+ext)
		}
	}
	for _, c := range candidates {
		if contains(c) {
			return c, true
		}
	}
	return "", false
}
