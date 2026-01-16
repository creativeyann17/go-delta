// pkg/compress/gitignore.go
package compress

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// gitignoreMatcher handles .gitignore pattern matching with proper hierarchy support.
// It pre-scans the directory tree for .gitignore files and compiles them into matchers.
type gitignoreMatcher struct {
	baseDir  string                       // Root directory for this matcher
	matchers map[string]*ignore.GitIgnore // Key: relative dir path, Value: compiled patterns
	// Keys are relative paths like "", "src", "src/lib" (empty string = root)
}

// newGitignoreMatcher creates a matcher that pre-scans the directory tree for .gitignore files.
// Returns nil if no .gitignore files are found (no-op for performance).
func newGitignoreMatcher(baseDir string) (*gitignoreMatcher, error) {
	baseDir = filepath.Clean(baseDir)
	gm := &gitignoreMatcher{
		baseDir:  baseDir,
		matchers: make(map[string]*ignore.GitIgnore),
	}

	// Scan for all .gitignore files in the tree
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip inaccessible paths
			return nil
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Base(path) != ".gitignore" {
			return nil
		}

		// Get the directory containing this .gitignore
		dir := filepath.Dir(path)
		relDir, err := filepath.Rel(baseDir, dir)
		if err != nil {
			return nil
		}
		if relDir == "." {
			relDir = ""
		}

		// Compile the gitignore file
		matcher, err := ignore.CompileIgnoreFile(path)
		if err != nil {
			// Skip invalid .gitignore files silently
			return nil
		}

		gm.matchers[relDir] = matcher
		return nil
	})

	if err != nil {
		return nil, err
	}

	// If no .gitignore files found, return nil (caller can skip filtering)
	if len(gm.matchers) == 0 {
		return nil, nil
	}

	return gm, nil
}

// ShouldIgnore checks if a file at relPath should be ignored.
// relPath should be relative to the matcher's baseDir.
// Returns true if the file matches any ignore pattern.
// Negation patterns within the same .gitignore file work correctly.
// Cross-file negation (child negating parent patterns) requires the child to re-specify the negation.
func (gm *gitignoreMatcher) ShouldIgnore(relPath string) bool {
	if gm == nil || len(gm.matchers) == 0 {
		return false
	}

	// Normalize path separators
	relPath = filepath.ToSlash(relPath)

	// Check against all matchers that could apply to this path
	// Process from root to most-specific directory
	hierarchy := gm.buildHierarchy(relPath)

	for _, dirPath := range hierarchy {
		matcher, exists := gm.matchers[dirPath]
		if !exists {
			continue
		}

		// Get path relative to this .gitignore's directory
		var pathToCheck string
		if dirPath == "" {
			pathToCheck = relPath
		} else {
			// Make path relative to this directory
			pathToCheck = strings.TrimPrefix(relPath, dirPath+"/")
		}

		// Check if this matcher matches the path
		// go-gitignore handles negation patterns internally within the file
		if matcher.MatchesPath(pathToCheck) {
			return true
		}
	}

	return false
}

// ShouldIgnoreDir checks if a directory should be entirely skipped.
// This is used for pruning entire subtrees during filepath.Walk.
// Only matches explicit directory patterns (e.g., "build/") to avoid
// incorrectly pruning directories that match file patterns (e.g., "*.log").
func (gm *gitignoreMatcher) ShouldIgnoreDir(relPath string) bool {
	if gm == nil || len(gm.matchers) == 0 {
		return false
	}

	// Only prune if pattern matches with trailing slash but NOT without.
	// This ensures we only prune for directory-specific patterns like "build/"
	// and not file patterns like "*.log" that happen to match directory names.
	matchesWithSlash := gm.ShouldIgnore(relPath + "/")
	matchesWithoutSlash := gm.ShouldIgnore(relPath)

	return matchesWithSlash && !matchesWithoutSlash
}

// buildHierarchy builds the list of directory paths from root to the file's parent.
// For "src/lib/file.log", returns ["", "src", "src/lib"]
func (gm *gitignoreMatcher) buildHierarchy(relPath string) []string {
	relPath = filepath.ToSlash(relPath)

	// Get parent directory
	parentDir := filepath.Dir(relPath)
	if parentDir == "." {
		parentDir = ""
	}
	parentDir = filepath.ToSlash(parentDir)

	hierarchy := []string{""}

	if parentDir == "" {
		return hierarchy
	}

	// Build hierarchy from root to parent
	parts := strings.Split(parentDir, "/")
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = current + "/" + part
		}
		hierarchy = append(hierarchy, current)
	}

	// Sort to ensure consistent order (root first)
	sort.Slice(hierarchy, func(i, j int) bool {
		return len(hierarchy[i]) < len(hierarchy[j])
	})

	return hierarchy
}
