// Package manifest discovers, parses, validates, and compiles HCL configuration files.
package manifest

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// IgnoreRules holds compiled ignore patterns loaded from a .hclapiignore file.
type IgnoreRules struct {
	patterns []string
}

// LoadIgnoreRules reads an optional .hclapiignore file located in the specified root path.
func LoadIgnoreRules(rootDir string) (*IgnoreRules, error) {
	ignorePath := filepath.Join(rootDir, ".hclapiignore")

	f, err := os.Open(ignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &IgnoreRules{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}

	return &IgnoreRules{patterns: patterns}, scanner.Err()
}

// Matches reports whether relPath (a file or directory) is excluded by the
// loaded ignore patterns, honoring negation. A pattern ending in "/" only
// matches directories; all other patterns can match either.
func (ir *IgnoreRules) Matches(relPath string, isDir bool) bool {
	relPath = filepath.ToSlash(relPath)
	baseName := filepath.Base(relPath)

	ignored := false
	for _, pat := range ir.patterns {
		negate := strings.HasPrefix(pat, "!")
		p := strings.TrimPrefix(pat, "!")

		dirOnly := strings.HasSuffix(p, "/")
		p = strings.TrimSuffix(p, "/")
		if dirOnly && !isDir {
			continue // a directory-only pattern never excludes a file
		}

		var matched bool
		if strings.Contains(p, "/") {
			// anchored: pattern spans multiple segments, so it's matched
			// against the full path relative to the ignore file
			matched, _ = doublestar.Match(p, relPath)
		} else {
			// unanchored: matches the basename anywhere in the tree
			matched, _ = doublestar.Match(p, baseName)
			if !matched {
				matched, _ = doublestar.Match("**/"+p, relPath)
			}
		}

		if matched {
			ignored = !negate
		}
	}
	return ignored
}

// DiscoverFiles resolves globs or walks directory paths, ignoring hidden files,
// applying .hclapiignore exclusion rules, and returning deterministically sorted file paths.
func DiscoverFiles(patterns ...string) ([]string, error) {
	var matchedFiles []string
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		if strings.ContainsAny(pattern, "*?[{") {
			matches, err := doublestar.FilepathGlob(pattern)
			if err != nil {
				return nil, err
			}
			for _, m := range matches {
				if !seen[m] {
					seen[m] = true
					matchedFiles = append(matchedFiles, m)
				}
			}
			continue
		}

		info, err := os.Stat(pattern)
		if err != nil {
			return nil, err
		}

		if !info.IsDir() {
			if !seen[pattern] {
				seen[pattern] = true
				matchedFiles = append(matchedFiles, pattern)
			}
			continue
		}

		rules, err := LoadIgnoreRules(pattern)
		if err != nil {
			return nil, err
		}

		err = filepath.WalkDir(pattern, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if strings.HasPrefix(d.Name(), ".") && path != pattern {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			relPath, err := filepath.Rel(pattern, path)
			if err != nil {
				return err
			}
			relPath = filepath.ToSlash(relPath)

			if rules.Matches(relPath, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if !d.IsDir() && strings.ToLower(filepath.Ext(d.Name())) == ".hcl" {
				if !seen[path] {
					seen[path] = true
					matchedFiles = append(matchedFiles, path)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(matchedFiles)
	return matchedFiles, nil
}
