// Command api-warden generates an OpenAPI 3.0 spec for a backend, without AI.
//
//	api-warden spec  <url>   step 0: published spec? (else a crawled draft)
//	api-warden scan  <src>   step 1: routes from source -> routes.txt + routes.json
//	api-warden rules         list the framework rule sets
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rishab2104/OpenAPI-Generator/internal/core"
	"github.com/Rishab2104/OpenAPI-Generator/internal/rules"
	"github.com/Rishab2104/OpenAPI-Generator/internal/scan"
	"github.com/Rishab2104/OpenAPI-Generator/internal/step0"
)

const defaultOut = "warden-out"

const usage = `api-warden %s: deterministic OpenAPI generation (no AI)

usage:
  api-warden spec  <url>  [-o dir] [--max-pages N] [--no-crawl]
  api-warden scan  <src>  [-o dir] [-f framework]... [--include-tests] [--extra-routes file]... [-q]
  api-warden rules
  api-warden version
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// parse lets flags appear before or after the positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, core.Version)
		return 2
	}
	say := func(s string) { fmt.Fprintln(stdout, s) }
	fail := func(err error) int { fmt.Fprintln(stderr, "error:", err); return 2 }

	switch args[0] {
	case "spec":
		fs := flag.NewFlagSet("spec", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("o", defaultOut, "output directory")
		maxPages := fs.Int("max-pages", 40, "HTML pages to crawl if no spec is published")
		noCrawl := fs.Bool("no-crawl", false, "only look for a published spec")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return 2
		}
		if len(pos) != 1 {
			return fail(fmt.Errorf("spec takes one URL, e.g. api-warden spec localhost:3000"))
		}
		d, err := step0.Discover(pos[0], step0.Options{MaxPages: *maxPages, NoCrawl: *noCrawl, Log: say})
		if err != nil {
			return fail(err)
		}
		code, err := step0.Report(d, filepath.Join(*out, "step0_spec.yaml"), filepath.Join(*out, "crawled_routes.txt"), say)
		if err != nil {
			return fail(err)
		}
		return code

	case "scan":
		fs := flag.NewFlagSet("scan", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("o", defaultOut, "output directory")
		var frameworks, extraFiles multiFlag
		fs.Var(&frameworks, "f", "force a rule set (repeatable); default: auto-detect")
		fs.Var(&extraFiles, "extra-routes", "file of extra paths for routes.txt, e.g. warden-out/crawled_routes.txt")
		includeTests := fs.Bool("include-tests", false, "also scan test directories and test files")
		quiet := fs.Bool("q", false, "don't print the route table")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return 2
		}
		if len(pos) != 1 {
			return fail(fmt.Errorf("scan takes one source directory"))
		}
		var extra []string
		for _, f := range extraFiles {
			data, err := os.ReadFile(f)
			if err != nil {
				return fail(err)
			}
			for _, line := range strings.Split(string(data), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					extra = append(extra, line)
				}
			}
		}
		return runScan(pos[0], *out, frameworks, extra, *includeTests, *quiet, stdout, stderr)

	case "rules":
		for _, r := range rules.All() {
			fmt.Fprintf(stdout, "%-10s %s\n", r.Name(), r.Language())
		}
		return 0

	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "api-warden", core.Version)
		return 0

	case "help", "--help", "-h":
		fmt.Fprintf(stdout, usage, core.Version)
		return 0
	}
	fmt.Fprintf(stderr, usage, core.Version)
	return 2
}

func runScan(source, out string, frameworks, extra []string, includeTests, quiet bool, stdout, stderr io.Writer) int {
	result, err := scan.Run(source, frameworks, includeTests)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	abs, _ := filepath.Abs(source)
	fmt.Fprintln(stdout, "[*] Source:", abs)
	if len(result.Frameworks) == 0 {
		fmt.Fprintf(stdout, "[-] No supported framework detected. Use -f to force one (%s).\n",
			strings.Join(scan.Names(), ", "))
		return 1
	}
	parsers := map[string]int{}
	for _, r := range result.Routes {
		parsers[r.Parser]++
	}
	var counts []string
	for p, n := range parsers {
		counts = append(counts, fmt.Sprintf("%s: %d", p, n))
	}
	sort.Strings(counts)
	fmt.Fprintf(stdout, "[*] Frameworks: %s  (parsed with %s)\n", strings.Join(result.Frameworks, ", "), strings.Join(counts, ", "))
	if !quiet && len(result.Routes) > 0 {
		width := 4
		for _, r := range result.Routes {
			width = max(width, len(r.Path))
		}
		fmt.Fprintf(stdout, "\n  %-7s %-*s  SOURCE\n", "METHOD", width, "PATH")
		for _, r := range result.Routes {
			flag := ""
			if len(r.Notes) > 0 {
				flag = "  [!]"
			}
			fmt.Fprintf(stdout, "  %-7s %-*s  %s%s\n", r.Method, width, r.Path, r.Location(), flag)
		}
		fmt.Fprintln(stdout)
	}
	for _, w := range result.Warnings {
		fmt.Fprintln(stdout, "[!]", w)
	}
	txt, js, err := scan.WriteOutputs(result, source, out, extra)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	unique := len(scan.SamplePaths(result.Routes, extra))
	suffix := ""
	if len(extra) > 0 {
		suffix = fmt.Sprintf(" (incl. %d extra)", len(extra))
	}
	fmt.Fprintf(stdout, "[+] %d routes, %d unique paths to send%s\n", len(result.Routes), unique, suffix)
	fmt.Fprintf(stdout, "[+] Wrote %s and %s\n", txt, js)
	if len(result.Routes) == 0 {
		return 1
	}
	return 0
}
