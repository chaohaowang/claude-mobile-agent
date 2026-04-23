package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: claude-mobile <daemon|status|sessions|version>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println("claude-mobile 0.0.1-dev")
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(2)
	}
}
