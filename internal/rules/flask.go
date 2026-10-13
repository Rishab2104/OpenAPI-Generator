package rules

// Flask: @route decorators, add_url_rule, blueprints and where they are registered.
//
// Prefixes follow Flask's rules: a blueprint's url_prefix, overridden by the
// url_prefix given to register_blueprint(), stacked for nested blueprints (Flask >= 2.0).

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
	"github.com/Rishab2104/OpenAPI-Generator/internal/lang/python"
)

func init() { Register(flaskRule{}) }

var (
	flaskImport        = regexp.MustCompile(`(?m)^\s*(?:from\s+(?:flask|apiflask|flask_smorest)\b|import\s+flask\b)`)
	appFactories       = map[string]bool{"Flask": true, "APIFlask": true}
	blueprintFactories = map[string]bool{"Blueprint": true, "APIBlueprint": true}
	flaskShortcuts     = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true} // Flask >= 2.0
	viewMethods        = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true}
	flaskRouterish     = regexp.MustCompile(`^(?:app|application|bp|blueprint|api)$|_(?:bp|blueprint)$`)
	flaskConverter     = regexp.MustCompile(`<(?:(\w+)(?:\(([^)]*)\))?:)?(\w+)>`)
)

const unregisteredNote = "blueprint is never registered in the scanned code; path shown with its own url_prefix only"

type blueprint struct {
	prefix    string
	hasPrefix bool
	line      int
}

type flaskRoute struct {
	receiver  string
	path      string
	methods   []string // nil: decided later (from a MethodView class, else GET)
	line      int
	handler   string
	viewClass string
	notes     []string
}

type registration struct {
	parent, child string
	prefix        string
	hasPrefix     bool
	notes         []string
}

type flaskFacts struct {
	importsFlask  bool
	apps          map[string]bool
	blueprints    map[string]blueprint
	routes        []flaskRoute
	registrations []registration
	classes       map[string][]string // class -> HTTP methods it handles
	skipped       []string
}

// FlaskPathToOpenAPI: /users/<int:user_id> -> /users/{user_id} with user_id: integer.
func flaskPathToOpenAPI(rule string) (string, []core.PathParam) {
	var params []core.PathParam
	template := flaskConverter.ReplaceAllStringFunc(rule, func(m string) string {
		sub := flaskConverter.FindStringSubmatch(m)
		conv, args, name := sub[1], sub[2], sub[3]
		p := core.PathParam{Name: name, Type: "string"}
		switch conv {
		case "int":
			p.Type = "integer"
		case "float":
			p.Type = "number"
		case "uuid":
			p.Format = "uuid"
		case "any":
			for _, choice := range strings.Split(args, ",") {
				if c := strings.Trim(strings.TrimSpace(choice), `'"`); c != "" {
					p.Enum = append(p.Enum, c)
				}
			}
		}
		params = append(params, p)
		return "{" + name + "}"
	})
	return template, params
}

// flaskJoin is Flask's own joining (BlueprintSetupState.add_url_rule).
func flaskJoin(prefix string, hasPrefix bool, rule string) string {
	if !hasPrefix {
		return rule
	}
	if rule == "" {
		return prefix
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(rule, "/")
}

// stackPrefix is a nested blueprint's prefix (Blueprint.register).
func stackPrefix(parent string, hasParent bool, child string, hasChild bool) (string, bool) {
	switch {
	case hasParent && hasChild:
		return strings.TrimRight(parent, "/") + "/" + strings.TrimLeft(child, "/"), true
	case hasChild:
		return child, true
	}
	return parent, hasParent
}

type flaskExtractor struct {
	mod    *python.Module
	lookup python.Lookup
	facts  *flaskFacts
}

func extractFlask(mods *python.Modules, file, src string) *flaskFacts {
	x := &flaskExtractor{mod: mods.Get(file), lookup: mods.Lookup(file), facts: &flaskFacts{
		importsFlask: flaskImport.MatchString(src), apps: map[string]bool{},
		blueprints: map[string]blueprint{}, classes: map[string][]string{},
	}}
	lines := x.mod.Lines
	for k, line := range lines {
		switch {
		case line[0].IsOp("@"):
			x.decorator(line, lines[k+1:])
		case defOrClassName(line, "class") != "":
			x.class(line, lines[k+1:])
		default:
			x.assignment(line)
		}
	}
	toks := x.mod.Tokens
	for i := range toks {
		x.call(toks, i)
	}
	return x.facts
}

func defOrClassName(line []python.Token, keyword string) string {
	k := 0
	if len(line) > 1 && line[0].IsName("async") {
		k = 1
	}
	if line[k].IsName(keyword) && k+1 < len(line) && line[k+1].Kind == python.Name {
		return line[k+1].Text
	}
	return ""
}

// callOf reads `a.b.c(` at toks[0]: the dotted callee and the index of "(".
func callOf(toks []python.Token) (string, int) {
	for k := 1; k < len(toks); k += 2 {
		if toks[k].IsOp("(") {
			name, ok := python.Dotted(toks[:k])
			if !ok {
				return "", -1
			}
			return name, k
		}
		if !toks[k].IsOp(".") {
			return "", -1
		}
	}
	return "", -1
}

func lastPart(dotted string) (head, tail string) {
	if k := strings.LastIndex(dotted, "."); k >= 0 {
		return dotted[:k], dotted[k+1:]
	}
	return "", dotted
}

func (x *flaskExtractor) assignment(line []python.Token) {
	targets, value := python.Assignment(line)
	if len(targets) == 0 || len(value) == 0 {
		return
	}
	callee, open := callOf(value)
	if open < 0 {
		return
	}
	args, close := python.ParseArgs(value, open)
	if close != len(value)-1 {
		return
	}
	_, factory := lastPart(callee)
	switch {
	case appFactories[factory]:
		for _, t := range targets {
			x.facts.apps[t] = true
		}
	case blueprintFactories[factory]:
		bp := blueprint{line: line[0].Line}
		if prefix := args.Kw["url_prefix"]; prefix != nil {
			bp.prefix, bp.hasPrefix = python.Static(prefix, x.lookup)
			if !bp.hasPrefix {
				x.facts.skipped = append(x.facts.skipped, fmt.Sprintf("%d: blueprint url_prefix could not be resolved statically", line[0].Line))
			}
		}
		for _, t := range targets {
			x.facts.blueprints[t] = bp
		}
	}
}

func (x *flaskExtractor) methods(args python.Args, notes *[]string) []string {
	node := args.Kw["methods"]
	if node == nil {
		return nil
	}
	values, ok := python.StaticList(node, x.lookup)
	if !ok {
		*notes = append(*notes, "methods=... could not be read statically; assumed GET")
		return nil
	}
	for i, v := range values {
		values[i] = strings.ToUpper(v)
	}
	return values
}

func (x *flaskExtractor) decorator(line []python.Token, following [][]python.Token) {
	callee, open := callOf(line[1:])
	if open < 0 {
		return
	}
	receiver, kind := lastPart(callee)
	if receiver == "" || (kind != "route" && !flaskShortcuts[kind]) {
		return
	}
	args, _ := python.ParseArgs(line[1:], open)
	lineNo := line[0].Line
	path, ok := python.Static(args.Arg(0, "rule"), x.lookup)
	if !ok {
		x.facts.skipped = append(x.facts.skipped, fmt.Sprintf("%d: @%s: path could not be resolved statically", lineNo, callee))
		return
	}
	handler := ""
	for _, next := range following { // skip any further decorators to reach the def
		if next[0].IsOp("@") {
			continue
		}
		handler = defOrClassName(next, "def")
		break
	}
	var notes []string
	methods := []string{strings.ToUpper(kind)}
	if kind == "route" {
		if methods = x.methods(args, &notes); methods == nil {
			methods = []string{"GET"}
		}
	}
	x.facts.routes = append(x.facts.routes, flaskRoute{receiver, path, methods, lineNo, handler, "", notes})
}

func (x *flaskExtractor) class(line []python.Token, body [][]python.Token) {
	name := defOrClassName(line, "class")
	indent := line[0].Indent
	var handlers, explicit []string
	bodyIndent := -1
	for _, l := range body {
		if l[0].Indent <= indent {
			break
		}
		if bodyIndent < 0 {
			bodyIndent = l[0].Indent
		}
		if l[0].Indent != bodyIndent {
			continue
		}
		if fn := defOrClassName(l, "def"); viewMethods[fn] {
			handlers = append(handlers, strings.ToUpper(fn))
		}
		if targets, value := python.Assignment(l); len(targets) == 1 && targets[0] == "methods" {
			if values, ok := python.StaticList(value, x.lookup); ok {
				for _, m := range values {
					explicit = append(explicit, strings.ToUpper(m))
				}
			}
		}
	}
	if len(explicit) > 0 { // an explicit `methods = [...]` wins
		handlers = explicit
	}
	if len(handlers) > 0 {
		x.facts.classes[name] = handlers
	}
}

func (x *flaskExtractor) call(toks []python.Token, i int) {
	if toks[i].Kind != python.Name || (i > 0 && toks[i-1].IsOp(".")) || (i > 0 && toks[i-1].IsOp("@")) {
		return
	}
	callee, open := callOf(toks[i:])
	if open < 0 {
		return
	}
	receiver, method := lastPart(callee)
	if receiver == "" || (method != "register_blueprint" && method != "add_url_rule") {
		return
	}
	args, _ := python.ParseArgs(toks[i:], open)
	lineNo := toks[i].Line
	if method == "register_blueprint" {
		child, ok := python.Dotted(args.Arg(0, "blueprint"))
		if !ok {
			return
		}
		reg := registration{parent: receiver, child: child}
		if prefix := args.Kw["url_prefix"]; prefix != nil {
			reg.prefix, reg.hasPrefix = python.Static(prefix, x.lookup)
			if !reg.hasPrefix {
				reg.notes = append(reg.notes, "url_prefix given to register_blueprint() could not be resolved; the real path has a different prefix")
			}
		}
		x.facts.registrations = append(x.facts.registrations, reg)
		return
	}
	path, ok := python.Static(args.Arg(0, "rule"), x.lookup)
	if !ok {
		x.facts.skipped = append(x.facts.skipped, fmt.Sprintf("%d: %s.add_url_rule: path could not be resolved statically", lineNo, receiver))
		return
	}
	route := flaskRoute{receiver: receiver, path: path, line: lineNo}
	view := args.Arg(2, "view_func")
	if viewCallee, viewOpen := callOf(view); viewOpen > 0 && strings.HasSuffix(viewCallee, ".as_view") {
		route.viewClass = strings.TrimSuffix(viewCallee, ".as_view")
		route.handler = viewCallee
	} else if name, ok := python.Dotted(view); ok {
		route.handler = name
	}
	route.methods = x.methods(args, &route.notes)
	x.facts.routes = append(x.facts.routes, route)
}

type flaskRule struct{}

func (flaskRule) Name() string     { return "flask" }
func (flaskRule) Language() string { return "python" }

func (flaskRule) Detect(p *core.Project) bool {
	for _, f := range p.Files(".py") {
		if flaskImport.MatchString(p.Read(f)) {
			return true
		}
	}
	return false
}

type pyNode struct{ file, name string }

func (flaskRule) Scan(p *core.Project) core.ScanResult {
	var result core.ScanResult
	mods := python.NewModules(p)
	facts := map[string]*flaskFacts{}
	for _, file := range p.Files(".py") {
		facts[file] = extractFlask(mods, file, p.Read(file))
		for _, s := range facts[file].skipped {
			result.Warnings = append(result.Warnings, p.Rel(file)+":"+s)
		}
	}
	canon := func(file, name string) pyNode {
		f, n := mods.Canonical(file, name)
		return pyNode{f, n}
	}
	apps, blueprints := map[pyNode]bool{}, map[pyNode]blueprint{}
	for file, fx := range facts {
		for name := range fx.apps {
			apps[pyNode{file, name}] = true
		}
		for name, bp := range fx.blueprints {
			blueprints[pyNode{file, name}] = bp
		}
	}

	type attached struct {
		file  string
		route flaskRoute
	}
	routesOf := map[pyNode][]attached{}
	type flaskEdge struct {
		child pyNode
		reg   registration
	}
	edges, children := map[pyNode][]flaskEdge{}, map[pyNode]bool{}
	for _, file := range sortedKeys(facts) {
		fx := facts[file]
		for _, r := range fx.routes {
			n := canon(file, r.receiver)
			_, isBlueprint := blueprints[n]
			if apps[n] || isBlueprint || (fx.importsFlask && flaskRouterish.MatchString(n.name)) {
				routesOf[n] = append(routesOf[n], attached{file, r})
			}
		}
		for _, reg := range fx.registrations {
			parent, child := canon(file, reg.parent), canon(file, reg.child)
			edges[parent] = append(edges[parent], flaskEdge{child, reg})
			children[child] = true
		}
	}

	var walk func(n pyNode, prefix string, hasPrefix bool, notes []string, trail map[pyNode]bool)
	walk = func(n pyNode, prefix string, hasPrefix bool, notes []string, trail map[pyNode]bool) {
		for _, a := range routesOf[n] {
			result.Routes = append(result.Routes, emitFlask(p, mods, facts, a.file, a.route, prefix, hasPrefix, notes)...)
		}
		for _, e := range edges[n] {
			if trail[e.child] {
				continue
			}
			own := blueprints[e.child]
			childPrefix, hasChild := own.prefix, own.hasPrefix
			if e.reg.hasPrefix {
				childPrefix, hasChild = e.reg.prefix, true
			}
			stacked, hasStacked := stackPrefix(prefix, hasPrefix, childPrefix, hasChild)
			trail[e.child] = true
			walk(e.child, stacked, hasStacked, append(append([]string(nil), notes...), e.reg.notes...), trail)
			delete(trail, e.child)
		}
	}
	all := map[node]bool{}
	for n := range routesOf {
		all[node(n)] = true
	}
	for n := range edges {
		all[node(n)] = true
	}
	for _, root := range sortedNodes(all) {
		r := pyNode(root)
		if children[r] {
			continue
		}
		if bp, ok := blueprints[r]; ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d: blueprint '%s' is never registered; "+
				"its routes are listed with only its own url_prefix", p.Rel(r.file), bp.line, r.name))
			walk(r, bp.prefix, bp.hasPrefix, []string{unregisteredNote}, map[pyNode]bool{r: true})
		} else {
			walk(r, "", false, nil, map[pyNode]bool{r: true})
		}
	}
	return result
}

func emitFlask(p *core.Project, mods *python.Modules, facts map[string]*flaskFacts, file string, r flaskRoute,
	prefix string, hasPrefix bool, notes []string) []core.Route {
	notes = append(append([]string(nil), notes...), r.notes...)
	methods := r.methods
	if methods == nil && r.viewClass != "" {
		classFile, className := mods.Canonical(file, r.viewClass)
		if fx := facts[classFile]; fx != nil {
			methods = fx.classes[className]
		}
		if methods == nil {
			notes = append(notes, fmt.Sprintf("view class %s not found; assumed GET", r.viewClass))
		}
	}
	if methods == nil {
		methods = []string{"GET"}
	}
	full := flaskJoin(prefix, hasPrefix, r.path)
	template, params := flaskPathToOpenAPI(full)
	var out []core.Route
	for _, m := range methods {
		out = append(out, core.Route{Method: m, Path: template, Params: params, Framework: "flask",
			File: p.Rel(file), Line: r.line, Handler: r.handler, Parser: "tokens", RawPath: full,
			Notes: append([]string(nil), notes...)})
	}
	return out
}
