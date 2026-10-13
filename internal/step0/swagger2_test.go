package step0

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
)

func converted(t *testing.T) *ordered.Map {
	t.Helper()
	return ConvertSwagger2(petstore(t), "http://127.0.0.1:8080/v2/api-docs")
}

// asJSON normalises a document subtree for comparison.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func expectJSON(t *testing.T, what string, got any, want string) {
	t.Helper()
	var a, b any
	_ = json.Unmarshal([]byte(asJSON(t, got)), &a)
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("bad expectation for %s: %v", what, err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s:\n got  %s\n want %s", what, asJSON(t, got), want)
	}
}

func TestConvertedSpecIsValidOpenAPI3(t *testing.T) {
	spec := converted(t)
	validate(t, spec)
	if v, _ := spec.Get("openapi"); v != "3.0.3" {
		t.Errorf("openapi = %v", v)
	}
	if keys := spec.Keys(); !reflect.DeepEqual(keys[:4], []string{"openapi", "info", "servers", "paths"}) {
		t.Errorf("key order = %q", keys)
	}
}

func TestNoSwagger2RefsSurvive(t *testing.T) { // known bug 1
	spec := converted(t)
	text := asJSON(t, spec)
	for _, old := range []string{"#/definitions/", `"#/parameters/`, `"#/responses/`} {
		if strings.Contains(text, old) {
			t.Errorf("%s still referenced", old)
		}
	}
	paths := spec.Map("paths")
	expectJSON(t, "404", paths.Map("/pets/{petId}").Map("get").Map("responses").Map("404"),
		`{"$ref": "#/components/responses/NotFound"}`)
	expectJSON(t, "Pet.tags.items", spec.Map("components").Map("schemas").Map("Pet").Map("properties").Map("tags").Map("items"),
		`{"$ref": "#/components/schemas/Tag"}`)
}

func TestParametersGetSchemas(t *testing.T) { // known bug 2
	spec := converted(t)
	params := spec.Map("paths").Map("/pets").Map("get").List("parameters")
	expectJSON(t, "limit ref", params[0], `{"$ref": "#/components/parameters/limitParam"}`)
	expectJSON(t, "limitParam", spec.Map("components").Map("parameters").Map("limitParam").Map("schema"),
		`{"type": "integer", "minimum": 1, "maximum": 100}`)
	expectJSON(t, "tags", params[1],
		`{"name": "tags", "in": "query", "schema": {"type": "array", "items": {"type": "string"}}, "style": "form", "explode": true}`)
	fields := params[2].(*ordered.Map) // Swagger 2 default csv
	if fields.String("style") != "form" || fields.Bool("explode") {
		t.Errorf("fields = %s", asJSON(t, fields))
	}
	expectJSON(t, "path-level params", spec.Map("paths").Map("/pets/{petId}").List("parameters"),
		`[{"$ref": "#/components/parameters/petIdParam"}]`)
}

func TestBodyAndFormParametersBecomeRequestBodies(t *testing.T) {
	spec := converted(t)
	paths := spec.Map("paths")
	post := paths.Map("/pets").Map("post")
	if post.Has("parameters") {
		t.Error("body param left in parameters")
	}
	expectJSON(t, "requestBody", post.Map("requestBody"), `{"$ref": "#/components/requestBodies/petBody"}`)
	expectJSON(t, "petBody", spec.Map("components").Map("requestBodies").Map("petBody"),
		`{"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}, "required": true}`)

	form := paths.Map("/pets/{petId}").Map("post").Map("requestBody").Map("content").Map("application/x-www-form-urlencoded").Map("schema")
	expectJSON(t, "form required", form.List("required"), `["name"]`)
	expectJSON(t, "form status", form.Map("properties").Map("status"), `{"type": "string", "enum": ["available", "sold"]}`)

	upload := paths.Map("/pets/{petId}/photo").Map("post")
	multipart := upload.Map("requestBody").Map("content").Map("multipart/form-data").Map("schema")
	expectJSON(t, "file", multipart.Map("properties").Map("file"), `{"type": "string", "format": "binary"}`)
	expectJSON(t, "upload params", upload.List("parameters"), `[{"$ref": "#/components/parameters/petIdParam"}]`)
}

func TestResponsesSchemasAndSecurity(t *testing.T) {
	spec := converted(t)
	ok := spec.Map("paths").Map("/pets").Map("get").Map("responses").Map("200")
	media := ok.Map("content").Map("application/json")
	expectJSON(t, "schema", media.Map("schema"), `{"type": "array", "items": {"$ref": "#/components/schemas/Pet"}}`)
	expectJSON(t, "example", media.List("example"), `[{"id": 1, "name": "Rex"}]`)
	expectJSON(t, "header", ok.Map("headers").Map("X-Total-Count"), `{"schema": {"type": "integer"}, "description": "Total pets"}`)
	pet := spec.Map("components").Map("schemas").Map("Pet")
	expectJSON(t, "discriminator", pet.Map("discriminator"), `{"propertyName": "kind"}`)
	expectJSON(t, "nullable", pet.Map("properties").Map("name"), `{"type": "string", "nullable": true}`)
	schemes := spec.Map("components").Map("securitySchemes")
	expectJSON(t, "basic", schemes.Map("basic"), `{"type": "http", "scheme": "basic"}`)
	if url := schemes.Map("oauth").Map("flows").Map("implicit").String("authorizationUrl"); url != "http://auth.example/authorize" {
		t.Errorf("oauth url = %q", url)
	}
}

func TestServerFallsBackToWhereTheSpecWasFound(t *testing.T) {
	expectJSON(t, "servers", converted(t).List("servers"), `[{"url": "http://127.0.0.1:8080/v2"}]`)
}

func TestOpenAPI3PassesThroughUntouched(t *testing.T) {
	doc := ordered.Of("openapi", "3.0.0", "info", ordered.Of("title", "x", "version", "1"), "paths", ordered.New())
	if ConvertSwagger2(doc, "http://x") != doc {
		t.Error("OpenAPI 3 document was rebuilt")
	}
}

func TestYAMLResponseCodesStayStrings(t *testing.T) {
	doc, err := ordered.Parse([]byte("swagger: '2.0'\ninfo: {title: t, version: '1'}\npaths:\n  /a:\n    get:\n      responses:\n        200: {description: ok}\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ordered.YAML(ConvertSwagger2(doc.(*ordered.Map), "http://x"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"200":`) {
		t.Errorf("response code not a string key:\n%s", out)
	}
}
