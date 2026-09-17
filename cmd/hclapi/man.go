//go:build man

package main

import (
	"fmt"
	"os"

	docs "github.com/urfave/cli-docs/v3"
)

func main() {
	cliApp := newRootCommand()

	manPage, err := docs.ToMan(cliApp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate man page: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll("./man", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create man directory: %v\n", err)
		os.Exit(1)
	}

	file, err := os.Create("./man/hclapi.1")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create ./man/hclapi.1: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	if _, err := file.WriteString(manPage); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write man page: %v\n", err)
		os.Exit(1)
	}
}
