// Command mock runs an in-memory ANP backend for local development and manual
// testing. It prints the base URL and stays running until interrupted.
//
// Usage: go run ./cmd/mock            # prints e.g. http://127.0.0.1:54321
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
	baseURL, _, err := server.Start()
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, "ANP mock backend listening on", baseURL)
	fmt.Println(baseURL)
	fmt.Fprintln(os.Stderr, "Press Ctrl-C to stop")
	signal.Notify(make(chan os.Signal, 1), os.Interrupt, syscall.SIGTERM)
	select {}
}
