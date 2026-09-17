//go:build !docs && !man

package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	ctx := context.Background()

	if err := newRootCommand().Run(ctx, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
