package step0

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rishab2104/OpenAPI-Generator/internal/ordered"
)

// Discovery is step 0's outcome.
type Discovery struct {
	Kind          string // "published", "inferred" or "none"
	Spec          *ordered.Map
	SourceURL     string // where a published spec was found
	Version       string // what was published, e.g. "swagger 2.0"
	ObservedPaths []string
}

// Options for Discover. Zero values mean: default client, 40 pages, crawl on, print to stdout.
type Options struct {
	Client   *http.Client
	MaxPages int
	NoCrawl  bool
	Log      func(string)
}

// Discover finds the spec a backend already publishes, else crawls its pages into a draft.
func Discover(target string, opts Options) (Discovery, error) {
	base, err := NormalizeURL(target)
	if err != nil {
		return Discovery{}, err
	}
	if opts.Client == nil {
		opts.Client = NewClient()
	}
	if opts.MaxPages <= 0 {
		opts.MaxPages = 40
	}
	if opts.Log == nil {
		opts.Log = func(s string) { fmt.Println(s) }
	}
	opts.Log("[*] Target: " + base)
	if found := FindSpec(base, opts.Client, opts.Log); found != nil {
		return Discovery{Kind: "published", Spec: found.Spec, SourceURL: found.URL, Version: found.Version}, nil
	}
	if opts.NoCrawl {
		return Discovery{Kind: "none"}, nil
	}
	opts.Log("[*] No published spec found; checking public pages...")
	crawl := Crawl(base, opts.Client, opts.MaxPages, 0)
	opts.Log(fmt.Sprintf("[*] Crawled %d page(s) with %d request(s).", crawl.Pages, crawl.Requests))
	if crawl.Spec.Map("paths").Len() == 0 {
		return Discovery{Kind: "none", ObservedPaths: crawl.ObservedPaths}, nil
	}
	return Discovery{Kind: "inferred", Spec: crawl.Spec, ObservedPaths: crawl.ObservedPaths}, nil
}

// WriteYAML writes a document, creating parent directories.
func WriteYAML(doc any, path string) error {
	data, err := ordered.YAML(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Report writes step 0's outputs and returns the exit code: 0 if a spec was written.
func Report(d Discovery, output, routesOut string, log func(string)) (int, error) {
	if routesOut != "" && len(d.ObservedPaths) > 0 {
		if err := os.MkdirAll(filepath.Dir(routesOut), 0o755); err != nil {
			return 1, err
		}
		if err := os.WriteFile(routesOut, []byte(strings.Join(d.ObservedPaths, "\n")+"\n"), 0o644); err != nil {
			return 1, err
		}
		log("[+] Crawled paths for the step-1 wordlist: " + routesOut)
	}
	switch d.Kind {
	case "none":
		log("[-] No published spec and no public routes found; no file written.")
		return 1, nil
	case "published":
		log(fmt.Sprintf("[+] Published spec (%s) is authoritative; use it as the deliverable / ground truth.", d.Version))
	default:
		log("[!] This is an inferred draft. Review it before use.")
	}
	if err := WriteYAML(d.Spec, output); err != nil {
		return 1, err
	}
	version, _ := d.Spec.Get("openapi")
	log(fmt.Sprintf("[+] OpenAPI version: %v", version))
	log(fmt.Sprintf("[+] Paths: %d", d.Spec.Map("paths").Len()))
	log("[+] Saved: " + output)
	return 0, nil
}
