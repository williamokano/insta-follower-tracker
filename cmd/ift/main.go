// Command ift runs the Instagram follower tracker service.
package main

import (
	"fmt"
	"os"
)

// version is overridden at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ift:", err)
		os.Exit(1)
	}
}

func run() error {
	return nil
}
