// pkg/compress/gitignore_test.go
package compress

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitignoreMatcher_BasicPatterns(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	// Create .gitignore with basic patterns
	gitignoreContent := `*.log
build/
*.tmp
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test files
	createFile(t, tmpDir, "keep.txt", "content")
	createFile(t, tmpDir, "debug.log", "log content")
	createFile(t, tmpDir, "cache.tmp", "temp content")
	createDir(t, tmpDir, "build")
	createFile(t, tmpDir, "build/output.bin", "binary")
	createDir(t, tmpDir, "src")
	createFile(t, tmpDir, "src/main.go", "package main")

	// Create matcher
	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}

	tests := []struct {
		path     string
		expected bool
	}{
		{"keep.txt", false},
		{"debug.log", true},
		{"cache.tmp", true},
		{"build/output.bin", true},
		{"src/main.go", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := matcher.ShouldIgnore(tc.path)
			if got != tc.expected {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		})
	}
}

func TestGitignoreMatcher_DirectoryPruning(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore that ignores a directory
	gitignoreContent := `build/
node_modules/
*.log
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	createDir(t, tmpDir, "build")
	createDir(t, tmpDir, "node_modules")
	createDir(t, tmpDir, "src")
	createDir(t, tmpDir, "debug.log") // Directory with name matching file pattern

	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		dir      string
		expected bool
	}{
		{"build", true},
		{"node_modules", true},
		{"src", false},
		{"debug.log", false}, // Directory should NOT be pruned by file pattern *.log
	}

	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			got := matcher.ShouldIgnoreDir(tc.dir)
			if got != tc.expected {
				t.Errorf("ShouldIgnoreDir(%q) = %v, want %v", tc.dir, got, tc.expected)
			}
		})
	}
}

func TestGitignoreMatcher_NestedGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	// Root .gitignore: ignore all .log files
	rootGitignore := `*.log
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(rootGitignore), 0644); err != nil {
		t.Fatal(err)
	}

	// Create nested structure
	createDir(t, tmpDir, "src")
	createDir(t, tmpDir, "src/lib")

	// src/.gitignore: also ignore .tmp files
	srcGitignore := `*.tmp
`
	if err := os.WriteFile(filepath.Join(tmpDir, "src", ".gitignore"), []byte(srcGitignore), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test files
	createFile(t, tmpDir, "debug.log", "log")
	createFile(t, tmpDir, "src/cache.tmp", "temp")
	createFile(t, tmpDir, "src/main.go", "package main")
	createFile(t, tmpDir, "src/lib/data.txt", "data")

	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		expected bool
	}{
		{"debug.log", true},         // Ignored by root *.log
		{"src/cache.tmp", true},     // Ignored by src/*.tmp
		{"src/main.go", false},      // Not matched by any pattern
		{"src/lib/data.txt", false}, // Not matched by any pattern
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := matcher.ShouldIgnore(tc.path)
			if got != tc.expected {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		})
	}
}

func TestGitignoreMatcher_Negation(t *testing.T) {
	tmpDir := t.TempDir()

	// .gitignore with negation pattern (within same file)
	gitignoreContent := `*.log
!important.log
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	createFile(t, tmpDir, "debug.log", "log")
	createFile(t, tmpDir, "important.log", "important")
	createFile(t, tmpDir, "keep.txt", "content")

	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		expected bool
	}{
		{"debug.log", true},      // Ignored by *.log
		{"important.log", false}, // Un-ignored by !important.log
		{"keep.txt", false},      // Not matched
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := matcher.ShouldIgnore(tc.path)
			if got != tc.expected {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		})
	}
}

func TestGitignoreMatcher_NoGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files but no .gitignore
	createFile(t, tmpDir, "file.txt", "content")
	createFile(t, tmpDir, "debug.log", "log")

	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Should return nil when no .gitignore exists
	if matcher != nil {
		t.Error("expected nil matcher when no .gitignore exists")
	}

	// nil matcher should not ignore anything
	if matcher.ShouldIgnore("file.txt") {
		t.Error("nil matcher should return false")
	}
	if matcher.ShouldIgnoreDir("any") {
		t.Error("nil matcher should return false for directories")
	}
}

func TestGitignoreMatcher_EmptyGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	// Create empty .gitignore
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	createFile(t, tmpDir, "file.txt", "content")

	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Empty .gitignore might return nil or a matcher that ignores nothing
	if matcher != nil && matcher.ShouldIgnore("file.txt") {
		t.Error("empty .gitignore should not ignore any files")
	}
}

func TestGitignoreMatcher_CommentAndBlankLines(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore with comments and blank lines
	gitignoreContent := `# This is a comment
*.log

# Another comment
build/

`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	createFile(t, tmpDir, "debug.log", "log")
	createFile(t, tmpDir, "keep.txt", "content")

	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if !matcher.ShouldIgnore("debug.log") {
		t.Error("*.log should be ignored")
	}
	if matcher.ShouldIgnore("keep.txt") {
		t.Error("keep.txt should not be ignored")
	}
}

func TestGitignoreMatcher_DoubleStarPattern(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore with ** pattern
	gitignoreContent := `**/temp/
**/*.bak
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	createDir(t, tmpDir, "temp")
	createDir(t, tmpDir, "a/temp")
	createDir(t, tmpDir, "a/b/temp")
	createFile(t, tmpDir, "file.bak", "backup")
	createFile(t, tmpDir, "a/file.bak", "backup")
	createFile(t, tmpDir, "a/b/file.bak", "backup")
	createFile(t, tmpDir, "keep.txt", "content")

	matcher, err := newGitignoreMatcher(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		expected bool
	}{
		{"file.bak", true},
		{"a/file.bak", true},
		{"a/b/file.bak", true},
		{"keep.txt", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := matcher.ShouldIgnore(tc.path)
			if got != tc.expected {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		})
	}

	// Test directory patterns
	dirTests := []struct {
		dir      string
		expected bool
	}{
		{"temp", true},
		{"a/temp", true},
		{"a/b/temp", true},
	}

	for _, tc := range dirTests {
		t.Run("dir_"+tc.dir, func(t *testing.T) {
			got := matcher.ShouldIgnoreDir(tc.dir)
			if got != tc.expected {
				t.Errorf("ShouldIgnoreDir(%q) = %v, want %v", tc.dir, got, tc.expected)
			}
		})
	}
}

func TestGitignore_Integration(t *testing.T) {
	tmpDir := t.TempDir()

	// Create realistic project structure
	gitignoreContent := `*.log
build/
.env
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files
	createFile(t, tmpDir, "main.go", "package main")
	createFile(t, tmpDir, "debug.log", "logs")
	createFile(t, tmpDir, ".env", "SECRET=123")
	createDir(t, tmpDir, "build")
	createFile(t, tmpDir, "build/output", "binary")
	createDir(t, tmpDir, "src")
	createFile(t, tmpDir, "src/app.go", "package src")

	// Compress with gitignore enabled
	outPath := filepath.Join(tmpDir, "test.gdelta")
	opts := &Options{
		InputPath:    tmpDir,
		OutputPath:   outPath,
		UseGitignore: true,
		Level:        1,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 3 files: main.go, src/app.go, and .gitignore
	// (debug.log, .env, and build/* are ignored)
	if result.FilesProcessed != 3 {
		t.Errorf("expected 3 files, got %d", result.FilesProcessed)
	}
}

func TestGitignore_Disabled(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore
	gitignoreContent := `*.log
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	createFile(t, tmpDir, "main.go", "package main")
	createFile(t, tmpDir, "debug.log", "logs")

	// Compress without gitignore (should include all files)
	outPath := filepath.Join(tmpDir, "test.gdelta")
	opts := &Options{
		InputPath:    tmpDir,
		OutputPath:   outPath,
		UseGitignore: false,
		Level:        1,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 3 files: main.go, debug.log, and .gitignore
	if result.FilesProcessed != 3 {
		t.Errorf("expected 3 files without gitignore filtering, got %d", result.FilesProcessed)
	}
}

// Helper functions

func createFile(t *testing.T, base, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(base, relPath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func createDir(t *testing.T, base, relPath string) {
	t.Helper()
	fullPath := filepath.Join(base, relPath)
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		t.Fatal(err)
	}
}
