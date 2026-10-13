// Package ordered holds JSON/YAML documents with their key order intact.
//
// OpenAPI documents are read and written as trees of *Map, []any and scalars.
// Go maps would sort keys on output; keeping source order makes converted
// specs read like the original and keeps "openapi, info, servers, paths" first.
package ordered

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// Map is a string-keyed map that remembers insertion order.
type Map struct {
	keys []string
	vals map[string]any
}

func New() *Map { return &Map{vals: map[string]any{}} }

// Of builds a Map from alternating key, value arguments.
func Of(kv ...any) *Map {
	m := New()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

// Set adds or replaces a key; a replaced key keeps its position.
func (m *Map) Set(key string, value any) *Map {
	if _, ok := m.vals[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.vals[key] = value
	return m
}

func (m *Map) Get(key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.vals[key]
	return v, ok
}

func (m *Map) Has(key string) bool { _, ok := m.Get(key); return ok }

func (m *Map) Delete(key string) {
	if _, ok := m.vals[key]; !ok {
		return
	}
	delete(m.vals, key)
	for i, k := range m.keys {
		if k == key {
			m.keys = append(m.keys[:i:i], m.keys[i+1:]...)
			break
		}
	}
}

func (m *Map) Keys() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.keys...)
}

func (m *Map) Len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

// Map returns the value at key if it is a *Map, else nil.
func (m *Map) Map(key string) *Map {
	v, _ := m.Get(key)
	sub, _ := v.(*Map)
	return sub
}

// List returns the value at key if it is a list, else nil.
func (m *Map) List(key string) []any {
	v, _ := m.Get(key)
	list, _ := v.([]any)
	return list
}

// String returns the value at key if it is a string, else "".
func (m *Map) String(key string) string {
	v, _ := m.Get(key)
	s, _ := v.(string)
	return s
}

// Bool returns the value at key if it is a bool, else false.
func (m *Map) Bool(key string) bool {
	v, _ := m.Get(key)
	b, _ := v.(bool)
	return b
}

// Strings returns the value at key as a []string, skipping non-strings.
func (m *Map) Strings(key string) []string {
	var out []string
	for _, v := range m.List(key) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Clone deep-copies a document tree.
func Clone(v any) any {
	switch t := v.(type) {
	case *Map:
		out := New()
		for _, k := range t.keys {
			out.Set(k, Clone(t.vals[k]))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Clone(x)
		}
		return out
	default:
		return v
	}
}

// Parse reads a JSON or YAML document. Mapping keys always become strings,
// so YAML's `200:` response codes come back as "200".
func Parse(data []byte) (any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return nil, nil
	}
	return fromNode(&root)
}

func fromNode(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return fromNode(n.Content[0])
	case yaml.AliasNode:
		return fromNode(n.Alias)
	case yaml.MappingNode:
		m := New()
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Tag == "!!merge" {
				merged, err := fromNode(val)
				if err != nil {
					return nil, err
				}
				if mm, ok := merged.(*Map); ok {
					for _, k := range mm.keys {
						if !m.Has(k) {
							m.Set(k, mm.vals[k])
						}
					}
				}
				continue
			}
			v, err := fromNode(val)
			if err != nil {
				return nil, err
			}
			m.Set(key.Value, v)
		}
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := fromNode(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			err := n.Decode(&b)
			return b, err
		case "!!int":
			if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
				return i, nil
			}
			var f float64
			err := n.Decode(&f)
			return f, err
		case "!!float":
			var f float64
			err := n.Decode(&f)
			return f, err
		default:
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("unsupported YAML node kind %d", n.Kind)
}

// ToNode converts a document tree to a YAML node, keeping key order.
func ToNode(v any) *yaml.Node {
	switch t := v.(type) {
	case *Map:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range t.keys {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, ToNode(t.vals[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, x := range t {
			n.Content = append(n.Content, ToNode(x))
		}
		return n
	case []string:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, x := range t {
			n.Content = append(n.Content, ToNode(x))
		}
		return n
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		m := New()
		for _, k := range keys {
			m.Set(k, t[k])
		}
		return ToNode(m)
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
	return n
}

func (m *Map) MarshalYAML() (any, error) { return ToNode(m), nil }

func (m *Map) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		val, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// YAML renders a document with 2-space indentation.
func YAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(ToNode(v)); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
