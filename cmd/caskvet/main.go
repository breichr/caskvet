// Command caskvet is a read-only supply-chain health report for installed
// Homebrew casks. See AGENTS.md for the product contract.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "caskvet: not implemented yet (scaffold stage)")
	os.Exit(1)
}
