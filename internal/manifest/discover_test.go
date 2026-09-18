package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLoadIgnoreRules verifies parsing of comments, negation, and directory markers.
func TestLoadIgnoreRules(t *testing.T) {
	t.Parallel()

	t.Run("returns empty ruleset when file does not exist", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		rules, err := LoadIgnoreRules(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rules == nil || len(rules.rules) != 0 {
			t.Fatalf("expected empty ruleset, got %+v", rules)
		}
	})

	t.Run("parses ignore file correctly", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".hclapiignore"), []byte(`
			# Comment line
			scratch/
			local.hcl
			!important.hcl
			routes/drafts/*
		`), 0o600); err != nil {
			t.Fatalf("failed to write ignore file: %v", err)
		}

		rules, err := LoadIgnoreRules(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(rules.rules) != 4 {
			t.Fatalf("expected 4 rules, got %d", len(rules.rules))
		}

		expected := []ignoreRule{
			{pattern: "scratch", dirOnly: true, negated: false, anchored: false},
			{pattern: "local.hcl", dirOnly: false, negated: false, anchored: false},
			{pattern: "important.hcl", dirOnly: false, negated: true, anchored: false},
			{pattern: "routes/drafts/*", dirOnly: false, negated: false, anchored: true},
		}

		for i, exp := range expected {
			got := rules.rules[i]
			if got != exp {
				t.Errorf("rule[%d] = %+v, want %+v", i, got, exp)
			}
		}
	})
}

// TestIgnoreRules_Matches verifies pattern matching, recursion, anchored paths, and negation.
func TestIgnoreRules_Matches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rules      []ignoreRule
		path       string
		isDir      bool
		wantIgnore bool
	}{
		{
			name:       "nil receiver returns false",
			rules:      nil,
			path:       "any.hcl",
			isDir:      false,
			wantIgnore: false,
		},
		{
			name: "unanchored basename matches root file",
			rules: []ignoreRule{
				{pattern: "local.hcl"},
			},
			path:       "local.hcl",
			isDir:      false,
			wantIgnore: true,
		},
		{
			name: "unanchored basename matches nested file",
			rules: []ignoreRule{
				{pattern: "local.hcl"},
			},
			path:       "routes/sub/local.hcl",
			isDir:      false,
			wantIgnore: true,
		},
		{
			name: "directory-only rule ignores directory",
			rules: []ignoreRule{
				{pattern: "temp", dirOnly: true},
			},
			path:       "temp",
			isDir:      true,
			wantIgnore: true,
		},
		{
			name: "directory-only rule does not ignore file with same name",
			rules: []ignoreRule{
				{pattern: "temp", dirOnly: true},
			},
			path:       "temp",
			isDir:      false,
			wantIgnore: false,
		},
		{
			name: "anchored pattern matches relative root",
			rules: []ignoreRule{
				{pattern: "routes/draft.hcl", anchored: true},
			},
			path:       "routes/draft.hcl",
			isDir:      false,
			wantIgnore: true,
		},
		{
			name: "anchored pattern does not match deeper hierarchy",
			rules: []ignoreRule{
				{pattern: "routes/draft.hcl", anchored: true},
			},
			path:       "v2/routes/draft.hcl",
			isDir:      false,
			wantIgnore: false,
		},
		{
			name: "negation un-ignores previously matched file",
			rules: []ignoreRule{
				{pattern: "drafts/*", anchored: true},
				{pattern: "drafts/preview.hcl", negated: true, anchored: true},
			},
			path:       "drafts/preview.hcl",
			isDir:      false,
			wantIgnore: false,
		},
		{
			name: "non-negated file remains ignored",
			rules: []ignoreRule{
				{pattern: "drafts/*", anchored: true},
				{pattern: "drafts/preview.hcl", negated: true, anchored: true},
			},
			path:       "drafts/other.hcl",
			isDir:      false,
			wantIgnore: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ir := &IgnoreRules{rules: tt.rules}
			if got := ir.Matches(tt.path, tt.isDir); got != tt.wantIgnore {
				t.Fatalf("Matches(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.wantIgnore)
			}
		})
	}
}

// TestDiscoverFiles verifies file discovery, glob expansion, ignore filtering, and sorting.
func TestDiscoverFiles(t *testing.T) {
	t.Parallel()

	t.Run("empty patterns returns nil", func(t *testing.T) {
		t.Parallel()

		got, err := DiscoverFiles()
		if err != nil || got != nil {
			t.Fatalf("expected (nil, nil), got (%v, %v)", got, err)
		}
	})

	t.Run("non-existent file returns error", func(t *testing.T) {
		t.Parallel()

		_, err := DiscoverFiles("non_existent_manifest.hcl")
		if err == nil {
			t.Fatal("expected error for non-existent file, got nil")
		}
	})

	t.Run("discovers single direct file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := filepath.Join(dir, "app.hcl")
		if err := os.WriteFile(filePath, []byte(`server {}`), 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		got, err := DiscoverFiles(filePath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := []string{filePath}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("DiscoverFiles() = %v, want %v", got, expected)
		}
	})

	t.Run("traverses directory applying ignore rules, hidden file pruning, and sorting", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		files := map[string]string{
			".hclapiignore": `
				scratch/
				local.hcl
				drafts/*
				!drafts/preview.hcl
			`,
			"server.hcl":         `server {}`,
			"local.hcl":          `server {}`,
			"scratch/temp.hcl":   `route "GET /temp" {}`,
			"drafts/wip.hcl":     `route "GET /wip" {}`,
			"drafts/preview.hcl": `route "GET /preview" {}`,
			".hidden/secret.hcl": `route "GET /secret" {}`,
			"routes/users.hcl":   `route "GET /users" {}`,
			"routes/auth.hcl":    `route "POST /auth" {}`,
			"routes/notes.txt":   `not an hcl file`,
		}

		for rel, content := range files {
			abs := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
				t.Fatalf("failed to create directory: %v", err)
			}
			if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
				t.Fatalf("failed to write file %q: %v", rel, err)
			}
		}

		got, err := DiscoverFiles(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var rels []string
		for _, f := range got {
			r, err := filepath.Rel(dir, f)
			if err != nil {
				t.Fatalf("failed to resolve relative path: %v", err)
			}
			rels = append(rels, filepath.ToSlash(r))
		}

		expected := []string{
			"drafts/preview.hcl",
			"routes/auth.hcl",
			"routes/users.hcl",
			"server.hcl",
		}

		if !reflect.DeepEqual(rels, expected) {
			t.Fatalf("discovered files mismatch:\ngot:  %v\nwant: %v", rels, expected)
		}
	})

	t.Run("resolves glob patterns and deduplicates overlapping targets", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		file1 := filepath.Join(dir, "a.hcl")
		file2 := filepath.Join(dir, "b.hcl")
		if err := os.WriteFile(file1, []byte(`server {}`), 0o600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
		if err := os.WriteFile(file2, []byte(`server {}`), 0o600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		glob := filepath.Join(dir, "*.hcl")
		got, err := DiscoverFiles(glob, file1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := []string{file1, file2}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("DiscoverFiles() = %v, want %v", got, expected)
		}
	})
}
