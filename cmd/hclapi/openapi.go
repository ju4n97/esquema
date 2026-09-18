package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/ju4n97/hclapi"
)

func newOpenAPICommand() *cli.Command {
	return &cli.Command{
		Name:      "openapi",
		Aliases:   []string{"oas", "spec"},
		Usage:     "Export OpenAPI 3.1 specification for the compiled manifests",
		ArgsUsage: "[manifest paths or globs...]",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Manifest file, directory, or glob pattern",
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "Write output to a file instead of stdout",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "Output format (json, yaml)",
				Value:   "json",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			patterns := resolvePatterns(cmd)

			m, err := hclapi.Load(patterns...)
			if err != nil {
				return fmt.Errorf("compile manifests: %w", err)
			}

			spec, err := hclapi.CompileSpec(m)
			if err != nil {
				return fmt.Errorf("generate openapi specification: %w", err)
			}

			var outBytes []byte
			if strings.EqualFold(cmd.String("format"), "yaml") {
				outBytes = spec.YAML
			} else {
				outBytes = spec.JSON
			}

			targetFile := cmd.String("output")
			if targetFile == "" {
				_, err = os.Stdout.Write(outBytes)
				return err
			}

			if err := os.WriteFile(targetFile, outBytes, 0o600); err != nil {
				return fmt.Errorf("write specification file %q: %w", targetFile, err)
			}

			fmt.Fprintf(os.Stderr, "OpenAPI 3.1 specification written to %s (ETag: %s)\n", targetFile, spec.JSONETag)
			return nil
		},
	}
}
