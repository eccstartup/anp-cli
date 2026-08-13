package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Version is the semantic version of the CLI. It may be overridden at build
// time via -ldflags "-X github.com/eccstartup/anp-cli/internal/buildinfo.Version=...".
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
		SDKVer:   sdkVersion(),
		Protocol: "anp-jsonrpc-v1",
	}
}

// sdkVersion returns the compiled-in version of the ANP Go SDK from the
// runtime build info. This keeps `version` accurate across SDK upgrades
// instead of relying on a hardcoded constant that drifts out of sync.
func sdkVersion() string {
	const fallback = "v0.9.3"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return fallback
	}
	for _, dep := range bi.Deps {
		if dep.Path != "github.com/agent-network-protocol/anp/golang" {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		return dep.Version
	}
	return fallback
}

func cgoEnabled() bool {
	return false
}
