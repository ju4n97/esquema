package manifest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ju4n97/hclapi/internal/manifest"
)

// TestDiscoverFiles verifies file globbing, directory traversal, .hclapiignore filtering, and deterministic sorting.
func TestDiscoverFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	fileTree := map[string]string{
		".hclapiignore":            "scratch/\nlocal.hcl\nexperimental/*\n!experimental/preview.hcl\n",
		"routes/users.hcl":         `route "GET /users" {}`,
		"routes/auth.hcl":          `route "POST /auth" {}`,
		"routes/local.hcl":         `route "GET /local" {}`,
		"scratch/temp.hcl":         `route "GET /scratch" {}`,
		"experimental/draft.hcl":   `route "GET /draft" {}`,
		"experimental/preview.hcl": `route "GET /preview" {}`,
		".hidden/secret.hcl":       `route "GET /hidden" {}`,
	}

	for relPath, content := range fileTree {
		absPath := filepath.Join(tempDir, relPath)
		err := os.MkdirAll(filepath.Dir(absPath), 0o750)
		if err != nil {
			t.Fatalf("failed to create directory structure: %v", err)
		}
		err = os.WriteFile(absPath, []byte(content), 0o600)
		if err != nil {
			t.Fatalf("failed to write test file %q: %v", relPath, err)
		}
	}

	t.Run("resolves globs and enforces ignore rules with negation", func(t *testing.T) {
		t.Parallel()

		discovered, err := manifest.DiscoverFiles(tempDir)
		if err != nil {
			t.Fatalf("unexpected discovery error: %v", err)
		}

		var relPaths []string
		for _, f := range discovered {
			rel, relErr := filepath.Rel(tempDir, f)
			if relErr != nil {
				t.Fatalf("failed to resolve relative path: %v", relErr)
			}
			relPaths = append(relPaths, filepath.ToSlash(rel))
		}

		expected := []string{
			"experimental/preview.hcl",
			"routes/auth.hcl",
			"routes/users.hcl",
		}

		if !reflect.DeepEqual(relPaths, expected) {
			t.Errorf("discovered = %v; want %v", relPaths, expected)
		}
	})
}
