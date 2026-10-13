// Package core holds the language-agnostic pieces: the data model, path
// handling, read-only access to a source tree, and the Rule interface every
// framework rule set implements.
package core

import (
	"fmt"

	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
)

// HTTPMethods in display order.
var HTTPMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

// AnyMethod marks routes registered for every verb (Express's app.all()).
// The source alone can't say which verbs the app really serves, so the route
// stays open and the live stages narrow it down.
const AnyMethod = "ANY"

// MethodRank orders methods for display; unknown methods sort last.
func MethodRank(method string) int {
	for i, m := range HTTPMethods {
		if m == method {
			return i
		}
	}
	if method == AnyMethod {
		return len(HTTPMethods)
	}
	return len(HTTPMethods) + 1
}

// PathParam is a templated path segment and what is known about its type.
type PathParam struct {
	Name   string
	Type   string // OpenAPI primitive type; "" means string
	Format string
	Enum   []string
}

func (p PathParam) Schema() *ordered.Map {
	t := p.Type
	if t == "" {
		t = "string"
	}
	schema := ordered.Of("type", t)
	if p.Format != "" {
		schema.Set("format", p.Format)
	}
	if len(p.Enum) > 0 {
		enum := make([]any, len(p.Enum))
		for i, e := range p.Enum {
			enum[i] = e
		}
		schema.Set("enum", enum)
	}
	return schema
}

// Route is one (method, path) the source code says the backend serves.
type Route struct {
	Method    string // upper-case HTTP verb, or AnyMethod
	Path      string // OpenAPI template, e.g. /users/{id}
	Params    []PathParam
	Framework string
	File      string // relative to the scan root, forward slashes
	Line      int
	Handler   string
	Parser    string // how the route was read, e.g. "tokens"
	RawPath   string // the full path in the framework's own syntax, e.g. /users/:id
	Notes     []string
}

func (r Route) SamplePath() string { return Substitute(r.Path, r.Params) }
func (r Route) Location() string   { return fmt.Sprintf("%s:%d", r.File, r.Line) }

// ScanResult is what a rule set (or the whole scan) found.
type ScanResult struct {
	Routes     []Route
	Warnings   []string
	Frameworks []string
}
