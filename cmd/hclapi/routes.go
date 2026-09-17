package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/ju4n97/hclapi"
)

// newRoutesCommand prints an inspection table of all compiled endpoints and step pipelines.
func newRoutesCommand() *cli.Command {
	return &cli.Command{
		Name:      "routes",
		Usage:     "List all compiled endpoints and pipeline step sequences",
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

			cfg, err := hclapi.Load(patterns...)
			if err != nil {
				return err
			}

			if len(cfg.Endpoints) == 0 {
				fmt.Println("No routes compiled.")
				return nil
			}

			fmt.Fprintf(os.Stdout, "\nCompiled Routes (%d total):\n\n", len(cfg.Endpoints))

			for _, ep := range cfg.Endpoints {
				var stepNames []string
				for _, s := range ep.Pipeline {
					name := s.Name
					if name == "" {
						name = string(s.Type)
					} else {
						name = fmt.Sprintf("%s(%s)", s.Type, name)
					}
					stepNames = append(stepNames, name)
				}

				chain := strings.Join(stepNames, " → ")
				if chain == "" {
					chain = "(empty pipeline)"
				}

				fmt.Fprintf(os.Stdout, "  %-7s %-35s  %s\n", ep.Method, ep.Path, chain)
			}
			fmt.Println()
			return nil
		},
	}
}
