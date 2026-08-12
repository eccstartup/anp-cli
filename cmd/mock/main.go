// Command mock starts an in-memory ANP backend for testing. It prints the
// base URL on stdout and stays running until interrupted.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ANPWorld/anp-cli/internal/testutil/mockbackend"
)

func main() {
	server := mockbackend.New()
	baseURL, closeFn, err := server.Start()
	if err != nil {
		panic(err)
	}
	defer closeFn()

	fmt.Fprintln(os.Stderr, "[mock] ANP backend listening on", baseURL)
	fmt.Println(baseURL)
	fmt.Fprintln(os.Stderr, "Press Ctrl-C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}
