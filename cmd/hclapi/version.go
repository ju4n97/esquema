package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/ju4n97/hclapi/internal/version"
)

// newVersionCommand outputs build and VCS metadata.
func newVersionCommand() *cli.Command {
	return &cli.Command{
		Name:    "version",
		Aliases: []string{"v"},
		Usage:   "Print version and build metadata",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "short",
				Aliases: []string{"s"},
				Usage:   "Print only the semantic version string",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("short") {
				fmt.Println(version.GetVersion())
				return nil
			}
			fmt.Println(version.String())
			return nil
		},
	}
}
