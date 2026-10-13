package step0

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
)

// validate fails the test unless spec is a valid OpenAPI 3 document.
func validate(t *testing.T, spec *ordered.Map) {
	t.Helper()
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatalf("not loadable as OpenAPI 3: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("invalid OpenAPI 3: %v", err)
	}
}

func petstore(t *testing.T) *ordered.Map {
	t.Helper()
	data, err := os.ReadFile("../../testdata/swagger2_petstore.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ordered.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc.(*ordered.Map)
}

// site serves {path: page} from 127.0.0.1 and records the paths it was asked for.
type testPage struct {
	status      int
	contentType string
	body        string
}

func site(t *testing.T, pages map[string]testPage) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		p, ok := pages[r.URL.Path]
		if !ok {
			p = testPage{404, "text/plain", "not found"}
		}
		w.Header().Set("Content-Type", p.contentType)
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(p.body))
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://") // no scheme: Discover must pick http:// itself
	return host, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), hits...) }
}

func quiet(string) {}

func hit(hits []string, path string) bool {
	for _, h := range hits {
		if h == path {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- known bug 3

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"localhost:3000":         "http://localhost:3000",
		"127.0.0.1:5000/api/":    "http://127.0.0.1:5000/api",
		"[::1]:8000":             "http://[::1]:8000",
		"shop.test":              "http://shop.test",
		"backend:8080":           "http://backend:8080", // docker-compose service name
		"192.168.1.20:8000":      "http://192.168.1.20:8000",
		"example.com":            "https://example.com",
		"http://example.com/":    "http://example.com",
		"https://localhost:8443": "https://localhost:8443", // an explicit scheme is kept
	}
	for in, want := range cases {
		if got, err := NormalizeURL(in); err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeURL("ftp://example.com"); err == nil {
		t.Error("ftp accepted")
	}
	if IsLocalHost("api.example.com") {
		t.Error("public host treated as local")
	}
}

func TestValidSpecAcceptsUnquotedYAMLVersions(t *testing.T) {
	for _, text := range []string{"swagger: 2.0\npaths: {}", "openapi: 3.1\npaths: {}", `{"openapi": "3.0.0", "paths": {}}`} {
		if !ValidSpec(ParseDocument(text)) {
			t.Errorf("rejected %q", text)
		}
	}
	for _, text := range []string{"openapi: '2.0'", "<!doctype html><html></html>", "just text"} {
		if ValidSpec(ParseDocument(text)) {
			t.Errorf("accepted %q", text)
		}
	}
}

// ---------------------------------------------------------------- discovery

func TestDiscoversAndConvertsALocalSwagger2Spec(t *testing.T) {
	data, _ := os.ReadFile("../../testdata/swagger2_petstore.json")
	host, hits := site(t, map[string]testPage{"/v2/api-docs": {200, "application/json", string(data)}})
	d, err := Discover(host, Options{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "published" || d.Version != "swagger 2.0" || d.SourceURL != "http://"+host+"/v2/api-docs" {
		t.Fatalf("discovery = %+v", d)
	}
	if got := d.Spec.List("servers")[0].(*ordered.Map).String("url"); got != "http://"+host+"/v2" {
		t.Errorf("server = %q", got)
	}
	validate(t, d.Spec)
	if !hit(hits(), "/v3/api-docs") {
		t.Error("earlier candidates were not probed")
	}
}

func TestHTMLCatchAllIsNotMistakenForASpec(t *testing.T) {
	spa := testPage{200, "text/html", "<!doctype html><html><title>SPA</title></html>"}
	host, _ := site(t, map[string]testPage{"/openapi.json": spa, "/swagger.json": spa, "/": spa})
	d, err := Discover(host, Options{Log: quiet})
	if err != nil || d.Kind != "inferred" {
		t.Fatalf("kind = %q, err = %v", d.Kind, err)
	}
}

// ---------------------------------------------------------------- crawler (known bugs 4, 5)

var shop = map[string]testPage{
	"/": {200, "text/html", `<html><head><title>Corner Shop</title></head><body>
		<a href="/missing1">a</a> <a href="/missing2">b</a> <a href="/missing3">c</a>
		<a href="/notes.txt">notes</a> <a href="/logo.png">logo</a>
		<a href="/products/1">one</a> <a href="/products/2">two</a> <a href="/about#team">about</a>
		<a href="https://elsewhere.example/x">external</a>
		<form action="/search" method="get"><input name="q"><input name="q"><select name="sort"></select></form>
		<form action="/products/7/reviews" method="post"><input name="rating"><textarea name="body"></textarea></form>
		</body></html>`},
	"/notes.txt":  {200, "text/plain", "not html"},
	"/products/1": {200, "text/html", "<p>one</p>"},
	"/products/2": {200, "text/html", "<p>two</p>"},
	"/about":      {200, "text/html", "<p>about</p>"},
}

func TestFailedFetchesDoNotBurnThePageLimit(t *testing.T) {
	host, hits := site(t, shop)
	if _, err := Discover(host, Options{MaxPages: 3, Log: quiet}); err != nil {
		t.Fatal(err)
	}
	// 3 missing pages and a text file come first in BFS order; they must not count
	h := hits()
	if !hit(h, "/products/1") || !hit(h, "/products/2") {
		t.Errorf("product pages not reached: %q", h)
	}
	if hit(h, "/about") || hit(h, "/logo.png") {
		t.Errorf("crawled past the limit or fetched an asset: %q", h)
	}
}

func TestCrawledPathsAreNormalizedAndValid(t *testing.T) {
	host, hits := site(t, shop)
	d, err := Discover(host, Options{MaxPages: 10, Log: quiet})
	if err != nil || d.Kind != "inferred" {
		t.Fatalf("kind = %q, err = %v", d.Kind, err)
	}
	paths := d.Spec.Map("paths")
	if !paths.Has("/products/{id}") || paths.Has("/products/1") || paths.Has("/products/2") {
		t.Errorf("paths = %q", paths.Keys())
	}
	review := paths.Map("/products/{id}/reviews").Map("post")
	param := review.List("parameters")[0].(*ordered.Map)
	if param.String("name") != "id" || param.String("in") != "path" || param.Map("schema").String("type") != "integer" {
		t.Errorf("review param = %v", param)
	}
	var names []string
	for _, p := range paths.Map("/search").Map("get").List("parameters") {
		names = append(names, p.(*ordered.Map).String("name"))
	}
	if !reflect.DeepEqual(names, []string{"q", "sort"}) {
		t.Errorf("search params = %q", names)
	}
	if !paths.Has("/about") || !hit(hits(), "/about") {
		t.Error("link with #fragment was skipped instead of crawled")
	}
	if title := d.Spec.Map("info").String("title"); title != "Corner Shop" {
		t.Errorf("title = %q", title)
	}
	for _, p := range []string{"/products/1", "/products/2", "/search"} {
		if !hit(d.ObservedPaths, p) {
			t.Errorf("%s not in observed paths %q", p, d.ObservedPaths)
		}
	}
	validate(t, d.Spec)
}
