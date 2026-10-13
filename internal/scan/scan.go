// Package scan is step 1's orchestration: pick the rule sets that apply, run
// them, and write routes.txt (for wfuzz) and routes.json (the structured record).
package scan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
	"github.com/Rishab2104/OpenAPI-Generator/internal/rules"
)

// Run scans root with the named rule sets, or every rule set that detects its framework.
func Run(root string, frameworks []string, includeTests bool) (core.ScanResult, error) {
	project, err := core.NewProject(root, includeTests)
	if err != nil {
		return core.ScanResult{}, err
	}
	var selected []core.Rule
	if len(frameworks) > 0 {
		for _, name := range frameworks {
			rule, ok := rules.Get(name)
			if !ok {
				return core.ScanResult{}, fmt.Errorf("unknown framework %q (available: %s)", name, strings.Join(Names(), ", "))
			}
			selected = append(selected, rule)
		}
	} else {
		for _, rule := range rules.All() {
			if rule.Detect(project) {
				selected = append(selected, rule)
			}
		}
	}
	var result core.ScanResult
	for _, rule := range selected {
		found := rule.Scan(project)
		result.Frameworks = append(result.Frameworks, rule.Name())
		result.Routes = append(result.Routes, found.Routes...)
		result.Warnings = append(result.Warnings, found.Warnings...)
	}
	result.Routes = Dedupe(result.Routes)
	return result, nil
}

// Names lists the registered rule sets.
func Names() []string {
	var names []string
	for _, r := range rules.All() {
		names = append(names, r.Name())
	}
	return names
}

// Dedupe keeps one route per (method, path); later registrations become a note on the first.
func Dedupe(routes []core.Route) []core.Route {
	index := map[[2]string]int{}
	var kept []core.Route
	for _, r := range routes {
		key := [2]string{r.Method, r.Path}
		if i, ok := index[key]; ok {
			if r.Location() != kept[i].Location() {
				kept[i].Notes = append(kept[i].Notes, "also registered at "+r.Location())
			}
			continue
		}
		index[key] = len(kept)
		kept = append(kept, r)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Path != kept[j].Path {
			return kept[i].Path < kept[j].Path
		}
		return core.MethodRank(kept[i].Method) < core.MethodRank(kept[j].Method)
	})
	return kept
}

// SamplePaths is the unique sendable paths, for wfuzz's -z file,routes.txt.
func SamplePaths(routes []core.Route, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "/") && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, r := range routes {
		add(r.SamplePath())
	}
	for _, p := range extra {
		add(p)
	}
	return out
}

type paramRecord struct {
	Name     string       `json:"name"`
	In       string       `json:"in"`
	Required bool         `json:"required"`
	Schema   *ordered.Map `json:"schema"`
}

type routeRecord struct {
	Method     string        `json:"method"`
	Path       string        `json:"path"`
	SamplePath string        `json:"sample_path"`
	Params     []paramRecord `json:"params"`
	Framework  string        `json:"framework"`
	File       string        `json:"file"`
	Line       int           `json:"line"`
	Handler    string        `json:"handler"`
	Parser     string        `json:"parser"`
	RawPath    string        `json:"raw_path"`
	Notes      []string      `json:"notes"`
}

// Record is the routes.json document.
type Record struct {
	Generator  string        `json:"generator"`
	Source     string        `json:"source"`
	Frameworks []string      `json:"frameworks"`
	Routes     []routeRecord `json:"routes"`
	ExtraPaths []string      `json:"extra_paths"`
	Warnings   []string      `json:"warnings"`
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// WriteOutputs writes routes.txt and routes.json into outDir and returns their paths.
func WriteOutputs(result core.ScanResult, source, outDir string, extra []string) (string, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", err
	}
	txt, js := filepath.Join(outDir, "routes.txt"), filepath.Join(outDir, "routes.json")
	var lines strings.Builder
	for _, p := range SamplePaths(result.Routes, extra) {
		lines.WriteString(p + "\n")
	}
	if err := os.WriteFile(txt, []byte(lines.String()), 0o644); err != nil {
		return "", "", err
	}
	abs, _ := filepath.Abs(source)
	record := Record{Generator: "api-warden " + core.Version, Source: abs, Frameworks: orEmpty(result.Frameworks),
		Routes: []routeRecord{}, ExtraPaths: orEmpty(extra), Warnings: orEmpty(result.Warnings)}
	for _, r := range result.Routes {
		params := []paramRecord{}
		for _, p := range r.Params {
			params = append(params, paramRecord{p.Name, "path", true, p.Schema()})
		}
		record.Routes = append(record.Routes, routeRecord{r.Method, r.Path, r.SamplePath(), params, r.Framework,
			r.File, r.Line, r.Handler, r.Parser, r.RawPath, orEmpty(r.Notes)})
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(js, append(data, '\n'), 0o644); err != nil {
		return "", "", err
	}
	return txt, js, nil
}
