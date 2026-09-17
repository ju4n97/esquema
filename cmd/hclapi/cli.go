package main

import (
	"github.com/urfave/cli/v3"

	"github.com/ju4n97/hclapi/internal/version"
)

// newRootCommand initializes the top-level hclapi CLI application.
func newRootCommand() *cli.Command {
	return &cli.Command{
		Name:                  "hclapi",
		Usage:                 "Type-safe, declarative API runtime powered by HCL.",
		Version:               version.GetVersion(),
		Suggest:               true,
		EnableShellCompletion: true,
		Commands: []*cli.Command{
			newServeCommand(),
			newOpenAPICommand(),
			newLintCommand(),
			newRoutesCommand(),
			newVersionCommand(),
		},
	}
}

// resolvePatterns extracts manifest targets from positional arguments or the --config flags.
// If neither is provided, it defaults to the current working directory (".").
func resolvePatterns(cmd *cli.Command) []string {
	if cmd.Args().Len() > 0 {
		return cmd.Args().Slice()
	}
	if configs := cmd.StringSlice("config"); len(configs) > 0 {
		return configs
	}
	return []string{"."}
}
