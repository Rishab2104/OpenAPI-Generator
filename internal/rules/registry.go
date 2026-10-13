// Package rules holds one file per framework. Each file registers its rule set
// from init(), so adding a framework means adding a file here and nothing else.
package rules

import (
	"sort"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
)

var registry = map[string]core.Rule{}

func Register(r core.Rule) { registry[r.Name()] = r }

// All returns every registered rule set, sorted by name.
func All() []core.Rule {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]core.Rule, len(names))
	for i, name := range names {
		out[i] = registry[name]
	}
	return out
}

func Get(name string) (core.Rule, bool) {
	r, ok := registry[name]
	return r, ok
}
