package rules

// Express.js: routes, routers, and the app.use() mounts that give them their prefixes.
//
// Each file is reduced to jsFacts: which names are apps/routers, what it imports
// and exports, its route registrations and its mounts. A resolver then follows
// imports and mounts across files, so router.get('/:id') in routes/users.js comes
// out as GET /api/users/{id} when server.js does app.use('/api', ...) and
// routes/index.js does router.use('/users', ...).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
	"github.com/Rishab2104/OpenAPI-Generator/internal/lang/js"
)

func init() { Register(expressRule{}) }

var (
	jsExtensions = []string{".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx"}
	routeMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true,
		"delete": true, "options": true, "head": true, "all": true}
	// Names that mean "an app or router" by convention. Used when the object comes in
	// as a function argument (module.exports = (app) => {...}) so its creation isn't visible.
	routerish     = regexp.MustCompile(`^(?:app|application|server|router|routes?)$|^[a-z][\w$]*(?:Router|Routes|App)$`)
	expressImport = regexp.MustCompile(`require\(\s*['"]express['"]\s*\)|from\s+['"]express['"]`)
	catchAll      = regexp.MustCompile(`^/?(?:\*\w*|\(\.\*\)|\{/?\*\w+\})$`)
)

const unmountedNote = "router is never mounted in the scanned code; path shown without its mount prefix"

type importRef struct {
	spec   string
	export string // "" = the default export / whole module
}

type mountTarget struct {
	kind string // "name" (users), "member" (routes.users), "require" (require('./users'))
	name string
	attr string
	spec string
}

type rawRoute struct {
	receiver string
	method   string
	paths    []string
	line     int
	handler  string
}

type mount struct {
	receiver string
	prefixes []string
	targets  []mountTarget
	notes    []string
}

type jsFacts struct {
	src             string
	expressImported bool
	expressNames    map[string]bool // locals bound to the express module
	routerFactories map[string]bool // locals bound to express.Router
	apps, routers   map[string]bool
	imports         map[string]importRef
	defaultExport   string
	namedExports    map[string]string // exported name -> local
	consts          js.Constants
	routes          []rawRoute
	mounts          []mount
	skipped         []string
}

func newJSFacts(src string) *jsFacts {
	return &jsFacts{src: src, expressNames: map[string]bool{}, routerFactories: map[string]bool{},
		apps: map[string]bool{}, routers: map[string]bool{}, imports: map[string]importRef{},
		namedExports: map[string]string{}, consts: js.Constants{}}
}

func (f *jsFacts) addImport(local, spec, export string) {
	f.imports[local] = importRef{spec, export}
	if spec == "express" {
		f.expressImported = true
		switch export {
		case "":
			f.expressNames[local] = true
		case "Router":
			f.routerFactories[local] = true
		}
	}
}

func (f *jsFacts) export(exported, local, spec string) {
	if spec != "" { // export { x as y } from './z': route it through a synthetic import
		synthetic := "<re-export " + exported + ">"
		ref := importRef{spec: spec, export: local}
		if local == "default" {
			ref.export = ""
		}
		f.imports[synthetic] = ref
		local = synthetic
	}
	if exported == "default" {
		f.defaultExport = local
	} else {
		f.namedExports[exported] = local
	}
}

// classify records local as an app or router if callee is express() / express.Router().
func (f *jsFacts) classify(local, callee string) {
	head, tail := "", callee
	if k := strings.LastIndex(callee, "."); k >= 0 {
		head, tail = callee[:k], callee[k+1:]
	}
	switch {
	case callee == "require('express')" || f.expressNames[callee] || callee == "express":
		f.apps[local] = true
	case callee == "require('express').Router" || f.routerFactories[callee] || callee == "Router" ||
		(tail == "Router" && (f.expressNames[head] || head == "express")):
		f.routers[local] = true
	}
}

// accepts decides whether receiver.get(path, handler) is a route rather than axios.get / cache.get.
func (f *jsFacts) accepts(receiver string, handlerIsFunction bool) bool {
	if f.apps[receiver] || f.routers[receiver] {
		return true
	}
	if ref, ok := f.imports[receiver]; ok && strings.HasPrefix(ref.spec, ".") {
		return true // a router from another project file
	}
	last := receiver[strings.LastIndex(receiver, ".")+1:]
	if routerish.MatchString(last) {
		return true
	}
	return handlerIsFunction && f.expressImported
}

func (f *jsFacts) addRoute(receiver, method string, path []js.Token, line int, handler string, chained, handlerIsFunction bool) {
	if !chained && !f.accepts(receiver, handlerIsFunction) {
		return
	}
	label := strings.ToUpper(method)
	if len(path) == 1 && path[0].Kind == js.Regex {
		f.skipped = append(f.skipped, fmt.Sprintf("%d: %s route with a regular-expression path was skipped", line, label))
		return
	}
	values, ok := js.PathValues(path, f.consts)
	if !ok {
		f.skipped = append(f.skipped, fmt.Sprintf("%d: %s route path %s could not be resolved statically",
			line, label, js.Source(f.src, path)))
		return
	}
	for _, v := range values {
		if !strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "*") {
			return // app.get('env') is a settings getter, not a route
		}
	}
	f.routes = append(f.routes, rawRoute{receiver, method, values, line, handler})
}

func (f *jsFacts) addMount(receiver string, args [][]js.Token) {
	if len(args) == 0 {
		return
	}
	first, rest := args[0], args
	prefixes, notes := []string{""}, []string(nil)
	_, static := js.Static(first, f.consts)
	if first[0].Kind == js.String || first[0].Kind == js.Template || first[0].IsPunct("[") || static {
		rest = args[1:]
		if values, ok := js.PathValues(first, f.consts); ok {
			prefixes = values
		} else {
			notes = append(notes, fmt.Sprintf("mount prefix %s could not be resolved; the real path has an extra prefix",
				js.Source(f.src, first)))
		}
	}
	var targets []mountTarget
	for _, arg := range rest {
		if t, ok := targetOf(arg); ok {
			targets = append(targets, t)
		}
	}
	if len(targets) > 0 {
		f.mounts = append(f.mounts, mount{receiver, prefixes, targets, notes})
	}
}

func targetOf(toks []js.Token) (mountTarget, bool) {
	if len(toks) == 1 && toks[0].Kind == js.Ident {
		return mountTarget{kind: "name", name: toks[0].Text}, true
	}
	if spec, attr, ok := js.RequireCall(toks); ok {
		return mountTarget{kind: "require", spec: spec, attr: attr}, true
	}
	if len(toks) == 3 && toks[0].Kind == js.Ident && toks[1].IsPunct(".") && toks[2].Kind == js.Ident {
		return mountTarget{kind: "member", name: toks[0].Text, attr: toks[2].Text}, true
	}
	return mountTarget{}, false
}

// ---------------------------------------------------------------- fact extraction

type declarator struct {
	name    string     // set for `const x = ...`
	pattern []js.Token // set for `const { a, b: c } = ...`
	value   []js.Token
}

func chainStart(toks []js.Token, i int) bool {
	return i == 0 || !(toks[i-1].IsPunct(".") || toks[i-1].IsPunct("?."))
}

func extractJS(src string) *jsFacts {
	f := newJSFacts(src)
	toks := js.Tokenize(src)
	var decls []declarator
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.Kind != js.Ident || !chainStart(toks, i) {
			continue
		}
		switch t.Text {
		case "import":
			parseImport(f, toks, i)
		case "const", "let", "var":
			decls = append(decls, parseDeclarators(toks, i+1)...)
		case "export":
			parseExport(f, toks, i)
		case "module", "exports":
			parseModuleExports(f, toks, i)
		}
	}
	// requires first: later declarations may call express() or Router() from them
	for _, d := range decls {
		if d.pattern != nil {
			if spec, _, ok := js.RequireCall(d.value); ok {
				for _, part := range js.SplitTop(d.pattern, ",") {
					key, local := part[0].Text, part[0].Text
					if len(part) >= 3 && part[1].IsPunct(":") && part[2].Kind == js.Ident {
						local = part[2].Text
					}
					if part[0].Kind == js.Ident {
						f.addImport(local, spec, key)
					}
				}
			}
		} else if spec, attr, ok := js.RequireCall(d.value); ok {
			f.addImport(d.name, spec, attr)
		}
	}
	for _, d := range decls {
		if d.pattern != nil || len(d.value) == 0 {
			continue
		}
		if _, _, ok := js.RequireCall(d.value); ok {
			continue
		}
		value := d.value
		if value[0].IsIdent("await") || value[0].IsIdent("new") {
			value = value[1:]
		}
		if callee, n := calleeOf(value); n > 0 && n < len(value) && value[n].IsPunct("(") {
			f.classify(d.name, callee)
		} else if v, ok := js.Static(d.value, f.consts); ok {
			f.consts[d.name] = v
		}
	}
	for i := 0; i < len(toks); i++ {
		if toks[i].Kind == js.Ident && chainStart(toks, i) {
			parseCall(f, toks, i)
		}
	}
	return f
}

// calleeOf reads `require('x').Router` or `a.b.c` at the start of toks; n is its token length.
func calleeOf(toks []js.Token) (string, int) {
	if len(toks) >= 4 && toks[0].IsIdent("require") && toks[1].IsPunct("(") && toks[2].Kind == js.String && toks[3].IsPunct(")") {
		spec, _ := js.Static(toks[2:3], nil)
		callee := "require('" + spec + "')"
		if len(toks) >= 6 && toks[4].IsPunct(".") && toks[5].Kind == js.Ident {
			return callee + "." + toks[5].Text, 6
		}
		return callee, 4
	}
	n := 0
	for n < len(toks) && toks[n].Kind == js.Ident {
		if n+2 < len(toks) && toks[n+1].IsPunct(".") && toks[n+2].Kind == js.Ident {
			n += 2
			continue
		}
		n++
		break
	}
	if n == 0 {
		return "", 0
	}
	name, _ := js.Dotted(toks[:n])
	return name, n
}

func parseDeclarators(toks []js.Token, i int) []declarator {
	var out []declarator
	for i < len(toks) {
		var d declarator
		switch {
		case toks[i].Kind == js.Ident:
			d.name = toks[i].Text
			i++
		case toks[i].IsPunct("{"):
			close := js.MatchClose(toks, i)
			if close < 0 {
				return out
			}
			d.pattern = toks[i+1 : close]
			i = close + 1
		default:
			return out
		}
		for i < len(toks) && !toks[i].IsPunct("=") && !toks[i].IsPunct(",") && !toks[i].IsPunct(";") {
			if toks[i].IsPunct("(") || toks[i].IsPunct("{") || toks[i].IsPunct("[") { // inside a TS type
				if c := js.MatchClose(toks, i); c > 0 {
					i = c
				}
			}
			if toks[i].NewlineBefore && !toks[i-1].IsPunct(":") && toks[i].Kind == js.Ident {
				break
			}
			i++
		}
		if i < len(toks) && toks[i].IsPunct("=") {
			end := js.ExprEnd(toks, i+1)
			d.value = toks[i+1 : end]
			i = end
		}
		out = append(out, d)
		if i >= len(toks) || !toks[i].IsPunct(",") {
			return out
		}
		i++
	}
	return out
}

func parseImport(f *jsFacts, toks []js.Token, i int) {
	j := i + 1
	if j >= len(toks) || toks[j].IsPunct("(") || toks[j].IsPunct(".") || toks[j].Kind == js.String {
		return // dynamic import(), import.meta, or a side-effect import
	}
	if toks[j].IsIdent("type") && j+1 < len(toks) && !toks[j+1].IsIdent("from") && !toks[j+1].IsPunct(",") {
		return // import type { ... }
	}
	if toks[j].Kind == js.Ident && j+1 < len(toks) && toks[j+1].IsPunct("=") { // TS: import x = require('y')
		end := js.ExprEnd(toks, j+2)
		if spec, attr, ok := js.RequireCall(toks[j+2 : end]); ok {
			f.addImport(toks[j].Text, spec, attr)
		}
		return
	}
	k := j
	for k < len(toks) && !(toks[k].IsIdent("from") && k+1 < len(toks) && toks[k+1].Kind == js.String) {
		if toks[k].IsPunct(";") {
			return
		}
		if toks[k].IsPunct("{") {
			if c := js.MatchClose(toks, k); c > 0 {
				k = c
			}
		}
		k++
	}
	if k >= len(toks) {
		return
	}
	spec, _ := js.Static(toks[k+1:k+2], nil)
	clause := toks[j:k]
	for p := 0; p < len(clause); p++ {
		t := clause[p]
		switch {
		case t.Kind == js.Ident:
			f.addImport(t.Text, spec, "")
		case t.IsPunct("*") && p+2 < len(clause) && clause[p+1].IsIdent("as"):
			f.addImport(clause[p+2].Text, spec, "")
			p += 2
		case t.IsPunct("{"):
			close := js.MatchClose(clause, p)
			if close < 0 {
				return
			}
			for _, part := range js.SplitTop(clause[p+1:close], ",") {
				if len(part) > 1 && part[0].IsIdent("type") {
					part = part[1:]
				}
				name, _ := js.Static(part[:1], nil)
				if part[0].Kind == js.Ident {
					name = part[0].Text
				}
				local := name
				if len(part) >= 3 && part[1].IsIdent("as") {
					local = part[2].Text
				}
				export := name
				if name == "default" {
					export = ""
				}
				f.addImport(local, spec, export)
			}
			p = close
		}
	}
}

func parseExport(f *jsFacts, toks []js.Token, i int) {
	j := i + 1
	if j >= len(toks) {
		return
	}
	isDefault := toks[j].IsIdent("default")
	if isDefault {
		j++
	}
	k := j
	for k < len(toks) && (toks[k].IsIdent("async") || toks[k].IsIdent("abstract")) {
		k++
	}
	switch {
	case k < len(toks) && (toks[k].IsIdent("function") || toks[k].IsIdent("class")):
		for k++; k < len(toks) && toks[k].IsPunct("*"); k++ {
		}
		if k < len(toks) && toks[k].Kind == js.Ident && !toks[k].IsIdent("extends") {
			f.namedExports[toks[k].Text] = toks[k].Text
			if isDefault {
				f.defaultExport = toks[k].Text
			}
		}
	case isDefault:
		if j < len(toks) && toks[j].Kind == js.Ident &&
			(j+1 == len(toks) || toks[j+1].IsPunct(";") || toks[j+1].NewlineBefore) {
			f.defaultExport = toks[j].Text
		}
	case toks[j].IsIdent("const") || toks[j].IsIdent("let") || toks[j].IsIdent("var"):
		for _, d := range parseDeclarators(toks, j+1) {
			if d.name != "" {
				f.namedExports[d.name] = d.name
			}
		}
	case toks[j].IsPunct("{"):
		close := js.MatchClose(toks, j)
		if close < 0 {
			return
		}
		spec := ""
		if close+2 < len(toks) && toks[close+1].IsIdent("from") && toks[close+2].Kind == js.String {
			spec, _ = js.Static(toks[close+2:close+3], nil)
		}
		for _, part := range js.SplitTop(toks[j+1:close], ",") {
			name := part[0].Text
			exported := name
			if len(part) >= 3 && part[1].IsIdent("as") {
				exported = part[2].Text
			}
			f.export(exported, name, spec)
		}
	}
}

func parseModuleExports(f *jsFacts, toks []js.Token, i int) {
	k := i
	if toks[i].IsIdent("module") {
		if !(k+2 < len(toks) && toks[k+1].IsPunct(".") && toks[k+2].IsIdent("exports")) {
			return
		}
		k += 2
	}
	member := ""
	if k+2 < len(toks) && toks[k+1].IsPunct(".") && toks[k+2].Kind == js.Ident {
		member, k = toks[k+2].Text, k+2
	}
	if member == "" && toks[i].IsIdent("exports") {
		return // bare `exports = ...` rebinds a local, exports nothing
	}
	if k+1 >= len(toks) || !toks[k+1].IsPunct("=") {
		return
	}
	value := toks[k+2 : js.ExprEnd(toks, k+2)]
	if len(value) == 0 {
		return
	}
	spec, _, isRequire := js.RequireCall(value)
	switch {
	case member != "":
		if len(value) == 1 && value[0].Kind == js.Ident {
			f.namedExports[member] = value[0].Text
		} else if isRequire {
			f.export(member, "default", spec)
		}
	case len(value) == 1 && value[0].Kind == js.Ident:
		f.defaultExport = value[0].Text
	case value[0].IsPunct("{") && js.MatchClose(value, 0) == len(value)-1:
		for _, part := range js.SplitTop(value[1:len(value)-1], ",") {
			switch {
			case len(part) == 1 && part[0].Kind == js.Ident:
				f.namedExports[part[0].Text] = part[0].Text
			case len(part) == 3 && part[1].IsPunct(":") && part[2].Kind == js.Ident:
				key, ok := js.Static(part[:1], nil)
				if !ok {
					key = part[0].Text
				}
				f.namedExports[key] = part[2].Text
			}
		}
	case isRequire: // module.exports = require('./routes')
		f.export("default", "default", spec)
	}
}

func parseCall(f *jsFacts, toks []js.Token, i int) {
	parts := []string{toks[i].Text}
	j := i + 1
	for j+1 < len(toks) && toks[j].IsPunct(".") && toks[j+1].Kind == js.Ident {
		parts = append(parts, toks[j+1].Text)
		j += 2
	}
	if len(parts) < 2 || j >= len(toks) || !toks[j].IsPunct("(") {
		return
	}
	prop := parts[len(parts)-1]
	if !routeMethods[prop] && prop != "use" && prop != "route" {
		return
	}
	receiver := strings.Join(parts[:len(parts)-1], ".")
	close := js.MatchClose(toks, j)
	if close < 0 {
		return
	}
	args := js.SplitTop(toks[j+1:close], ",")
	line := toks[j-1].Line
	switch {
	case prop == "route": // router.route('/x').get(a).post(b)
		for k := close + 1; len(args) > 0 && k+2 < len(toks) && toks[k].IsPunct(".") &&
			routeMethods[toks[k+1].Text] && toks[k+1].Kind == js.Ident && toks[k+2].IsPunct("("); {
			chainClose := js.MatchClose(toks, k+2)
			if chainClose < 0 {
				return
			}
			if chainArgs := js.SplitTop(toks[k+3:chainClose], ","); len(chainArgs) > 0 {
				f.addRoute(receiver, toks[k+1].Text, args[0], toks[k+1].Line,
					js.HandlerName(chainArgs[len(chainArgs)-1]), true, false)
			}
			k = chainClose + 1
		}
	case prop == "use":
		f.addMount(receiver, args)
	case len(args) >= 2:
		handler := args[len(args)-1]
		f.addRoute(receiver, prop, args[0], line, js.HandlerName(handler), false, js.IsFunction(handler))
	}
}

// ---------------------------------------------------------------- path syntax

var (
	expressParam       = regexp.MustCompile(`:([A-Za-z_$][\w$]*)(\((?:\\.|[^()\\])*\))?`)
	optionalParam      = regexp.MustCompile(`/?:[A-Za-z_$][\w$]*(?:\((?:\\.|[^()\\])*\))?\?`)
	optionalGroup      = regexp.MustCompile(`\{([^{}]*)\}`)
	integerConstraint  = regexp.MustCompile(`^\((?:\\d|\[0-9\])[+*]\)$`)
	splatParam         = regexp.MustCompile(`\*([A-Za-z_$][\w$]*)?`)
	unsupportedPattern = regexp.MustCompile(`[()?+]`)
)

// expandOptional turns Express optional syntax into concrete variants:
// /a/:id? -> [/a, /a/:id], and Express 5's /a{/:id} likewise.
func expandOptional(path string) []string {
	if loc := optionalGroup.FindStringSubmatchIndex(path); loc != nil {
		without := path[:loc[0]] + path[loc[1]:]
		with := path[:loc[0]] + path[loc[2]:loc[3]] + path[loc[1]:]
		return append(expandOptional(without), expandOptional(with)...)
	}
	if loc := optionalParam.FindStringIndex(path); loc != nil {
		without := path[:loc[0]] + path[loc[1]:]
		with := path[:loc[0]] + path[loc[0]:loc[1]-1] + path[loc[1]:]
		return append(expandOptional(without), expandOptional(with)...)
	}
	return []string{path}
}

type openAPIPath struct {
	template string
	params   []core.PathParam
	notes    []string
}

// expressPathToOpenAPI: /users/:id(\d+) -> /users/{id} with id: integer, one entry per optional variant.
func expressPathToOpenAPI(path string) []openAPIPath {
	var out []openAPIPath
	seen := map[string]bool{}
	for _, variant := range expandOptional(path) {
		var params []core.PathParam
		var notes []string
		template := expressParam.ReplaceAllStringFunc(variant, func(m string) string {
			sub := expressParam.FindStringSubmatch(m)
			p := core.PathParam{Name: sub[1], Type: "string"}
			if integerConstraint.MatchString(sub[2]) {
				p.Type = "integer"
			}
			params = append(params, p)
			return "{" + sub[1] + "}"
		})
		template = splatParam.ReplaceAllStringFunc(template, func(m string) string {
			name := m[1:]
			if name == "" {
				name = "wildcard"
				for n := 2; hasParam(params, name); n++ {
					name = fmt.Sprintf("wildcard%d", n)
				}
			}
			params = append(params, core.PathParam{Name: name, Type: "string"})
			return "{" + name + "}"
		})
		if unsupportedPattern.MatchString(template) {
			notes = append(notes, "path uses Express pattern syntax that OpenAPI can't express; check it by hand")
		}
		template = core.JoinPath(template)
		if !seen[template] {
			seen[template] = true
			out = append(out, openAPIPath{template, params, notes})
		}
	}
	return out
}

func hasParam(params []core.PathParam, name string) bool {
	for _, p := range params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- resolution

type node struct{ file, name string }

type expressGraph struct {
	project *core.Project
	facts   map[string]*jsFacts
}

func (g *expressGraph) module(file, spec string) (string, bool) {
	target, ok := js.ResolveModule(g.project.Contains, file, spec)
	if !ok || g.facts[target] == nil {
		return "", false
	}
	return target, true
}

func (g *expressGraph) exported(target, export string) string {
	tf := g.facts[target]
	if export == "" || export == "default" {
		return tf.defaultExport
	}
	return tf.namedExports[export]
}

// canon follows imports until reaching the file that creates name.
func (g *expressGraph) canon(file, name string, depth int) node {
	ref, ok := g.facts[file].imports[name]
	if !ok || depth > 12 {
		return node{file, name}
	}
	if target, ok := g.module(file, ref.spec); ok {
		if local := g.exported(target, ref.export); local != "" {
			return g.canon(target, local, depth+1)
		}
	}
	return node{file, name}
}

func (g *expressGraph) targets(file string, t mountTarget) []node {
	switch t.kind {
	case "name":
		return []node{g.canon(file, t.name, 0)}
	case "require":
		if module, ok := g.module(file, t.spec); ok {
			if local := g.exported(module, t.attr); local != "" {
				return []node{g.canon(module, local, 0)}
			}
		}
		return nil
	}
	if ref, ok := g.facts[file].imports[t.name]; ok && ref.export == "" { // routes.users
		if module, ok := g.module(file, ref.spec); ok {
			if local := g.facts[module].namedExports[t.attr]; local != "" {
				return []node{g.canon(module, local, 0)}
			}
		}
	}
	return []node{{file, t.name + "." + t.attr}}
}

type edge struct {
	prefix string
	child  node
	notes  []string
}

type attachedRoute struct {
	file string
	raw  rawRoute
}

func (g *expressGraph) routes() ([]core.Route, []string) {
	var warnings []string
	attached := map[node][]attachedRoute{}
	for _, file := range sortedKeys(g.facts) {
		for _, raw := range g.facts[file].routes {
			n := g.canon(file, raw.receiver, 0)
			attached[n] = append(attached[n], attachedRoute{file, raw})
		}
	}
	edges := map[node][]edge{}
	mounted := map[node]bool{}
	for _, file := range sortedKeys(g.facts) {
		for _, m := range g.facts[file].mounts {
			parent := g.canon(file, m.receiver, 0)
			for _, t := range m.targets {
				for _, child := range g.targets(file, t) {
					if child == parent {
						continue
					}
					mounted[child] = true
					for _, prefix := range m.prefixes {
						edges[parent] = append(edges[parent], edge{prefix, child, m.notes})
					}
				}
			}
		}
	}

	var out []core.Route
	reached := map[node]bool{}
	var walk func(n node, prefix []string, notes []string, trail map[node]bool)
	walk = func(n node, prefix []string, notes []string, trail map[node]bool) {
		reached[n] = true
		for _, a := range attached[n] {
			out = append(out, g.emit(a.file, a.raw, prefix, notes, &warnings)...)
		}
		for _, e := range edges[n] {
			if trail[e.child] {
				warnings = append(warnings, fmt.Sprintf("%s: router '%s' is mounted inside itself; cycle ignored",
					g.project.Rel(e.child.file), e.child.name))
				continue
			}
			trail[e.child] = true
			walk(e.child, append(append([]string(nil), prefix...), e.prefix), append(append([]string(nil), notes...), e.notes...), trail)
			delete(trail, e.child)
		}
	}
	all := map[node]bool{}
	for n := range attached {
		all[n] = true
	}
	for n := range edges {
		all[n] = true
	}
	for _, root := range sortedNodes(all) {
		if !mounted[root] {
			walk(root, nil, g.rootNotes(root, attached, &warnings), map[node]bool{root: true})
		}
	}
	for _, n := range sortedNodes(all) { // only reachable through a cycle
		if len(attached[n]) > 0 && !reached[n] {
			walk(n, nil, []string{unmountedNote}, map[node]bool{n: true})
		}
	}
	return out, warnings
}

func (g *expressGraph) rootNotes(root node, attached map[node][]attachedRoute, warnings *[]string) []string {
	if len(attached[root]) > 0 && (g.facts[root.file].routers[root.name] || strings.Contains(strings.ToLower(root.name), "router")) {
		*warnings = append(*warnings, fmt.Sprintf("%s: router '%s' is never mounted with app.use(); "+
			"its routes are listed without their prefix", g.project.Rel(root.file), root.name))
		return []string{unmountedNote}
	}
	return nil
}

func (g *expressGraph) emit(file string, raw rawRoute, prefix, notes []string, warnings *[]string) []core.Route {
	rel := g.project.Rel(file)
	method := strings.ToUpper(raw.method)
	if raw.method == "all" {
		method = core.AnyMethod
	}
	var routes []core.Route
	for _, path := range raw.paths {
		full := core.JoinPath(append(append([]string(nil), prefix...), path)...)
		if catchAll.MatchString(path) {
			*warnings = append(*warnings, fmt.Sprintf("%s:%d: catch-all %s %s answers any path; "+
				"expect fake fuzz hits there (step 5 catch-all guard)", rel, raw.line, method, full))
			continue
		}
		for _, p := range expressPathToOpenAPI(full) {
			routes = append(routes, core.Route{
				Method: method, Path: p.template, Params: p.params, Framework: "express",
				File: rel, Line: raw.line, Handler: raw.handler, Parser: "tokens", RawPath: full,
				Notes: append(append([]string(nil), notes...), p.notes...),
			})
		}
	}
	return routes
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodes(set map[node]bool) []node {
	nodes := make([]node, 0, len(set))
	for n := range set {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].file != nodes[j].file {
			return nodes[i].file < nodes[j].file
		}
		return nodes[i].name < nodes[j].name
	})
	return nodes
}

// ---------------------------------------------------------------- rule

type expressRule struct{}

func (expressRule) Name() string     { return "express" }
func (expressRule) Language() string { return "javascript" }

func (expressRule) Detect(p *core.Project) bool {
	for _, manifest := range p.Files("package.json") {
		if filepath.Base(manifest) != "package.json" {
			continue
		}
		var pkg struct {
			Dependencies    map[string]any `json:"dependencies"`
			DevDependencies map[string]any `json:"devDependencies"`
		}
		if json.Unmarshal([]byte(p.Read(manifest)), &pkg) == nil {
			if _, ok := pkg.Dependencies["express"]; ok {
				return true
			}
			if _, ok := pkg.DevDependencies["express"]; ok {
				return true
			}
		}
	}
	for _, f := range p.Files(jsExtensions...) {
		if expressImport.MatchString(p.Read(f)) {
			return true
		}
	}
	return false
}

func (expressRule) Scan(p *core.Project) core.ScanResult {
	var result core.ScanResult
	facts := map[string]*jsFacts{}
	for _, file := range p.Files(jsExtensions...) {
		f := extractJS(p.Read(file))
		facts[file] = f
		for _, s := range f.skipped {
			result.Warnings = append(result.Warnings, p.Rel(file)+":"+s)
		}
	}
	routes, warnings := (&expressGraph{p, facts}).routes()
	result.Routes = routes
	result.Warnings = append(result.Warnings, warnings...)
	return result
}
