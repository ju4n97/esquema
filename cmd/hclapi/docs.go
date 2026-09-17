//go:build docs

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	docs "github.com/urfave/cli-docs/v3"
	"github.com/urfave/cli/v3"
)

const docsOutputDir = "./docs/content/cli"

func main() {
	if err := generateDocs(newRootCommand()); err != nil {
		fmt.Fprintf(os.Stderr, "error generating documentation: %v\n", err)
		os.Exit(1)
	}
}

func generateDocs(root *cli.Command) error {
	if err := os.RemoveAll(docsOutputDir); err != nil {
		return fmt.Errorf("remove old docs: %w", err)
	}
	if err := os.MkdirAll(docsOutputDir, 0o755); err != nil {
		return fmt.Errorf("create docs directory: %w", err)
	}

	return writeCommandDocs(root, nil)
}

func writeCommandDocs(cmd *cli.Command, path []string) error {
	if cmd.Hidden {
		return nil
	}

	currPath := append(append([]string(nil), path...), cmd.Name)
	name := strings.Join(currPath, " ")

	md, err := docs.ToMarkdown(cmd)
	if err != nil {
		return fmt.Errorf("generate markdown for %q: %w", name, err)
	}

	content := fmt.Sprintf(
		"---\ntitle: %s\n---\n\n<!-- Generated automatically by hclapi docs. Do not edit directly. -->\n\n%s\n",
		name,
		md,
	)

	fileName := "hclapi.md"
	if len(currPath) > 1 {
		fileName = "hclapi-" + strings.Join(currPath[1:], "-") + ".md"
	}

	filePath := filepath.Join(docsOutputDir, fileName)
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filePath, err)
	}

	for _, sub := range cmd.Commands {
		if err := writeCommandDocs(sub, currPath); err != nil {
			return err
		}
	}

	return nil
}
