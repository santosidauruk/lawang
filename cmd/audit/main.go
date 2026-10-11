package main

import (
	"fmt"
	"os"
)

// Checkpoint 5 user-owned runtime wiring starts here. Parse the required UUID
// before opening PostgreSQL, then run the bounded audit service. Successful JSON
// belongs on stdout; safe diagnostics belong on stderr.
func main() {
	fmt.Fprintln(os.Stderr, "audit: not implemented")
	os.Exit(1)
}
