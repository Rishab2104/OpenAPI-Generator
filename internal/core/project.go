package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var ignoredDirs = map[string]bool{
	"node_modules": true, "bower_components": true, "vendor": true, "dist": true, "build": true,
	"out": true, "target": true, "coverage": true, "venv": true, "env": true, "__pycache__": true,
	"site-packages": true, "htmlcov": true,
}

// Test code builds throwaway apps and calls routes; neither is the served API.
var (
	testDirs = map[string]bool{"test": true, "tests": true, "__tests__": true, "spec": true,
		"specs": true, "e2e": true, "cypress": true, "testdata": true}
	testFile = regexp.MustCompile(`^test_.*\.py$|_test\.py$|^conftest\.py$|_test\.go$|\.(test|spec)\.[cm]?[jt]sx?$`)
)

const (
	maxFileBytes = 2_000_000
	utf8BOM      = string(rune(0xFEFF))
)

// Project is read-only access to the source tree being scanned.
type Project struct {
	Root         string
	IncludeTests bool

	once  sync.Once
	files []string
	set   map[string]bool
	text  map[string]string
}

func NewProject(root string, includeTests bool) (*Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "scan", Path: root, Err: fs.ErrInvalid}
	}
	return &Project{Root: filepath.Clean(abs), IncludeTests: includeTests, text: map[string]string{}}, nil
}

func (p *Project) skipDir(name string) bool {
	return ignoredDirs[name] || strings.HasPrefix(name, ".") || (!p.IncludeTests && testDirs[name])
}

// AllFiles lists every file under Root that isn't in an ignored directory, sorted.
func (p *Project) AllFiles() []string {
	p.once.Do(func() {
		p.set = map[string]bool{}
		_ = filepath.WalkDir(p.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != p.Root && p.skipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if p.IncludeTests || !testFile.MatchString(d.Name()) {
				p.files = append(p.files, path)
				p.set[path] = true
			}
			return nil
		})
		sort.Strings(p.files)
	})
	return p.files
}

// Files returns source files ending in any of the suffixes, minus minified bundles and type stubs.
func (p *Project) Files(suffixes ...string) []string {
	var out []string
	for _, f := range p.AllFiles() {
		name := filepath.Base(f)
		if strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, ".d.ts") {
			continue
		}
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// Contains reports whether path (absolute, cleaned) is a file of the project.
func (p *Project) Contains(path string) bool {
	p.AllFiles()
	return p.set[filepath.Clean(path)]
}

// Read returns a file's text ("" if unreadable or too large), cached.
func (p *Project) Read(path string) string {
	if text, ok := p.text[path]; ok {
		return text
	}
	text := ""
	if info, err := os.Stat(path); err == nil && info.Size() <= maxFileBytes {
		if data, err := os.ReadFile(path); err == nil {
			text = strings.TrimPrefix(string(data), utf8BOM)
		}
	}
	p.text[path] = text
	return text
}

// Rel is path relative to Root with forward slashes.
func (p *Project) Rel(path string) string {
	rel, err := filepath.Rel(p.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
