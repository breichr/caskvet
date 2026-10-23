// Command caskvet is a read-only supply-chain health report for
// installed Homebrew casks. See AGENTS.md for the product contract.
package main

import (
	"os"

	"github.com/breichr/caskvet/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Options{}))
}
