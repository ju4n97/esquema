package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/ju4n97/hclapi"
)

// newLintCommand validates and type-checks manifests without booting listeners or pools.

func newLintCommand() *cli.Command {
	return &cli.Command{
		Name:      "lint",
		Aliases:   []string{"check", "validate"},
		Usage:     "Validate manifest schemas, types, and referential integrity",
		ArgsUsage: "[manifest paths or globs...]",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Manifest file, directory, or glob pattern",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			patterns := resolvePatterns(cmd)

			m, err := hclapi.Load(patterns...)
			if err != nil {
				return fmt.Errorf("validation error:\n%w", err)
			}

			fmt.Fprintf(os.Stdout, "✓ Manifests are valid (%d routes, %d connections, %d schemas compiled)\n",
				len(m.Routes), len(m.Connections), len(m.Schemas))
			return nil
		},
	}
}
