package main

import (
	"os"

	"github.com/ANPWorld/anp-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
