package python

import (
	"testing"
)

func TestStringsCommentsAndLines(t *testing.T) {
	src := "x = 1  # c = 2\n" +
		"@bp.route(\n    '/a',\n    methods=['GET'],  # trailing\n)\n" +
		"def f():\n    s = '''multi\nline # not a comment'''\n    return s\n"
	lines := Lines(Tokenize(src))
	if len(lines) != 5 {
		for _, l := range lines {
			t.Log(l)
		}
		t.Fatalf("got %d logical lines, want 5", len(lines))
	}
	if lines[1][0].Text != "@" || lines[1][0].Line != 2 || lines[2][0].Text != "def" || lines[2][0].Line != 6 {
		t.Errorf("decorator/def lines wrong: %v / %v", lines[1][0], lines[2][0])
	}
	if lines[3][0].Indent != 4 || lines[3][2].Kind != String || lines[4][0].Line != 9 {
		t.Errorf("body: %+v / %+v", lines[3], lines[4][0])
	}
}

func TestStatic(t *testing.T) {
	consts := map[string]string{"API": "/api"}
	lookup := func(n string) (string, bool) { v, ok := consts[n]; return v, ok }
	cases := []struct {
		expr string
		want string
		ok   bool
	}{
		{`"/a"`, "/a", true},
		{`f"{API}/users"`, "/api/users", true},
		{`F'{API}' "/x"`, "/api/x", true}, // f-string + implicit concatenation
		{`API + "/x"`, "/api/x", true},
		{`r"\d+"`, `\d+`, true},
		{`"\d+"`, `\d+`, true}, // Python keeps unknown escapes
		{`"tab\there"`, "tab\there", true},
		{`f"{API!r}"`, "", false}, // conversions aren't static
		{`f"{other}/x"`, "", false},
		{`b"/x"`, "", false},       // bytes aren't strings
		{`f"{d["k"]}"`, "", false}, // 3.12 nested quotes: lexed, just not static
	}
	for _, c := range cases {
		got, ok := Static(Tokenize(c.expr), lookup)
		if got != c.want || ok != c.ok {
			t.Errorf("Static(%s) = %q, %v; want %q, %v", c.expr, got, ok, c.want, c.ok)
		}
	}
	if n := len(Tokenize(`f"{d["k"]}" + x`)); n != 3 {
		t.Errorf("nested-quote f-string split into %d tokens, want 3", n)
	}
}
