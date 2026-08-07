// Package doctor performs baseline environment and workspace diagnostics.
package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/ANPWorld/anp-cli/internal/buildinfo"
	"github.com/ANPWorld/anp-cli/internal/config"
	"github.com/ANPWorld/anp-cli/internal/store"
)

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok | warn | error
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

type Report struct {
	Summary string  `json:"summary"`
	Checks  []Check `json:"checks"`
}

func Run(resolved *config.Resolved) Report {
	checks := []Check{}
	failed := 0

	// build
	info := buildinfo.Current()
	checks = append(checks, Check{Name: "build", Status: "ok", Message: "anp-cli " + info.Version + " on " + runtime.GOOS + "/" + runtime.GOARCH})

	// config file
	if resolved.ConfigExists {
		checks = append(checks, Check{Name: "config", Status: "ok", Message: resolved.Paths.ConfigFile})
	} else {
		checks = append(checks, Check{Name: "config", Status: "warn", Message: "config.yaml not found", Hint: "Run `anp-cli init` to create the workspace."})
	}
	if resolved.ConfigError != "" {
		checks = append(checks, Check{Name: "config", Status: "error", Message: resolved.ConfigError, Hint: "Fix config.yaml or set ANP_WORKSPACE."})
		failed++
	}

	// backend
	if resolved.Backend == "" {
		checks = append(checks, Check{Name: "backend", Status: "warn", Message: "no backend configured", Hint: "Set ANP_BACKEND or run `anp-cli config set --backend <url>`."})
	} else {
		checks = append(checks, Check{Name: "backend", Status: "ok", Message: resolved.Backend})
	}

	// did domain
	if resolved.DidDomain == "" {
		checks = append(checks, Check{Name: "did_domain", Status: "warn", Message: "no did_domain configured; generated DIDs default to the backend host"})
	} else {
		checks = append(checks, Check{Name: "did_domain", Status: "ok", Message: resolved.DidDomain})
	}

	// identity store
	if entries, err := os.ReadDir(resolved.Paths.IdentityDir); err == nil {
		count := 0
		for _, entry := range entries {
			if entry.IsDir() {
				count++
			}
		}
		checks = append(checks, Check{Name: "identity_store", Status: "ok", Message: filepath.Join(resolved.Paths.IdentityDir) + " (" + intToString(count) + " identities)"})
	} else if os.IsNotExist(err) {
		checks = append(checks, Check{Name: "identity_store", Status: "warn", Message: "not initialized", Hint: "Run `anp-cli init`."})
	} else {
		checks = append(checks, Check{Name: "identity_store", Status: "error", Message: err.Error()})
		failed++
	}

	// database
	if _, err := os.Stat(resolved.Paths.DatabaseFile); err == nil {
		if db, err := store.Open(resolved.Paths.DatabaseFile); err == nil {
			if version, err := store.CurrentSchemaVersion(db); err == nil {
				checks = append(checks, Check{Name: "database", Status: "ok", Message: "schema v" + intToString(version)})
			} else {
				checks = append(checks, Check{Name: "database", Status: "error", Message: err.Error()})
				failed++
			}
			db.Close()
		} else {
			checks = append(checks, Check{Name: "database", Status: "error", Message: err.Error()})
			failed++
		}
	} else {
		checks = append(checks, Check{Name: "database", Status: "ok", Message: "not yet created (created on first use)"})
	}

	summary := "All checks passed"
	if failed > 0 {
		summary = "Check " + intToString(failed) + " failed item(s)"
	} else if anyWarn(checks) {
		summary = "Passed with warnings"
	}
	return Report{Summary: summary, Checks: checks}
}

func anyWarn(checks []Check) bool {
	for _, check := range checks {
		if check.Status == "warn" {
			return true
		}
	}
	return false
}

func intToString(value int) string {
	return strconv.Itoa(value)
}
