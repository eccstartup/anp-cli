//go:build !windows

package cli

import (
	"os"
	"syscall"
)

// shutdownSignals returns the signals that should stop the receiver loop.
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
