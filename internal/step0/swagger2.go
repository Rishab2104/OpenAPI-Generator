package step0

// Swagger 2.0 -> OpenAPI 3.0 conversion. Fixes over the original apigen.py:
//   - every #/definitions|parameters|responses/... $ref is rewritten to #/components/...
//   - parameters are converted: type/format/items/enum/limits move under `schema`,
//     collectionFormat becomes style/explode, `in: body` and `in: formData` become a
//     requestBody (form fields as an object schema; `type: file` as binary)
//   - path-level parameters, response headers, examples, security schemes and the
//     per-operation consumes/produces are converted too

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
)

var (
	operationKeys = map[string]bool{"get": true, "put": true, "post": true, "delete": true,
		"options": true, "head": true, "patch": true}
	schemaKeys = []string{"type", "format", "items", "default", "maximum", "exclusiveMaximum", "minimum",
		"exclusiveMinimum", "maxLength", "minLength", "pattern", "maxItems", "minItems", "uniqueItems",
		"enum", "multipleOf"}
	subschemaKeys = map[string]bool{"items": true, "additionalProperties": true, "not": true,
		"allOf": true, "anyOf": true, "oneOf": true}
	refPrefixes = [][2]string{
		{"#/definitions/", "#/components/schemas/"},
		{"#/parameters/", "#/components/parameters/"},
		{"#/responses/", "#/components/responses/"},
	}
	collectionStyles = map[string]struct {
		style   string
		explode bool
	}{"csv": {"form", false}, "ssv": {"spaceDelimited", false}, "pipes": {"pipeDelimited", false}, "multi": {"form", true}}
	oauthFlows = map[string]string{"implicit": "implicit", "password": "password",
		"application": "clientCredentials", "accessCode": "authorizationCode"}
)

const (
	formURLEncoded = "application/x-www-form-urlencoded"
	multipartForm  = "multipart/form-data"
)

// ConvertSwagger2 returns an OpenAPI 3.0 document. OpenAPI 3 input is returned unchanged.
func ConvertSwagger2(doc *ordered.Map, sourceURL string) *ordered.Map {
	if doc.Has("openapi") {
		return doc
	}
	source, _ := url.Parse(sourceURL)
	if source == nil {
		source = &url.URL{}
	}
	clone := ordered.Clone(doc).(*ordered.Map)
	c := &converter{doc: clone, source: source, globalParams: clone.Map("parameters"),
		consumes: orDefault(clone.Strings("consumes")), produces: orDefault(clone.Strings("produces"))}
	return c.run()
}

func orDefault(media []string) []string {
	if len(media) == 0 {
		return []string{"application/json"}
	}
	return media
}

type converter struct {
	doc                *ordered.Map
	source             *url.URL
	consumes, produces []string
	globalParams       *ordered.Map
}

func present(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case []any:
		return len(t) > 0
	case *ordered.Map:
		return t.Len() > 0
	case string:
		return t != ""
	}
	return true
}

func (c *converter) run() *ordered.Map {
	info := c.doc.Map("info")
	if info == nil {
		info = ordered.New()
	}
	if !info.Has("title") {
		info.Set("title", "Discovered API")
	}
	if !info.Has("version") {
		info.Set("version", "1.0.0")
	}
	result := ordered.Of("openapi", "3.0.3", "info", info, "servers", c.servers())
	for _, key := range []string{"tags", "externalDocs", "security"} {
		if v, _ := c.doc.Get(key); present(v) {
			result.Set(key, v)
		}
	}
	for _, key := range c.doc.Keys() {
		if strings.HasPrefix(key, "x-") {
			v, _ := c.doc.Get(key)
			result.Set(key, v)
		}
	}
	paths := ordered.New()
	for _, path := range c.doc.Map("paths").Keys() {
		item, _ := c.doc.Map("paths").Get(path)
		paths.Set(path, c.pathItem(item))
	}
	result.Set("paths", paths)

	components := ordered.New()
	if defs := c.doc.Map("definitions"); defs.Len() > 0 {
		schemas := ordered.New()
		for _, name := range defs.Keys() {
			s, _ := defs.Get(name)
			schemas.Set(name, c.schema(s))
		}
		components.Set("schemas", schemas)
	}
	params, bodies := ordered.New(), ordered.New()
	for _, name := range c.globalParams.Keys() {
		param := c.globalParams.Map(name)
		switch param.String("in") {
		case "body":
			bodies.Set(name, c.body(param, c.consumes))
		case "formData": // form fields are inlined where they're used
		default:
			params.Set(name, c.parameter(param))
		}
	}
	if params.Len() > 0 {
		components.Set("parameters", params)
	}
	if bodies.Len() > 0 {
		components.Set("requestBodies", bodies)
	}
	if responses := c.doc.Map("responses"); responses.Len() > 0 {
		out := ordered.New()
		for _, name := range responses.Keys() {
			r, _ := responses.Get(name)
			out.Set(name, c.response(r, c.produces))
		}
		components.Set("responses", out)
	}
	if schemes := c.doc.Map("securityDefinitions"); schemes.Len() > 0 {
		out := ordered.New()
		for _, name := range schemes.Keys() {
			out.Set(name, c.security(schemes.Map(name)))
		}
		components.Set("securitySchemes", out)
	}
	if components.Len() > 0 {
		result.Set("components", components)
	}
	return rewriteRefs(result).(*ordered.Map)
}

func (c *converter) servers() []any {
	host := c.doc.String("host")
	if host == "" {
		host = c.source.Host
	}
	base := c.doc.String("basePath")
	if host == "" {
		if base == "" {
			base = "/"
		}
		return []any{ordered.Of("url", base)}
	}
	schemes := c.doc.Strings("schemes")
	if len(schemes) == 0 {
		scheme := c.source.Scheme
		if scheme == "" {
			scheme = "https"
		}
		schemes = []string{scheme}
	}
	var servers []any
	for _, scheme := range schemes {
		servers = append(servers, ordered.Of("url", fmt.Sprintf("%s://%s%s", scheme, host, base)))
	}
	return servers
}

// ---------------------------------------------------------------- parameters

func (c *converter) resolve(param *ordered.Map) *ordered.Map {
	if ref := param.String("$ref"); strings.HasPrefix(ref, "#/parameters/") {
		if global := c.globalParams.Map(strings.TrimPrefix(ref, "#/parameters/")); global != nil {
			return global
		}
		return ordered.New()
	}
	return param
}

func (c *converter) key(param *ordered.Map) string {
	r := c.resolve(param)
	return r.String("name") + "\x00" + r.String("in")
}

func (c *converter) paramSchema(param *ordered.Map) *ordered.Map {
	schema := ordered.New()
	for _, k := range schemaKeys {
		if v, ok := param.Get(k); ok {
			schema.Set(k, ordered.Clone(v))
		}
	}
	return c.schema(schema).(*ordered.Map)
}

func (c *converter) parameter(param *ordered.Map) any {
	if param.Has("$ref") {
		return param
	}
	out := ordered.New()
	for _, k := range []string{"name", "in", "description", "required", "deprecated", "allowEmptyValue"} {
		if v, ok := param.Get(k); ok {
			out.Set(k, v)
		}
	}
	for _, k := range param.Keys() {
		if strings.HasPrefix(k, "x-") && k != "x-example" {
			v, _ := param.Get(k)
			out.Set(k, v)
		}
	}
	location := param.String("in")
	if location == "path" {
		out.Set("required", true)
	}
	if location != "query" {
		out.Delete("allowEmptyValue")
	}
	out.Set("schema", c.paramSchema(param))
	if param.String("type") == "array" && (location == "query" || location == "cookie") {
		// Swagger 2 defaults to csv; OpenAPI 3 query arrays default to form+explode (= multi)
		format := param.String("collectionFormat")
		if format == "" {
			format = "csv"
		}
		style, ok := collectionStyles[format]
		if !ok {
			style = collectionStyles["csv"]
		}
		out.Set("style", style.style)
		out.Set("explode", style.explode)
	}
	if example, ok := param.Get("x-example"); ok {
		out.Set("example", example)
	}
	return out
}

// ---------------------------------------------------------------- request bodies

func (c *converter) body(param *ordered.Map, consumes []string) *ordered.Map {
	if ref := param.String("$ref"); ref != "" {
		return ordered.Of("$ref", "#/components/requestBodies/"+ref[strings.LastIndex(ref, "/")+1:])
	}
	var media []string
	for _, m := range consumes {
		if m != formURLEncoded && m != multipartForm {
			media = append(media, m)
		}
	}
	content := ordered.New()
	for _, m := range orDefault(media) {
		schema, _ := param.Get("schema")
		if schema == nil {
			schema = ordered.New()
		}
		content.Set(m, ordered.Of("schema", c.schema(ordered.Clone(schema))))
	}
	body := ordered.Of("content", content)
	if d := param.String("description"); d != "" {
		body.Set("description", d)
	}
	if param.Bool("required") {
		body.Set("required", true)
	}
	return body
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (c *converter) formBody(fields []*ordered.Map, consumes []string) *ordered.Map {
	hasFile := false
	for _, f := range fields {
		hasFile = hasFile || f.String("type") == "file"
	}
	media := formURLEncoded
	if hasFile || (contains(consumes, multipartForm) && !contains(consumes, formURLEncoded)) {
		media = multipartForm
	}
	properties := ordered.New()
	var required []any
	for _, f := range fields {
		prop := c.paramSchema(f)
		if d := f.String("description"); d != "" {
			prop.Set("description", d)
		}
		properties.Set(f.String("name"), prop)
		if f.Bool("required") {
			required = append(required, f.String("name"))
		}
	}
	schema := ordered.Of("type", "object", "properties", properties)
	if len(required) > 0 {
		schema.Set("required", required)
	}
	body := ordered.Of("content", ordered.Of(media, ordered.Of("schema", schema)))
	if len(required) > 0 {
		body.Set("required", true)
	}
	return body
}

// ---------------------------------------------------------------- paths and operations

func (c *converter) pathItem(item any) any {
	m, ok := item.(*ordered.Map)
	if !ok || m.Has("$ref") {
		return item
	}
	var shared []*ordered.Map
	for _, p := range m.List("parameters") {
		if pm, ok := p.(*ordered.Map); ok {
			shared = append(shared, pm)
		}
	}
	out := ordered.New()
	for _, key := range m.Keys() {
		value, _ := m.Get(key)
		if op, isMap := value.(*ordered.Map); operationKeys[key] && isMap {
			out.Set(key, c.operation(op, shared))
		} else if key != "parameters" {
			out.Set(key, value)
		}
	}
	var regular []any
	for _, p := range shared {
		if in := c.resolve(p).String("in"); in != "body" && in != "formData" {
			regular = append(regular, c.parameter(p))
		}
	}
	if len(regular) > 0 {
		out.Set("parameters", regular)
	}
	return out
}

func (c *converter) operation(source *ordered.Map, shared []*ordered.Map) *ordered.Map {
	op := ordered.Clone(source).(*ordered.Map)
	consumes, produces := c.consumes, c.produces
	if own := op.Strings("consumes"); len(own) > 0 {
		consumes = own
	}
	if own := op.Strings("produces"); len(own) > 0 {
		produces = own
	}
	var own []*ordered.Map
	for _, p := range op.List("parameters") {
		if pm, ok := p.(*ordered.Map); ok {
			own = append(own, pm)
		}
	}
	for _, k := range []string{"consumes", "produces", "parameters"} {
		op.Delete(k)
	}
	ownKeys := map[string]bool{}
	for _, p := range own {
		ownKeys[c.key(p)] = true
	}
	all := own
	for _, p := range shared { // path-level body/form params apply to every operation unless overridden
		if in := c.resolve(p).String("in"); (in == "body" || in == "formData") && !ownKeys[c.key(p)] {
			all = append(all, p)
		}
	}
	var params []any
	var body *ordered.Map
	var form []*ordered.Map
	for _, p := range all {
		switch c.resolve(p).String("in") {
		case "body":
			body = p
		case "formData":
			form = append(form, c.resolve(p))
		default:
			params = append(params, c.parameter(p))
		}
	}
	if len(params) > 0 {
		op.Set("parameters", params)
	}
	if body != nil {
		op.Set("requestBody", c.body(body, consumes))
	} else if len(form) > 0 {
		op.Set("requestBody", c.formBody(form, consumes))
	}
	if responses := op.Map("responses"); responses != nil {
		out := ordered.New()
		for _, code := range responses.Keys() {
			r, _ := responses.Get(code)
			out.Set(code, c.response(r, produces))
		}
		op.Set("responses", out)
	}
	return op
}

func (c *converter) response(value any, produces []string) any {
	r, ok := value.(*ordered.Map)
	if !ok || r.Has("$ref") {
		return value
	}
	out := ordered.Of("description", r.String("description"))
	for _, k := range r.Keys() {
		if strings.HasPrefix(k, "x-") {
			v, _ := r.Get(k)
			out.Set(k, v)
		}
	}
	examples := r.Map("examples")
	if schema, ok := r.Get("schema"); ok {
		content := ordered.New()
		for _, media := range produces {
			entry := ordered.Of("schema", c.schema(ordered.Clone(schema)))
			if example, ok := examples.Get(media); ok {
				entry.Set("example", example)
			}
			content.Set(media, entry)
		}
		out.Set("content", content)
	} else if examples.Len() > 0 {
		content := ordered.New()
		for _, media := range examples.Keys() {
			example, _ := examples.Get(media)
			content.Set(media, ordered.Of("example", example))
		}
		out.Set("content", content)
	}
	if headers := r.Map("headers"); headers.Len() > 0 {
		converted := ordered.New()
		for _, name := range headers.Keys() {
			h := headers.Map(name)
			header := ordered.Of("schema", c.paramSchema(h))
			if d := h.String("description"); d != "" {
				header.Set("description", d)
			}
			converted.Set(name, header)
		}
		out.Set("headers", converted)
	}
	return out
}

// ---------------------------------------------------------------- schemas and security

func (c *converter) schema(value any) any {
	switch t := value.(type) {
	case []any:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = c.schema(s)
		}
		return out
	case *ordered.Map:
		out := ordered.New()
		for _, key := range t.Keys() {
			v, _ := t.Get(key)
			switch {
			case key == "x-nullable":
				out.Set("nullable", v)
			case key == "collectionFormat":
			case key == "discriminator":
				if s, ok := v.(string); ok {
					out.Set(key, ordered.Of("propertyName", s))
				} else {
					out.Set(key, v)
				}
			case key == "properties":
				if props, ok := v.(*ordered.Map); ok {
					converted := ordered.New()
					for _, name := range props.Keys() {
						p, _ := props.Get(name)
						converted.Set(name, c.schema(p))
					}
					out.Set(key, converted)
				} else {
					out.Set(key, v)
				}
			case subschemaKeys[key]:
				out.Set(key, c.schema(v))
			default:
				out.Set(key, v) // example/enum/default are data, not schemas: leave alone
			}
		}
		if out.String("type") == "file" {
			out.Set("type", "string")
			out.Set("format", "binary")
		}
		return out
	}
	return value
}

func (c *converter) security(scheme *ordered.Map) any {
	extra := func(m *ordered.Map) *ordered.Map {
		if d := scheme.String("description"); d != "" {
			m.Set("description", d)
		}
		return m
	}
	switch scheme.String("type") {
	case "basic":
		return extra(ordered.Of("type", "http", "scheme", "basic"))
	case "apiKey":
		return extra(ordered.Of("type", "apiKey", "name", scheme.String("name"), "in", scheme.String("in")))
	case "oauth2":
		name := scheme.String("flow")
		scopes := scheme.Map("scopes")
		if scopes == nil {
			scopes = ordered.New()
		}
		flow := ordered.Of("scopes", scopes)
		if name == "implicit" || name == "accessCode" {
			flow.Set("authorizationUrl", scheme.String("authorizationUrl"))
		}
		if name == "password" || name == "application" || name == "accessCode" {
			flow.Set("tokenUrl", scheme.String("tokenUrl"))
		}
		flows := ordered.New()
		if key, ok := oauthFlows[name]; ok {
			flows.Set(key, flow)
		}
		return extra(ordered.Of("type", "oauth2", "flows", flows))
	}
	return scheme
}

func newRef(ref string) string {
	for _, p := range refPrefixes {
		if strings.HasPrefix(ref, p[0]) {
			return p[1] + strings.TrimPrefix(ref, p[0])
		}
	}
	return ref
}

func rewriteRefs(value any) any {
	switch t := value.(type) {
	case *ordered.Map:
		out := ordered.New()
		for _, k := range t.Keys() {
			v, _ := t.Get(k)
			if s, ok := v.(string); ok && k == "$ref" {
				out.Set(k, newRef(s))
			} else {
				out.Set(k, rewriteRefs(v))
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			out[i] = rewriteRefs(v)
		}
		return out
	}
	return value
}
