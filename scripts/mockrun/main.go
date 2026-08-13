// mockrun starts an in-memory ANP backend for smoke testing.
// It prints the base URL on stdout and stays running until killed.
//
// Usage: go run ./scripts/mockrun
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/eccstartup/anp-cli/internal/testutil/mockbackend"
)

func main() {
	s := mockbackend.New()
	baseURL, closeFn, err := s.Start()
	if err != nil {
		panic(err)
	}
	defer closeFn()

	fmt.Fprintln(os.Stderr, "[mock] listening on", baseURL)
	fmt.Println(baseURL)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}
