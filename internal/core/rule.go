package core

// Rule is one framework's knowledge. A rule set lives in internal/rules/<framework>.go
// and registers itself from init(); adding a framework means adding that one file.
type Rule interface {
	Name() string
	Language() string
	// Detect is a cheap check: does this project use the framework at all?
	Detect(p *Project) bool
	// Scan extracts every (method, path) the framework would serve.
	Scan(p *Project) ScanResult
}
