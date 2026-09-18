package manifest

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// IgnoreRules holds compiled ignore patterns loaded from a .hclapiignore file.
type IgnoreRules struct {
	rules []ignoreRule
}

type ignoreRule struct {
	pattern  string
	negated  bool
	dirOnly  bool
	anchored bool
}

// LoadIgnoreRules reads an optional .hclapiignore file from rootDir.
// If the file does not exist, an empty ruleset is returned without error.
func LoadIgnoreRules(rootDir string) (*IgnoreRules, error) {
	path := filepath.Join(rootDir, ".hclapiignore")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &IgnoreRules{}, nil
		}
		return nil, fmt.Errorf("open ignore file: %w", err)
	}
	defer f.Close()

	var rules []ignoreRule
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		negated := strings.HasPrefix(line, "!")
		if negated {
			line = strings.TrimPrefix(line, "!")
		}

		dirOnly := strings.HasSuffix(line, "/")
		if dirOnly {
			line = strings.TrimSuffix(line, "/")
		}

		cleanPattern := filepath.ToSlash(line)
		anchored := strings.Contains(cleanPattern, "/")

		rules = append(rules, ignoreRule{
			pattern:  cleanPattern,
			negated:  negated,
			dirOnly:  dirOnly,
			anchored: anchored,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read ignore file: %w", err)
	}

	return &IgnoreRules{rules: rules}, nil
}

// Matches reports whether relPath is excluded by the active ruleset.
// Evaluation honors pattern order, directory-only suffixes, and negation.
func (ir *IgnoreRules) Matches(relPath string, isDir bool) bool {
	if ir == nil || len(ir.rules) == 0 {
		return false
	}

	normalized := filepath.ToSlash(relPath)
	base := filepath.Base(normalized)
	ignored := false

	for _, rule := range ir.rules {
		if rule.dirOnly && !isDir {
			continue
		}

		matched := false
		if rule.anchored {
			matched, _ = doublestar.Match(rule.pattern, normalized)
		} else {
			matched, _ = doublestar.Match(rule.pattern, base)
			if !matched {
				matched, _ = doublestar.Match("**/"+rule.pattern, normalized)
			}
		}

		if matched {
			ignored = !rule.negated
		}
	}

	return ignored
}

// DiscoverFiles resolves globs, traverses directories, filters out hidden files and
// ignored paths, and returns a deduplicated, sorted list of .hcl file paths.
func DiscoverFiles(patterns ...string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{})
	var discovered []string

	for _, pattern := range patterns {
		if hasGlobMeta(pattern) {
			matches, err := doublestar.FilepathGlob(pattern)
			if err != nil {
				return nil, fmt.Errorf("resolve glob pattern %q: %w", pattern, err)
			}
			for _, match := range matches {
				clean := filepath.Clean(match)
				if isHCLFile(clean) {
					if _, exists := seen[clean]; !exists {
						seen[clean] = struct{}{}
						discovered = append(discovered, clean)
					}
				}
			}
			continue
		}

		info, err := os.Stat(pattern)
		if err != nil {
			return nil, fmt.Errorf("inspect manifest target %q: %w", pattern, err)
		}

		cleanPath := filepath.Clean(pattern)

		if !info.IsDir() {
			if isHCLFile(cleanPath) {
				if _, exists := seen[cleanPath]; !exists {
					seen[cleanPath] = struct{}{}
					discovered = append(discovered, cleanPath)
				}
			}
			continue
		}

		rules, err := LoadIgnoreRules(cleanPath)
		if err != nil {
			return nil, err
		}

		walkErr := filepath.WalkDir(cleanPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			// Automatically skip hidden files and directories
			name := d.Name()
			if strings.HasPrefix(name, ".") && path != cleanPath {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			rel, err := filepath.Rel(cleanPath, path)
			if err != nil {
				return err
			}

			if rules.Matches(rel, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if !d.IsDir() && isHCLFile(name) {
				clean := filepath.Clean(path)
				if _, exists := seen[clean]; !exists {
					seen[clean] = struct{}{}
					discovered = append(discovered, clean)
				}
			}

			return nil
		})

		if walkErr != nil {
			return nil, fmt.Errorf("traverse directory %q: %w", pattern, walkErr)
		}
	}

	sort.Strings(discovered)
	return discovered, nil
}

// hasGlobMeta reports whether s contains wildcard meta-characters.
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// isHCLFile reports whether filename has a case-insensitive .hcl extension.
func isHCLFile(filename string) bool {
	return strings.EqualFold(filepath.Ext(filename), ".hcl")
}
