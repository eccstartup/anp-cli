package buildinfo

import "runtime"

// Version is the semantic version of the CLI. It may be overridden at build
// time via -ldflags "-X github.com/ANPWorld/anp-cli/internal/buildinfo.Version=...".
var Version = "0.1.0"

// Commit is the git commit hash, injected at build time.
var Commit = ""

// Date is the build timestamp, injected at build time.
var Date = ""

// BuildInfo captures the build-time metadata surfaced by `anp-cli version`.
type BuildInfo struct {
	CLI      string `json:"cli"`
	Version  string `json:"version"`
	Commit   string `json:"commit,omitempty"`
	Date     string `json:"date,omitempty"`
	Go       string `json:"go"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CGO      bool   `json:"cgo"`
	SDK      string `json:"sdk"`
	SDKVer   string `json:"sdk_version"`
	Protocol string `json:"protocol"`
}

// Current returns the build metadata for the running binary.
func Current() BuildInfo {
	return BuildInfo{
		CLI:      "anp-cli",
		Version:  Version,
		Commit:   Commit,
		Date:     Date,
		Go:       runtime.Version(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		CGO:      cgoEnabled(),
		SDK:      "github.com/agent-network-protocol/anp/golang",
		SDKVer:   "v0.9.2",
		Protocol: "anp-jsonrpc-v1",
	}
}

func cgoEnabled() bool {
	return false
}
