package python

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
)

// Import is one imported name.
type Import struct {
	Module string // dotted module path as written ("" for `from . import x`)
	Level  int    // number of leading dots
	Name   string // imported attribute; "" for `import a.b as m`
}

// Module is one parsed source file.
type Module struct {
	File    string
	Tokens  []Token
	Lines   [][]Token
	Imports map[string]Import
	Defined map[string]bool // names the module binds itself, anywhere (including inside app factories)

	constants map[string]string
}

// Modules is every Python file of a project, plus import-following between them.
type Modules struct {
	project *core.Project
	files   map[string]*Module
	index   map[string][]string
}

func NewModules(p *core.Project) *Modules {
	m := &Modules{project: p, files: map[string]*Module{}, index: map[string][]string{}}
	for _, file := range p.Files(".py") {
		rel := strings.TrimSuffix(p.Rel(file), ".py")
		parts := strings.Split(rel, "/")
		if parts[len(parts)-1] == "__init__" {
			parts = parts[:len(parts)-1]
		}
		// register every suffix so "app.users" is found whether the code
		// lives at ./app/users or ./src/app/users
		for i := range parts {
			key := strings.Join(parts[i:], ".")
			m.index[key] = append(m.index[key], file)
		}
	}
	return m
}

// Get parses a file (once).
func (m *Modules) Get(file string) *Module {
	if mod, ok := m.files[file]; ok {
		return mod
	}
	toks := Tokenize(m.project.Read(file))
	mod := &Module{File: file, Tokens: toks, Lines: Lines(toks), Imports: map[string]Import{}, Defined: map[string]bool{}}
	for _, line := range mod.Lines {
		collectImports(line, mod.Imports)
		for _, name := range assignedNames(line) {
			mod.Defined[name] = true
		}
		if k := defOrClass(line); k >= 0 && k+1 < len(line) && line[k+1].Kind == Name {
			mod.Defined[line[k+1].Text] = true
		}
	}
	m.files[file] = mod
	return mod
}

// defOrClass returns the index of `def` / `class` starting a line (after `async`), or -1.
func defOrClass(line []Token) int {
	k := 0
	if len(line) > 1 && line[0].IsName("async") {
		k = 1
	}
	if line[k].IsName("def") || line[k].IsName("class") {
		return k
	}
	return -1
}

func collectImports(line []Token, into map[string]Import) {
	switch {
	case line[0].IsName("from"):
		k, level := 1, 0
		for ; k < len(line) && (line[k].IsOp(".") || line[k].IsOp("...")); k++ {
			level += len(line[k].Text)
		}
		start := k
		for k < len(line) && !line[k].IsName("import") {
			k++
		}
		module, _ := Dotted(line[start:k])
		names := line[min(k+1, len(line)):]
		if len(names) > 0 && names[0].IsOp("(") {
			names = names[1 : len(names)-1]
		}
		for _, part := range SplitTop(names, ",") {
			if part[0].Kind != Name {
				continue
			}
			local := part[0].Text
			if len(part) == 3 && part[1].IsName("as") {
				local = part[2].Text
			}
			into[local] = Import{Module: module, Level: level, Name: part[0].Text}
		}
	case line[0].IsName("import"):
		for _, part := range SplitTop(line[1:], ",") {
			asAt := len(part)
			for k, t := range part {
				if t.IsName("as") {
					asAt = k
				}
			}
			module, ok := Dotted(part[:asAt])
			if !ok {
				continue
			}
			if asAt+1 < len(part) {
				into[part[asAt+1].Text] = Import{Module: module}
			} else {
				head := strings.Split(module, ".")[0]
				if _, exists := into[head]; !exists {
					into[head] = Import{Module: head}
				}
			}
		}
	}
}

// Assignment splits `a = b = value` / `a: T = value` into target names and the value.
func Assignment(line []Token) (targets []string, value []Token) {
	depth, last := 0, -1
	var cuts []int
	for k, t := range line {
		if t.Kind != Op {
			continue
		}
		switch t.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		case "=":
			if depth == 0 {
				cuts = append(cuts, k)
				last = k
			}
		}
	}
	if last < 0 {
		return nil, nil
	}
	start := 0
	for _, cut := range cuts {
		target := line[start:cut]
		if len(target) >= 1 && target[0].Kind == Name && (len(target) == 1 || target[1].IsOp(":")) {
			targets = append(targets, target[0].Text)
		}
		start = cut + 1
	}
	return targets, line[last+1:]
}

func assignedNames(line []Token) []string {
	if line[0].Kind != Name || line[0].IsName("def") || line[0].IsName("class") {
		return nil
	}
	targets, _ := Assignment(line)
	return targets
}

// Resolve maps a module reference to a project file; "" for third-party modules.
func (m *Modules) Resolve(fromFile, module string, level int) string {
	if level > 0 {
		pkg := filepath.Dir(fromFile)
		for i := 1; i < level; i++ {
			pkg = filepath.Dir(pkg)
		}
		target := pkg
		if module != "" {
			target = filepath.Join(pkg, filepath.FromSlash(strings.ReplaceAll(module, ".", "/")))
			if m.project.Contains(target + ".py") {
				return target + ".py"
			}
		}
		if init := filepath.Join(target, "__init__.py"); m.project.Contains(init) {
			return init
		}
		return ""
	}
	candidates := m.index[module]
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return strings.Count(candidates[i], string(filepath.Separator)) < strings.Count(candidates[j], string(filepath.Separator))
	})
	return candidates[0]
}

func (m *Modules) submodule(packageInit, name string) string {
	if filepath.Base(packageInit) != "__init__.py" {
		return ""
	}
	dir := filepath.Dir(packageInit)
	for _, c := range []string{filepath.Join(dir, name+".py"), filepath.Join(dir, name, "__init__.py")} {
		if m.project.Contains(c) {
			return c
		}
	}
	return ""
}

// Canonical follows imports until reaching the module that defines name (which may be dotted).
func (m *Modules) Canonical(file, name string) (string, string) { return m.canonical(file, name, 0) }

func (m *Modules) canonical(file, name string, depth int) (string, string) {
	head, rest, dotted := strings.Cut(name, ".")
	mod := m.Get(file)
	imp, ok := mod.Imports[head]
	if !ok || mod.Defined[head] || depth > 12 {
		return file, name
	}
	if imp.Name == "" { // import pkg.mod as m; m.bp
		if target := m.Resolve(file, imp.Module, 0); target != "" && dotted {
			return m.canonical(target, rest, depth+1)
		}
		return file, name
	}
	base := m.Resolve(file, imp.Module, imp.Level)
	if base == "" {
		return file, name // third-party
	}
	bm := m.Get(base)
	if _, imported := bm.Imports[imp.Name]; !bm.Defined[imp.Name] && !imported {
		if sub := m.submodule(base, imp.Name); sub != "" { // from pkg import views; views.bp
			if dotted {
				return m.canonical(sub, rest, depth+1)
			}
			return file, name
		}
	}
	next := imp.Name
	if dotted {
		next += "." + rest
	}
	return m.canonical(base, next, depth+1)
}

// Constants are the string constants a module assigns, e.g. API_PREFIX = "/api".
func (m *Modules) Constants(file string) map[string]string {
	mod := m.Get(file)
	if mod.constants != nil {
		return mod.constants
	}
	mod.constants = map[string]string{} // set early: guards against import cycles
	for _, line := range mod.Lines {
		targets, value := Assignment(line)
		if len(targets) == 1 && line[0].Kind == Name {
			if v, ok := Static(value, m.Lookup(file)); ok {
				mod.constants[targets[0]] = v
			}
		}
	}
	return mod.constants
}

// Lookup is a name -> string-constant resolver for expressions in file.
func (m *Modules) Lookup(file string) Lookup {
	return func(name string) (string, bool) {
		if v, ok := m.Constants(file)[name]; ok {
			return v, true
		}
		if _, ok := m.Get(file).Imports[name]; !ok {
			return "", false
		}
		target, local := m.Canonical(file, name)
		if target == file {
			return "", false
		}
		v, ok := m.Constants(target)[local]
		return v, ok
	}
}
