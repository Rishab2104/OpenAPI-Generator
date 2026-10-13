package step0

// Fallback when no spec is published: crawl same-site HTML pages and forms into
// a draft spec. GET-only. The observed paths also feed the step-1 wordlist.

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
)

var (
	assetPath   = regexp.MustCompile(`(?i)\.(css|js|mjs|map|png|jpe?g|gif|svg|ico|webp|avif|bmp|pdf|zip|gz|tgz|rar|7z|woff2?|ttf|otf|eot|mp3|mp4|webm|avi|mov)$`)
	formMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	spaces      = regexp.MustCompile(`\s+`)
	idWords     = regexp.MustCompile(`[A-Za-z0-9]+`)
)

type form struct {
	action, method string
	fields         []string
}

type page struct {
	title string
	links []string
	forms []*form
}

func parsePage(body string) page {
	var p page
	var current *form
	inTitle := false
	z := html.NewTokenizer(strings.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return p
		case html.TextToken:
			if inTitle && p.title == "" {
				p.title = strings.TrimSpace(spaces.ReplaceAllString(string(z.Text()), " "))
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "form":
				current = nil
			case "title":
				inTitle = false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[string(k)] = string(v)
			}
			switch tag := string(name); tag {
			case "title":
				inTitle = true
			case "a":
				if attrs["href"] != "" {
					p.links = append(p.links, attrs["href"])
				}
			case "form":
				method := strings.ToLower(attrs["method"])
				if method == "" {
					method = "get"
				}
				current = &form{action: attrs["action"], method: method}
				p.forms = append(p.forms, current)
			case "input", "select", "textarea":
				if current != nil && attrs["name"] != "" && !contains(current.fields, attrs["name"]) {
					current.fields = append(current.fields, attrs["name"])
				}
			}
		}
	}
}

// CrawlResult is the draft spec plus what the crawl touched.
type CrawlResult struct {
	Spec          *ordered.Map
	ObservedPaths []string // concrete paths: pages fetched + form targets
	Pages         int
	Requests      int
}

func operationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	words := idWords.FindAllString(path, -1)
	if len(words) == 0 {
		b.WriteString("Root")
	}
	for _, w := range words {
		b.WriteString(strings.ToUpper(w[:1]) + strings.ToLower(w[1:]))
	}
	return b.String()
}

// canonical is the de-duplication key: no fragment, and an empty path is "/".
func canonical(u *url.URL) string {
	c := *u
	c.Fragment = ""
	if c.Path == "" {
		c.Path = "/"
	}
	return c.String()
}

type specBuilder struct {
	paths    map[string]*ordered.Map
	usedIDs  map[string]bool
	observed []string
}

func (b *specBuilder) add(concrete, method string, op *ordered.Map, replace bool) {
	if !contains(b.observed, concrete) {
		b.observed = append(b.observed, concrete)
	}
	template, params := core.NormalizeConcrete(concrete) // /products/1 and /products/2 -> /products/{id}
	item := b.paths[template]
	if item == nil {
		item = ordered.New()
		b.paths[template] = item
	}
	if item.Has(method) && !replace {
		return
	}
	id := ""
	if existing := item.Map(method); existing != nil {
		id = existing.String("operationId")
	} else {
		id = operationID(method, template)
		for n := 2; b.usedIDs[id]; n++ {
			id = fmt.Sprintf("%s%d", operationID(method, template), n)
		}
		b.usedIDs[id] = true
	}
	var parameters []any
	for _, p := range params {
		parameters = append(parameters, ordered.Of("name", p.Name, "in", "path", "required", true, "schema", p.Schema()))
	}
	parameters = append(parameters, op.List("parameters")...)
	out := ordered.Of("operationId", id)
	for _, k := range op.Keys() {
		if k != "parameters" {
			v, _ := op.Get(k)
			out.Set(k, v)
		}
	}
	if len(parameters) > 0 {
		out.Set("parameters", parameters)
	}
	item.Set(method, out)
}

// Crawl walks up to limit HTML pages on the start URL's host, breadth first. Only
// successfully fetched HTML pages count against limit; 404s, off-site redirects and
// non-HTML responses don't use up slots. maxRequests bounds the total work.
func Crawl(start string, client *http.Client, limit, maxRequests int) CrawlResult {
	origin, _ := url.Parse(start)
	if maxRequests <= 0 {
		maxRequests = limit * 5
	}
	queue := []*url.URL{origin}
	seen := map[string]bool{canonical(origin): true}
	b := &specBuilder{paths: map[string]*ordered.Map{}, usedIDs: map[string]bool{}}
	title := origin.Host
	pages, requests := 0, 0
	for len(queue) > 0 && pages < limit && requests < maxRequests {
		target := queue[0]
		queue = queue[1:]
		requests++
		resp, body, err := get(client, target.String(), nil)
		if err != nil {
			continue
		}
		final := resp.Request.URL
		seen[canonical(final)] = true
		if final.Host != origin.Host || resp.StatusCode >= 400 ||
			!strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
			continue
		}
		pages++
		pg := parsePage(body)
		path := final.Path
		if path == "" {
			path = "/"
		}
		if pages == 1 && pg.title != "" {
			title = pg.title
			if len(title) > 120 {
				title = title[:120]
			}
		}
		b.add(path, "get", ordered.Of(
			"summary", "Observed public page: "+path,
			"description", "Inferred from a public HTML page; verify whether this is an API endpoint.",
			"responses", ordered.Of("200", ordered.Of("description", "Successful page response"))), false)
		for _, href := range pg.links {
			link, err := final.Parse(href)
			if err != nil {
				continue
			}
			link.Fragment = ""
			key := canonical(link)
			if (link.Scheme == "http" || link.Scheme == "https") && link.Host == origin.Host &&
				!assetPath.MatchString(link.Path) && !seen[key] {
				seen[key] = true
				queue = append(queue, link)
			}
		}
		for _, f := range pg.forms {
			action, err := final.Parse(f.action)
			if err != nil || action.Host != origin.Host || !formMethods[f.method] {
				continue
			}
			op := ordered.Of(
				"summary", fmt.Sprintf("Inferred %s form submission", strings.ToUpper(f.method)),
				"description", "Inferred from an HTML form; verify the real API route and schemas.",
				"responses", ordered.Of("200", ordered.Of("description", "Inferred success response; verify")))
			if f.method == "get" {
				var params []any
				for _, field := range f.fields {
					params = append(params, ordered.Of("name", field, "in", "query", "required", false,
						"schema", ordered.Of("type", "string")))
				}
				if len(params) > 0 {
					op.Set("parameters", params)
				}
			} else if len(f.fields) > 0 {
				props := ordered.New()
				for _, field := range f.fields {
					props.Set(field, ordered.Of("type", "string"))
				}
				op.Set("requestBody", ordered.Of("required", false, "content", ordered.Of(formURLEncoded,
					ordered.Of("schema", ordered.Of("type", "object", "properties", props)))))
			}
			actionPath := action.Path
			if actionPath == "" {
				actionPath = "/"
			}
			b.add(actionPath, f.method, op, true)
		}
	}
	templates := make([]string, 0, len(b.paths))
	for t := range b.paths {
		templates = append(templates, t)
	}
	sort.Strings(templates)
	paths := ordered.New()
	for _, t := range templates {
		paths.Set(t, b.paths[t])
	}
	spec := ordered.Of(
		"openapi", "3.0.3",
		"info", ordered.Of("title", title, "version", "1.0.0",
			"description", "Inferred draft from public pages/forms, not a verified API contract."),
		"servers", []any{ordered.Of("url", origin.Scheme+"://"+origin.Host)},
		"tags", []any{ordered.Of("name", "Discovered routes")},
		"paths", paths,
		"components", ordered.Of("schemas", ordered.New()))
	return CrawlResult{Spec: spec, ObservedPaths: b.observed, Pages: pages, Requests: requests}
}
