package js

import (
	"reflect"
	"testing"
)

func kinds(toks []Token) []Kind {
	out := make([]Kind, len(toks))
	for i, t := range toks {
		out[i] = t.Kind
	}
	return out
}

func TestCommentsAndLiteralsAreExact(t *testing.T) {
	src := "a('x // not a comment'); // gone\n/* app.get('/gone') */ b(/re\\/x/g, `t ${c} }`)"
	toks := Tokenize(src)
	var texts []string
	for _, tok := range toks {
		texts = append(texts, tok.Text)
	}
	want := []string{"a", "(", "'x // not a comment'", ")", ";", "b", "(", `/re\/x/g`, ",", "`t ${c} }`", ")"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("tokens = %q", texts)
	}
	if toks[5].Line != 2 || !toks[5].NewlineBefore {
		t.Errorf("b: line %d newline %v", toks[5].Line, toks[5].NewlineBefore)
	}
}

func TestRegexVersusDivision(t *testing.T) {
	if got := kinds(Tokenize("x = a / b / c")); got[3] != Punct {
		t.Errorf("division lexed as %v", got)
	}
	if got := kinds(Tokenize("return /ab+c/i.test(s)")); got[1] != Regex {
		t.Errorf("regex lexed as %v", got)
	}
}

func TestStatic(t *testing.T) {
	consts := Constants{"P": "/api"}
	cases := []struct {
		expr string
		want string
		ok   bool
	}{
		{`'/a'`, "/a", true},
		{"`${P}/x`", "/api/x", true},
		{`P + '/x'`, "/api/x", true},
		{`('/x')`, "/x", true},
		{`'/x' as const`, "/x", true},
		{`UNKNOWN`, "", false},
		{"`${obj.prop}/x`", "", false},
		{`'/:id(\\d+)'`, `/:id(\d+)`, true},
		{`'/:id(\d+)'`, "/:id(d+)", true}, // JS drops the useless escape: '\d' === 'd'
		{`'café'`, "café", true},
	}
	for _, c := range cases {
		got, ok := StaticText(c.expr, consts)
		if got != c.want || ok != c.ok {
			t.Errorf("StaticText(%s) = %q, %v; want %q, %v", c.expr, got, ok, c.want, c.ok)
		}
	}
}

func TestHandlerNameAndIsFunction(t *testing.T) {
	cases := map[string]string{
		"getUser":                    "getUser",
		"users . list":               "users.list",
		"async function named() {}":  "named",
		"asyncHandler(ctrl.getUser)": "ctrl.getUser",
		"(req, res) => res.json({})": "<inline>",
	}
	for expr, want := range cases {
		if got := HandlerName(Tokenize(expr)); got != want {
			t.Errorf("HandlerName(%s) = %q, want %q", expr, got, want)
		}
	}
	for _, fn := range []string{"(req, res) => {}", "async (req: Request): Promise<void> => {}", "req => 1", "function () {}"} {
		if !IsFunction(Tokenize(fn)) {
			t.Errorf("IsFunction(%s) = false", fn)
		}
	}
	for _, notFn := range []string{"data", "{ params: { page: 1 } }", "auth"} {
		if IsFunction(Tokenize(notFn)) {
			t.Errorf("IsFunction(%s) = true", notFn)
		}
	}
}
