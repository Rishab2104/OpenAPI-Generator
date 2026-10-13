// Package step0 uses the backend's own published spec when it has one (the
// original apigen.py, fixed): probe the well-known spec locations, convert
// Swagger 2 to OpenAPI 3, and otherwise crawl public pages into a draft.
// Everything here sends GET requests only.
package step0

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
)

var SpecPaths = []string{
	"/openapi.json", "/openapi.yaml", "/openapi.yml", "/swagger.json",
	"/swagger.yaml", "/swagger.yml", "/v3/api-docs", "/v2/api-docs",
	"/api-docs", "/api/v3/openapi.json", "/api-docs/openapi.json",
}

const maxBody = 20 << 20

// FoundSpec is a published spec, always as OpenAPI 3 (Swagger 2 is converted).
type FoundSpec struct {
	Spec    *ordered.Map
	URL     string // where it was found
	Version string // what was published, e.g. "openapi 3.0.1" or "swagger 2.0"
}

// IsLocalHost: localhost, *.localhost/.test/.local/.internal, single-label names
// (docker-compose services), loopback and private IPs.
func IsLocalHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" {
		return false
	}
	if host == "localhost" || (!strings.Contains(host, ".") && !strings.Contains(host, ":")) {
		return true
	}
	for _, suffix := range []string{".localhost", ".test", ".local", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

// NormalizeURL adds a scheme if missing: http:// for local backends (they rarely
// have TLS), https:// otherwise. Trailing slashes are dropped.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		scheme := "https://"
		if u, err := url.Parse("//" + raw); err == nil && IsLocalHost(u.Hostname()) {
			scheme = "http://"
		}
		raw = scheme + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("please provide a valid URL, e.g. https://example.com or localhost:3000")
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/"), nil
}

// ValidSpec accepts OpenAPI 3.x and Swagger 2.0 documents (also with unquoted YAML versions).
func ValidSpec(doc any) bool {
	m, ok := doc.(*ordered.Map)
	if !ok {
		return false
	}
	if paths, ok := m.Get("paths"); ok && paths != nil {
		if _, ok := paths.(*ordered.Map); !ok {
			return false
		}
	}
	if v, ok := m.Get("openapi"); ok {
		return strings.HasPrefix(fmt.Sprint(v), "3")
	}
	v, _ := m.Get("swagger")
	s := fmt.Sprint(v)
	return s == "2.0" || s == "2"
}

// ParseDocument reads JSON or YAML; HTML pages (an SPA catch-all) and garbage give nil.
func ParseDocument(text string) any {
	text = strings.TrimSpace(strings.TrimPrefix(text, string(rune(0xFEFF))))
	if text == "" || strings.HasPrefix(text, "<") {
		return nil
	}
	doc, err := ordered.Parse([]byte(text))
	if err != nil {
		return nil
	}
	return doc
}

// NewClient is an HTTP client for probing a backend.
func NewClient() *http.Client { return &http.Client{Timeout: 12 * time.Second} }

func get(client *http.Client, target string, headers map[string]string) (*http.Response, string, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "API-Warden/"+core.Version)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return resp, string(body), err
}

type candidate struct {
	url     string
	headers map[string]string
}

func candidates(target string) []candidate {
	u, _ := url.Parse(target)
	root := u.Scheme + "://" + u.Host
	var out []candidate
	host := strings.ToLower(u.Host)
	if host == "app.swaggerhub.com" || host == "swaggerhub.com" {
		var parts []string
		for _, p := range strings.Split(u.Path, "/") {
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) >= 4 && parts[0] == "apis" {
			api := "https://api.swaggerhub.com/apis/" + url.PathEscape(parts[1]) + "/" +
				url.PathEscape(parts[2]) + "/" + url.PathEscape(parts[3])
			out = append(out, candidate{api, map[string]string{"Accept": "application/yaml"}},
				candidate{api + "/swagger.yaml", nil}, candidate{api + "/swagger.json", nil})
		}
	}
	if u.Path != "" && u.Path != "/" { // the API may live under a sub-path: /api/openapi.json
		for _, p := range SpecPaths {
			out = append(out, candidate{strings.TrimRight(target, "/") + p, nil})
		}
	}
	for _, p := range SpecPaths {
		out = append(out, candidate{root + p, nil})
	}
	return out
}

func unreachable(err error) bool {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	return (errors.As(err, &opErr) && opErr.Op == "dial") || errors.As(err, &dnsErr)
}

// FindSpec probes the usual spec locations and returns the first valid spec.
func FindSpec(target string, client *http.Client, log func(string)) *FoundSpec {
	if u, _ := url.Parse(target); u != nil && strings.Contains(strings.ToLower(u.Host), "swaggerhub.com") {
		log("[*] SwaggerHub URL detected.")
	}
	seen, dead := map[string]bool{}, map[string]bool{}
	for _, c := range candidates(target) {
		u, _ := url.Parse(c.url)
		if seen[c.url] || dead[u.Host] {
			continue
		}
		seen[c.url] = true
		resp, body, err := get(client, c.url, c.headers)
		if err != nil {
			if unreachable(err) {
				log(fmt.Sprintf("[!] Could not connect to %s", u.Host))
				dead[u.Host] = true // don't retry every path on a dead host
			} else {
				log(fmt.Sprintf("[!] Could not read %s: %v", c.url, err))
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			continue
		}
		doc := ParseDocument(body)
		if !ValidSpec(doc) {
			continue
		}
		found := resp.Request.URL.String()
		log("[+] Found API specification: " + found)
		m := doc.(*ordered.Map)
		version := "swagger 2.0"
		if v, ok := m.Get("openapi"); ok {
			version = fmt.Sprintf("openapi %v", v)
		}
		return &FoundSpec{Spec: ConvertSwagger2(m, found), URL: found, Version: version}
	}
	return nil
}
