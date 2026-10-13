package rules

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
)

func scanApp(t *testing.T, name string, rule core.Rule, includeTests bool) (map[string]core.Route, core.ScanResult) {
	t.Helper()
	p, err := core.NewProject("../../testdata/"+name, includeTests)
	if err != nil {
		t.Fatal(err)
	}
	if !rule.Detect(p) {
		t.Fatalf("%s not detected in %s", rule.Name(), name)
	}
	result := rule.Scan(p)
	byKey := map[string]core.Route{}
	for _, r := range result.Routes {
		byKey[r.Method+" "+r.Path] = r
	}
	return byKey, result
}

func keys(m map[string]core.Route) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(t *testing.T, got map[string]core.Route, want []string) {
	t.Helper()
	sort.Strings(want)
	if !reflect.DeepEqual(keys(got), want) {
		t.Errorf("routes:\n got  %q\n want %q", keys(got), want)
	}
}

func TestExpressFindsExactlyTheServedRoutes(t *testing.T) {
	routes, _ := scanApp(t, "express_app", expressRule{}, false)
	// no comments, strings, axios/api client calls, settings getters or test files
	sameSet(t, routes, []string{
		"GET /api",
		"GET /api/users", "POST /api/users",
		"GET /api/users/{userId}", "DELETE /api/users/{userId}",
		"GET /api/users/{userId}/avatar", "PUT /api/users/{userId}/avatar",
		"GET /api/users/search", "GET /api/users/search/{term}",
		"ANY /api/users/{userId}/ping",
		"GET /api/products/catalog/{sku}", "PATCH /api/products/{id}",
		"GET /health",
		"GET /legacy/orders/{orderId}",
		"GET /stats",
	})
}

func TestExpressRouteDetails(t *testing.T) {
	routes, result := scanApp(t, "express_app", expressRule{}, false)
	del := routes["DELETE /api/users/{userId}"]
	if !reflect.DeepEqual(del.Params, []core.PathParam{{Name: "userId", Type: "integer"}}) {
		t.Errorf("params = %+v", del.Params)
	}
	if del.File != "src/routes/users.js" || del.Line != 14 || del.Handler != "deleteUser" || del.RawPath != `/api/users/:userId(\d+)` {
		t.Errorf("delete = %+v", del)
	}
	if h := routes["PUT /api/users/{userId}/avatar"].Handler; h != "putAvatar" {
		t.Errorf("chained handler = %q", h)
	}
	if f := routes["GET /api/products/catalog/{sku}"].File; f != "src/routes/products.ts" {
		t.Errorf("ts file = %q", f)
	}
	if n := routes["GET /stats"].Notes; !reflect.DeepEqual(n, []string{unmountedNote}) {
		t.Errorf("stats notes = %q", n)
	}
	if n := routes["GET /health"].Notes; len(n) != 0 {
		t.Errorf("health notes = %q", n)
	}
	if s := routes["GET /api/users/search/{term}"].SamplePath(); s != "/api/users/search/test" {
		t.Errorf("sample = %q", s)
	}
	catchAll := false
	for _, w := range result.Warnings {
		catchAll = catchAll || strings.Contains(w, "catch-all GET /*")
	}
	if !catchAll {
		t.Errorf("no catch-all warning in %q", result.Warnings)
	}
	if _, ok := routes["GET /{wildcard}"]; ok {
		t.Error("catch-all reported as a route")
	}
}

func TestExpressTestFilesOptIn(t *testing.T) {
	routes, _ := scanApp(t, "express_app", expressRule{}, true)
	if _, ok := routes["GET /test-only"]; !ok {
		t.Error("include-tests did not include tests/users.test.js")
	}
}

func TestExpressPathSyntax(t *testing.T) {
	cases := map[string][]string{
		"/users/:id":      {"/users/{id}"},
		`/users/:id(\d+)`: {"/users/{id}"},
		"/search/:term?":  {"/search", "/search/{term}"},
		"/files/*":        {"/files/{wildcard}"},
		"/files/*rest":    {"/files/{rest}"},         // Express 5 named splat
		"/users{/:id}":    {"/users", "/users/{id}"}, // Express 5 optional group
	}
	for in, want := range cases {
		var got []string
		for _, p := range expressPathToOpenAPI(in) {
			got = append(got, p.template)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}

func TestFlaskFindsExactlyTheServedRoutes(t *testing.T) {
	routes, _ := scanApp(t, "flask_app", flaskRule{}, false)
	sameSet(t, routes, []string{
		"GET /api/users/", "POST /api/users/",
		"GET /api/users/{user_id}", "PUT /api/users/{user_id}", "DELETE /api/users/{user_id}",
		"GET /api/users/{token}/verify",
		"GET /api/users/files/{filename}",
		"POST /api/orders/",
		"GET /api/orders/reports/{period}",
		"GET /api/orders/{order_id}", "PATCH /api/orders/{order_id}",
		"GET /health", "GET /version",
		"GET /admin/stats",
	})
}

func TestFlaskRouteDetails(t *testing.T) {
	routes, result := scanApp(t, "flask_app", flaskRule{}, false)
	// register_blueprint(url_prefix=f"{API_PREFIX}/users") overrides the blueprint's own
	// "/overridden", with API_PREFIX imported from another module
	for k := range routes {
		if strings.Contains(k, "/overridden") {
			t.Errorf("blueprint's own prefix used: %s", k)
		}
	}
	report := routes["GET /api/orders/reports/{period}"] // nested blueprint: prefixes stack
	if !reflect.DeepEqual(report.Params, []core.PathParam{{Name: "period", Type: "string", Enum: []string{"daily", "weekly"}}}) ||
		report.SamplePath() != "/api/orders/reports/daily" {
		t.Errorf("report = %+v", report)
	}
	get := routes["GET /api/orders/{order_id}"] // MethodView: methods come from the class
	if get.Handler != "OrderAPI.as_view" || get.Params[0].Type != "integer" {
		t.Errorf("methodview = %+v", get)
	}
	user := routes["PUT /api/users/{user_id}"]
	if user.File != "app/users/views.py" || user.Line != 9 || user.Handler != "user" || user.SamplePath() != "/api/users/1" {
		t.Errorf("user = %+v", user)
	}
	verify := routes["GET /api/users/{token}/verify"]
	if verify.SamplePath() != "/api/users/"+core.SampleUUID+"/verify" {
		t.Errorf("verify sample = %q", verify.SamplePath())
	}
	if n := routes["GET /admin/stats"].Notes; !reflect.DeepEqual(n, []string{unregisteredNote}) {
		t.Errorf("admin notes = %q", n)
	}
	found := false
	for _, w := range result.Warnings {
		found = found || strings.Contains(w, "blueprint 'admin' is never registered")
	}
	if !found {
		t.Errorf("warnings = %q", result.Warnings)
	}
}

func TestFlaskTestFilesOptIn(t *testing.T) {
	without, _ := scanApp(t, "flask_app", flaskRule{}, false)
	with, _ := scanApp(t, "flask_app", flaskRule{}, true)
	if _, ok := without["GET /test-only"]; ok {
		t.Error("tests/ scanned by default")
	}
	if _, ok := with["GET /test-only"]; !ok {
		t.Error("include-tests did not include tests/test_app.py")
	}
}

func TestFlaskConverters(t *testing.T) {
	template, params := flaskPathToOpenAPI("/a/<int:x>/<float:y>/<uuid:z>/<path:p>/<any(one, two):k>/<plain>")
	if template != "/a/{x}/{y}/{z}/{p}/{k}/{plain}" {
		t.Errorf("template = %q", template)
	}
	want := []core.PathParam{{Name: "x", Type: "integer"}, {Name: "y", Type: "number"},
		{Name: "z", Type: "string", Format: "uuid"}, {Name: "p", Type: "string"},
		{Name: "k", Type: "string", Enum: []string{"one", "two"}}, {Name: "plain", Type: "string"}}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %+v", params)
	}
}
