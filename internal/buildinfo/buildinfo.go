// Package buildinfo holds version data injected at build time with -ldflags.
package buildinfo

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
