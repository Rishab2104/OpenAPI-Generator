package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// URL-path helpers shared by the source scan, the crawler and the traffic converter.
// Two directions matter:
//   template -> sample:   /users/{id} -> /users/1     (something we can actually send)
//   concrete -> template: /users/42   -> /users/{id}  (what we saw, generalised)

// SampleUUID is sent for uuid parameters; "test" would never match a uuid converter.
const SampleUUID = "00000000-0000-4000-8000-000000000001"

var (
	templateParam = regexp.MustCompile(`\{([^{}/]+)\}`)
	intSegment    = regexp.MustCompile(`^-?\d+$`)
	uuidSegment   = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// Mongo ObjectIds, hashes and other long hex keys (must contain a digit).
	hexSegment = regexp.MustCompile(`(?i)^[0-9a-f]{16,}$`)
	nonAlnum   = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

// JoinPath joins path pieces with exactly one slash between them; no trailing
// slash except for "/". Slashes inside a piece are left alone.
func JoinPath(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			b.WriteString("/")
			b.WriteString(part)
		}
	}
	if b.Len() == 0 {
		return "/"
	}
	return b.String()
}

// Shape erases parameter names, for matching routes found by different stages.
func Shape(template string) string { return templateParam.ReplaceAllString(template, "{}") }

func IsIDName(name string) bool {
	return strings.EqualFold(name, "id") || hasAnySuffix(name, "Id", "ID", "_id", "-id")
}

func IsUUIDName(name string) bool {
	low := strings.ToLower(name)
	return low == "uuid" || low == "guid" || hasAnySuffix(low, "_uuid", "_guid") ||
		hasAnySuffix(name, "Uuid", "UUID", "Guid", "GUID")
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

// SampleValue is what to send for a path parameter: ids and numbers get 1,
// uuids a valid uuid, enums their first value, everything else "test".
func SampleValue(p PathParam) string {
	switch {
	case len(p.Enum) > 0:
		return p.Enum[0]
	case p.Format == "uuid" || IsUUIDName(p.Name):
		return SampleUUID
	case p.Type == "integer" || p.Type == "number" || IsIDName(p.Name):
		return "1"
	}
	return "test"
}

// Substitute fills every {param} in a template with a sample value.
func Substitute(template string, params []PathParam) string {
	known := map[string]PathParam{}
	for _, p := range params {
		known[p.Name] = p
	}
	return templateParam.ReplaceAllStringFunc(template, func(m string) string {
		name := m[1 : len(m)-1]
		p, ok := known[name]
		if !ok {
			p = PathParam{Name: name}
		}
		return SampleValue(p)
	})
}

func segmentKind(segment string) (typ, format string, dynamic bool) {
	switch {
	case intSegment.MatchString(segment):
		return "integer", "", true
	case uuidSegment.MatchString(segment):
		return "string", "uuid", true
	case hexSegment.MatchString(segment) && strings.ContainsAny(segment, "0123456789"):
		return "string", "", true
	}
	return "", "", false
}

func singular(word string) string {
	low := strings.ToLower(word)
	switch {
	case strings.HasSuffix(low, "ies") && len(word) > 3:
		return word[:len(word)-3] + "y"
	case hasAnySuffix(low, "sses", "uses", "xes", "zes", "ches", "shes"):
		return word[:len(word)-2]
	case strings.HasSuffix(low, "s") && !strings.HasSuffix(low, "ss"):
		return word[:len(word)-1]
	}
	return word
}

func idName(segment string) string {
	var words []string
	for _, w := range nonAlnum.Split(segment, -1) {
		if w != "" {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return "id"
	}
	words[len(words)-1] = singular(words[len(words)-1])
	var b strings.Builder
	b.WriteString(strings.ToLower(words[0]))
	for _, w := range words[1:] {
		b.WriteString(strings.ToUpper(w[:1]) + strings.ToLower(w[1:]))
	}
	name := b.String()
	if unicode.IsDigit(rune(name[0])) {
		name = "p" + name
	}
	return name + "Id"
}

// NormalizeConcrete turns an observed path into a template: /products/7 -> /products/{id}.
// A single dynamic segment is called {id}; with several, each is named after the
// segment before it (/users/1/posts/2 -> /users/{userId}/posts/{postId}).
func NormalizeConcrete(path string) (string, []PathParam) {
	segments := strings.Split(path, "/")
	var dynamic []int
	isDynamic := map[int]bool{}
	for i, s := range segments {
		if _, _, ok := segmentKind(s); s != "" && ok {
			dynamic = append(dynamic, i)
			isDynamic[i] = true
		}
	}
	if len(dynamic) == 0 {
		return path, nil
	}
	var params []PathParam
	used := map[string]bool{}
	for _, i := range dynamic {
		name := "id"
		if len(dynamic) > 1 {
			previous := ""
			for j := i - 1; j >= 0; j-- {
				if segments[j] != "" && !isDynamic[j] {
					previous = segments[j]
					break
				}
			}
			name = idName(previous)
		}
		base := name
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s%d", base, n)
		}
		used[name] = true
		typ, format, _ := segmentKind(segments[i])
		segments[i] = "{" + name + "}"
		params = append(params, PathParam{Name: name, Type: typ, Format: format})
	}
	return strings.Join(segments, "/"), params
}
