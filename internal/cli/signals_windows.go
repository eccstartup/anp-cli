//go:build windows

package cli

import "os"

// shutdownSignals returns the signals that should stop the receiver loop.
// Windows has no SIGTERM; os.Interrupt covers Ctrl-C and service stop.
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
