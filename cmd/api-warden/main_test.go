package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func runCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String() + errOut.String()
}

func TestScanWritesWordlistAndRecord(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "crawled_routes.txt")
	os.WriteFile(extra, []byte("/health\n/products/7\n"), 0o644)
	out := filepath.Join(dir, "out")
	// flags after the positional argument work too
	code, output := runCLI(t, "scan", "../../testdata/express_app", "-o", out, "--extra-routes", extra)
	if code != 0 || !strings.Contains(output, "15 routes") {
		t.Fatalf("code %d:\n%s", code, output)
	}
	data, _ := os.ReadFile(filepath.Join(out, "routes.txt"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !contains(lines, "/api/users/1/avatar") || !contains(lines, "/api/users/search/test") {
		t.Errorf("routes.txt = %q", lines)
	}
	if strings.Count(string(data), "/health\n") != 1 || lines[len(lines)-1] != "/products/7" { // merged, de-duplicated
		t.Errorf("extras not merged: %q", lines)
	}
	var record struct {
		Frameworks []string
		Routes     []struct {
			Method, Path string
			SamplePath   string `json:"sample_path"`
			Params       []map[string]any
		}
	}
	data, _ = os.ReadFile(filepath.Join(out, "routes.json"))
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	for _, r := range record.Routes {
		if r.Method == "DELETE" {
			if r.Path != "/api/users/{userId}" || r.SamplePath != "/api/users/1" ||
				r.Params[0]["schema"].(map[string]any)["type"] != "integer" || r.Params[0]["required"] != true {
				t.Errorf("delete = %+v", r)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestScanForcedFrameworkAndNothingFound(t *testing.T) {
	if code, out := runCLI(t, "scan", "../../testdata/flask_app", "-f", "express", "-o", t.TempDir(), "-q"); code != 1 {
		t.Errorf("code %d:\n%s", code, out)
	}
	if code, _ := runCLI(t, "scan", "../../testdata/flask_app", "-f", "rails"); code != 2 {
		t.Errorf("unknown framework accepted")
	}
}

func TestSpecWritesPublishedSpec(t *testing.T) {
	doc, _ := os.ReadFile("../../testdata/swagger2_petstore.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/swagger.json" {
			http.NotFound(w, r)
			return
		}
		w.Write(doc)
	}))
	defer srv.Close()
	dir := t.TempDir()
	if code, out := runCLI(t, "spec", strings.TrimPrefix(srv.URL, "http://"), "-o", dir); code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "step0_spec.yaml"))
	var spec struct {
		OpenAPI string                    `yaml:"openapi"`
		Paths   map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI != "3.0.3" || spec.Paths["/pets/{petId}"] == nil {
		t.Errorf("spec = %+v", spec)
	}
	responses := spec.Paths["/pets"]["get"].(map[string]any)["responses"].(map[string]any)
	if _, ok := responses["200"]; !ok { // a string key, not the int 200
		t.Errorf("response codes = %v", responses)
	}
}

func TestSpecNothingFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	dir := t.TempDir()
	if code, _ := runCLI(t, "spec", srv.URL, "-o", dir, "--no-crawl"); code != 1 {
		t.Errorf("code %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "step0_spec.yaml")); err == nil {
		t.Error("spec written although nothing was found")
	}
}

func TestRulesListsRuleSets(t *testing.T) {
	code, out := runCLI(t, "rules")
	if code != 0 || !strings.Contains(out, "express") || !strings.Contains(out, "flask") {
		t.Errorf("code %d:\n%s", code, out)
	}
}
