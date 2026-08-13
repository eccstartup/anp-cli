package main

import (
	"os"

	"github.com/eccstartup/anp-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
