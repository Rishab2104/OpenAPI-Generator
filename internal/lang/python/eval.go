package python

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Lookup resolves a name to a statically known string.
type Lookup func(name string) (string, bool)

// Static evaluates a string expression: "a", f"{PREFIX}/x", A + "/x", "a" "b", or a known constant.
func Static(toks []Token, lookup Lookup) (string, bool) {
	if len(toks) == 0 {
		return "", false
	}
	if toks[0].IsOp("(") && MatchClose(toks, 0) == len(toks)-1 {
		return Static(toks[1:len(toks)-1], lookup)
	}
	if parts := SplitTop(toks, "+"); len(parts) > 1 {
		var b strings.Builder
		for _, part := range parts {
			v, ok := Static(part, lookup)
			if !ok {
				return "", false
			}
			b.WriteString(v)
		}
		return b.String(), true
	}
	if len(toks) == 1 && toks[0].Kind == Name {
		if lookup == nil {
			return "", false
		}
		return lookup(toks[0].Text)
	}
	var b strings.Builder // one string, or adjacent ones: "a" "b"
	for _, t := range toks {
		if t.Kind != String {
			return "", false
		}
		v, ok := StringValue(t, lookup)
		if !ok {
			return "", false
		}
		b.WriteString(v)
	}
	return b.String(), true
}

// StaticList evaluates ["GET", "POST"] / ("GET",) / {"GET"} or a single string.
func StaticList(toks []Token, lookup Lookup) ([]string, bool) {
	if len(toks) >= 2 && (toks[0].IsOp("[") || toks[0].IsOp("(") || toks[0].IsOp("{")) && MatchClose(toks, 0) == len(toks)-1 {
		var out []string
		for _, item := range SplitTop(toks[1:len(toks)-1], ",") {
			v, ok := Static(item, lookup)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return out, true
	}
	v, ok := Static(toks, lookup)
	if !ok {
		return nil, false
	}
	return []string{v}, true
}

// StringValue evaluates one string literal token. Bytes literals aren't strings.
func StringValue(t Token, lookup Lookup) (string, bool) {
	p := strings.IndexAny(t.Text, `'"`)
	if p < 0 {
		return "", false
	}
	prefix := strings.ToLower(t.Text[:p])
	quote := t.Text[p : p+1]
	if strings.HasPrefix(t.Text[p:], strings.Repeat(quote, 3)) && len(t.Text)-p >= 6 {
		quote = strings.Repeat(quote, 3)
	}
	if !strings.HasSuffix(t.Text, quote) || len(t.Text)-p < 2*len(quote) {
		return "", false
	}
	body := t.Text[p+len(quote) : len(t.Text)-len(quote)]
	raw := strings.Contains(prefix, "r")
	switch {
	case strings.Contains(prefix, "b") || strings.Contains(prefix, "t"):
		return "", false
	case strings.Contains(prefix, "f"):
		return fstring(body, raw, lookup)
	case raw:
		return body, true
	}
	return Unescape(body), true
}

func fstring(body string, raw bool, lookup Lookup) (string, bool) {
	var b strings.Builder
	literal := func(s string) {
		if !raw {
			s = Unescape(s)
		}
		b.WriteString(s)
	}
	pos := 0
	for j := 0; j < len(body); j++ {
		switch {
		case strings.HasPrefix(body[j:], "{{") || strings.HasPrefix(body[j:], "}}"):
			literal(body[pos:j])
			b.WriteByte(body[j])
			j++
			pos = j + 1
		case body[j] == '{':
			end := skipField(body, j) // just past the closing }
			if end > len(body) || body[end-1] != '}' {
				return "", false
			}
			expr := body[j+1 : end-1]
			if strings.ContainsAny(expr, "!:=") { // conversions, format specs, debug "=": not static
				return "", false
			}
			v, ok := Static(Tokenize(strings.TrimSpace(expr)), lookup)
			if !ok {
				return "", false
			}
			literal(body[pos:j])
			b.WriteString(v)
			pos, j = end, end-1
		}
	}
	literal(body[pos:])
	return b.String(), true
}

// Unescape interprets Python string escapes. Unknown escapes keep their backslash ("\d" stays "\d").
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
		simple := map[byte]string{'\\': "\\", '\'': "'", '"': "\"", 'a': "\a", 'b': "\b", 'f': "\f",
			'n': "\n", 'r': "\r", 't': "\t", 'v': "\v", '\n': ""}
		if s, ok := simple[c]; ok {
			b.WriteString(s)
			continue
		}
		width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[c]
		if width > 0 && i+width <= len(body) {
			if v, err := strconv.ParseUint(body[i:i+width], 16, 32); err == nil && utf8.ValidRune(rune(v)) {
				b.WriteRune(rune(v))
				i += width
				continue
			}
		}
		if c >= '0' && c <= '7' {
			end := i - 1
			for end < len(body) && end < i+2 && body[end] >= '0' && body[end] <= '7' {
				end++
			}
			v, _ := strconv.ParseUint(body[i-1:end], 8, 32)
			b.WriteRune(rune(v))
			i = end
			continue
		}
		b.WriteByte('\\')
		b.WriteByte(c)
	}
	return b.String()
}
