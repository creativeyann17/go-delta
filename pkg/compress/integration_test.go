package compress_test

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/creativeyann17/go-delta/pkg/compress"
	"github.com/creativeyann17/go-delta/pkg/decompress"
)

// TestRoundTrip tests complete compress/decompress cycle
func TestRoundTrip(t *testing.T) {
	// Create temporary directories
	sourceDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "test.delta")
	destDir := t.TempDir()

	// Create test files with various sizes and content, including subdirectories
	testFiles := []struct {
		name    string
		size    int
		content []byte // if nil, use random data
	}{
		{"small.txt", 100, []byte("small text file content\n")},
		{"medium.txt", 10000, nil},
		{"large.bin", 1024 * 1024, nil}, // 1 MB
		{"empty.txt", 0, []byte{}},
		{"subdir/file1.txt", 50, []byte("file in subdirectory\n")},
		{"subdir/nested/file2.txt", 75, []byte("file in nested subdirectory\n")},
		{"subdir/nested/data.bin", 5000, nil},
	}

	// Create test files and track their checksums
	checksums := make(map[string]string)
	for _, tf := range testFiles {
		path := filepath.Join(sourceDir, tf.name)

		// Create parent directories if needed
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("Failed to create directory for %s: %v", tf.name, err)
		}

		var data []byte
		if tf.content != nil {
			data = tf.content
		} else {
			// Generate pseudo-random but deterministic data
			data = make([]byte, tf.size)
			for i := range data {
				data[i] = byte(i % 256)
			}
		}

		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", tf.name, err)
		}

		// Calculate checksum
		hash := md5.Sum(data)
		checksums[tf.name] = fmt.Sprintf("%x", hash)
	}

	// Test compression
	t.Run("Compress", func(t *testing.T) {
		opts := &compress.Options{
			InputPath:  sourceDir,
			OutputPath: archivePath,
			Level:      5,
			MaxThreads: 2,
			Verbose:    false,
			Quiet:      true,
			DryRun:     false,
		}

		if err := opts.Validate(); err != nil {
			t.Fatalf("Invalid compress options: %v", err)
		}

		result, err := compress.Compress(opts, nil)
		if err != nil {
			t.Fatalf("Compression failed: %v", err)
		}

		if result.FilesProcessed != len(testFiles) {
			t.Errorf("Expected %d files compressed, got %d", len(testFiles), result.FilesProcessed)
		}

		if len(result.Errors) > 0 {
			t.Errorf("Compression had errors: %v", result.Errors)
		}

		// Verify archive was created
		stat, err := os.Stat(archivePath)
		if err != nil {
			t.Fatalf("Archive file not created: %v", err)
		}
		if stat.Size() == 0 {
			t.Error("Archive file is empty")
		}

		t.Logf("Compressed %d files: %.2f MB -> %.2f MB (%.1f%%)",
			result.FilesProcessed,
			float64(result.OriginalSize)/1024/1024,
			float64(result.CompressedSize)/1024/1024,
			result.CompressionRatio())
	})

	// Test decompression
	t.Run("Decompress", func(t *testing.T) {
		opts := &decompress.Options{
			InputPath:  archivePath,
			OutputPath: destDir,
			MaxThreads: 2,
			Verbose:    false,
			Quiet:      true,
			Overwrite:  false,
		}

		if err := opts.Validate(); err != nil {
			t.Fatalf("Invalid decompress options: %v", err)
		}

		result, err := decompress.Decompress(opts, nil)
		if err != nil {
			t.Fatalf("Decompression failed: %v", err)
		}

		if result.FilesProcessed != len(testFiles) {
			t.Errorf("Expected %d files decompressed, got %d", len(testFiles), result.FilesProcessed)
		}

		if len(result.Errors) > 0 {
			t.Errorf("Decompression had errors: %v", result.Errors)
		}

		t.Logf("Decompressed %d files: %.2f MB -> %.2f MB",
			result.FilesProcessed,
			float64(result.CompressedSize)/1024/1024,
			float64(result.DecompressedSize)/1024/1024)
	})

	// Verify decompressed files match originals
	t.Run("Verify", func(t *testing.T) {
		for _, tf := range testFiles {
			dstPath := filepath.Join(destDir, tf.name)

			// Check file exists
			if _, err := os.Stat(dstPath); err != nil {
				t.Errorf("Decompressed file %s not found: %v", tf.name, err)
				continue
			}

			// Calculate checksum of decompressed file
			dstFile, err := os.Open(dstPath)
			if err != nil {
				t.Errorf("Failed to open decompressed file %s: %v", tf.name, err)
				continue
			}

			hash := md5.New()
			if _, err := io.Copy(hash, dstFile); err != nil {
				dstFile.Close()
				t.Errorf("Failed to hash decompressed file %s: %v", tf.name, err)
				continue
			}
			dstFile.Close()

			dstChecksum := fmt.Sprintf("%x", hash.Sum(nil))
			srcChecksum := checksums[tf.name]

			if dstChecksum != srcChecksum {
				t.Errorf("File %s checksum mismatch:\n  original: %s\n  decompressed: %s",
					tf.name, srcChecksum, dstChecksum)
			} else {
				t.Logf("File %s: checksum OK (%s)", tf.name, dstChecksum)
			}
		}
	})
}

// TestCompressEmptyDirectory tests compressing an empty directory
func TestCompressEmptyDirectory(t *testing.T) {
	sourceDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "empty.delta")

	opts := &compress.Options{
		InputPath:  sourceDir,
		OutputPath: archivePath,
		Level:      5,
		MaxThreads: 1,
		Quiet:      true,
	}

	_, err := compress.Compress(opts, nil)
	if err == nil {
		t.Error("Expected error when compressing empty directory, got nil")
	}
}

// TestDecompressNonExistentArchive tests error handling
func TestDecompressNonExistentArchive(t *testing.T) {
	opts := &decompress.Options{
		InputPath:  "/nonexistent/archive.delta",
		OutputPath: t.TempDir(),
		Quiet:      true,
	}

	_, err := decompress.Decompress(opts, nil)
	if err == nil {
		t.Error("Expected error when decompressing non-existent archive, got nil")
	}
}

// TestOverwriteProtection tests that existing files are not overwritten by default
func TestOverwriteProtection(t *testing.T) {
	sourceDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "test.delta")
	destDir := t.TempDir()

	// Create a test file
	testFile := "test.txt"
	testContent := []byte("original content")
	if err := os.WriteFile(filepath.Join(sourceDir, testFile), testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Compress
	compOpts := &compress.Options{
		InputPath:  sourceDir,
		OutputPath: archivePath,
		Level:      5,
		Quiet:      true,
	}
	if _, err := compress.Compress(compOpts, nil); err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// Create a file in destination with same name
	existingContent := []byte("existing content - should not be overwritten")
	existingPath := filepath.Join(destDir, testFile)
	if err := os.WriteFile(existingPath, existingContent, 0644); err != nil {
		t.Fatalf("Failed to create existing file: %v", err)
	}

	// Try to decompress without overwrite flag
	decompOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: destDir,
		Overwrite:  false,
		Quiet:      true,
	}

	result, err := decompress.Decompress(decompOpts, nil)
	if err == nil && len(result.Errors) == 0 {
		t.Error("Expected error when file exists and overwrite is false")
	}

	// Verify file was not overwritten
	content, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content) != string(existingContent) {
		t.Error("File was overwritten despite overwrite=false")
	}

	// Now test with overwrite enabled
	decompOpts.Overwrite = true
	result, err = decompress.Decompress(decompOpts, nil)
	if err != nil {
		t.Fatalf("Decompression with overwrite failed: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Errorf("Decompression with overwrite had errors: %v", result.Errors)
	}

	// Verify file was overwritten
	content, err = os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("Failed to read file after overwrite: %v", err)
	}
	if string(content) == string(existingContent) {
		t.Error("File was not overwritten despite overwrite=true")
	}
}

// TestCompressTwice tests compressing to the same archive twice
func TestCompressTwice(t *testing.T) {
	sourceDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "test.delta")

	// Create a test file
	testFile := "test.txt"
	testContent := []byte("test content")
	if err := os.WriteFile(filepath.Join(sourceDir, testFile), testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// First compression
	opts := &compress.Options{
		InputPath:  sourceDir,
		OutputPath: archivePath,
		Level:      5,
		Quiet:      true,
	}

	result1, err := compress.Compress(opts, nil)
	if err != nil {
		t.Fatalf("First compression failed: %v", err)
	}
	if result1.FilesProcessed != 1 {
		t.Errorf("Expected 1 file compressed, got %d", result1.FilesProcessed)
	}

	// Get size of first archive
	stat1, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("Failed to stat first archive: %v", err)
	}

	// Second compression (should overwrite)
	result2, err := compress.Compress(opts, nil)
	if err != nil {
		t.Fatalf("Second compression failed: %v", err)
	}
	if result2.FilesProcessed != 1 {
		t.Errorf("Expected 1 file compressed on second run, got %d", result2.FilesProcessed)
	}

	// Verify archive still exists and has reasonable size
	stat2, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("Failed to stat second archive: %v", err)
	}

	if stat2.Size() != stat1.Size() {
		t.Logf("Archive sizes differ (expected for same content): %d vs %d", stat1.Size(), stat2.Size())
	}
}

// TestDecompressTwice tests decompressing the same archive twice
func TestDecompressTwice(t *testing.T) {
	sourceDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "test.delta")
	destDir := t.TempDir()

	// Create test files
	testFiles := map[string][]byte{
		"file1.txt": []byte("content 1"),
		"file2.txt": []byte("content 2"),
	}

	for name, content := range testFiles {
		if err := os.WriteFile(filepath.Join(sourceDir, name), content, 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", name, err)
		}
	}

	// Compress
	compOpts := &compress.Options{
		InputPath:  sourceDir,
		OutputPath: archivePath,
		Level:      5,
		Quiet:      true,
	}
	if _, err := compress.Compress(compOpts, nil); err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// First decompression
	decompOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: destDir,
		Overwrite:  false,
		Quiet:      true,
	}

	result1, err := decompress.Decompress(decompOpts, nil)
	if err != nil {
		t.Fatalf("First decompression failed: %v", err)
	}
	if result1.FilesProcessed != len(testFiles) {
		t.Errorf("Expected %d files decompressed, got %d", len(testFiles), result1.FilesProcessed)
	}
	if len(result1.Errors) > 0 {
		t.Errorf("First decompression had errors: %v", result1.Errors)
	}

	// Second decompression (should fail gracefully without overwrite flag)
	result2, err := decompress.Decompress(decompOpts, nil)

	// Should complete without crashing - if top-level error, something went wrong
	if err != nil {
		t.Fatalf("Second decompression crashed with top-level error (should handle gracefully): %v", err)
	}

	// Must have errors for all existing files - if not, the file exists check isn't working
	if len(result2.Errors) != len(testFiles) {
		t.Fatalf("Expected exactly %d errors for existing files, got %d: %v", len(testFiles), len(result2.Errors), result2.Errors)
	}

	// No files should be processed when all fail due to existing files
	if result2.FilesProcessed != 0 {
		t.Fatalf("Expected 0 files processed on second decompression, got %d (files should not be overwritten)", result2.FilesProcessed)
	}

	// All errors must be ErrFileExists - if not, something else went wrong
	for i, err := range result2.Errors {
		if !errors.Is(err, decompress.ErrFileExists) {
			t.Fatalf("Error %d should be ErrFileExists, got unexpected error: %v", i, err)
		}
	}

	t.Log("✓ Second decompression correctly failed with file exists errors (no crash)")

	// Now test with overwrite enabled - should succeed
	decompOpts.Overwrite = true
	result3, err := decompress.Decompress(decompOpts, nil)
	if err != nil {
		t.Fatalf("Third decompression with overwrite failed: %v", err)
	}
	if len(result3.Errors) > 0 {
		t.Fatalf("Third decompression with overwrite had unexpected errors: %v", result3.Errors)
	}
	if result3.FilesProcessed != len(testFiles) {
		t.Fatalf("Expected %d files processed with overwrite, got %d", len(testFiles), result3.FilesProcessed)
	}

	t.Log("✓ Third decompression with overwrite succeeded")
}

// TestCustomFilesOutsideWorkingDir tests using Files option with files outside current working directory
func TestCustomFilesOutsideWorkingDir(t *testing.T) {
	// Create test structure:
	// /tmp/dir1/file1.txt
	// /tmp/dir2/subdir/file2.txt
	// /tmp/dir3/file3.txt
	// We'll run the test from /tmp/workdir (different from the files)

	workDir := t.TempDir() // /tmp/.../workdir
	dir1 := t.TempDir()    // /tmp/.../dir1
	dir2 := t.TempDir()    // /tmp/.../dir2
	dir3 := t.TempDir()    // /tmp/.../dir3
	archivePath := filepath.Join(workDir, "test.delta")
	destDir := t.TempDir()

	// Create test files in different directories
	file1Path := filepath.Join(dir1, "file1.txt")
	file2Path := filepath.Join(dir2, "subdir", "file2.txt")
	file3Path := filepath.Join(dir3, "file3.txt")

	if err := os.WriteFile(file1Path, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(file2Path), 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}
	if err := os.WriteFile(file2Path, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}
	if err := os.WriteFile(file3Path, []byte("content3"), 0644); err != nil {
		t.Fatalf("Failed to create file3: %v", err)
	}

	// Change to workDir (different from file locations)
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Failed to change to work dir: %v", err)
	}

	// Test compression with Files option
	t.Run("Compress", func(t *testing.T) {
		opts := &compress.Options{
			Files: []string{
				file1Path,
				file2Path,
				dir3, // entire directory
			},
			OutputPath: archivePath,
			Level:      5,
			MaxThreads: 2,
			Verbose:    true,
			Quiet:      false,
			DryRun:     false,
		}

		if err := opts.Validate(); err != nil {
			t.Fatalf("Invalid compress options: %v", err)
		}

		result, err := compress.Compress(opts, nil)
		if err != nil {
			t.Fatalf("Compression failed: %v", err)
		}

		if result.FilesProcessed != 3 {
			t.Errorf("Expected 3 files compressed, got %d", result.FilesProcessed)
		}

		if len(result.Errors) > 0 {
			t.Errorf("Compression had errors: %v", result.Errors)
		}

		t.Logf("Compressed %d files from custom list", result.FilesProcessed)
	})

	// Test decompression
	t.Run("Decompress", func(t *testing.T) {
		opts := &decompress.Options{
			InputPath:  archivePath,
			OutputPath: destDir,
			MaxThreads: 2,
			Verbose:    true,
			Quiet:      false,
			Overwrite:  false,
		}

		if err := opts.Validate(); err != nil {
			t.Fatalf("Invalid decompress options: %v", err)
		}

		result, err := decompress.Decompress(opts, nil)
		if err != nil {
			t.Fatalf("Decompression failed: %v", err)
		}

		if result.FilesProcessed != 3 {
			t.Errorf("Expected 3 files decompressed, got %d", result.FilesProcessed)
		}

		if len(result.Errors) > 0 {
			t.Errorf("Decompression had errors: %v", result.Errors)
		}
	})

	// Verify decompressed files
	t.Run("Verify", func(t *testing.T) {
		// Check that files were decompressed with proper paths
		// The relative paths should be meaningful, not "." or empty

		// List all files in destDir recursively
		var extractedFiles []string
		err := filepath.Walk(destDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(destDir, path)
			extractedFiles = append(extractedFiles, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to walk destination directory: %v", err)
		}

		t.Logf("Extracted files: %v", extractedFiles)

		// Verify we have 3 files
		if len(extractedFiles) != 3 {
			t.Errorf("Expected 3 extracted files, got %d: %v", len(extractedFiles), extractedFiles)
		}

		// Verify none of the paths are "." or empty
		for _, path := range extractedFiles {
			if path == "." || path == "" {
				t.Errorf("Found invalid path in archive: %q (should have meaningful relative path)", path)
			}
		}

		// Verify content of files
		foundFiles := make(map[string]bool)
		for _, path := range extractedFiles {
			fullPath := filepath.Join(destDir, path)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				t.Errorf("Failed to read %s: %v", path, err)
				continue
			}
			contentStr := string(content)
			if contentStr == "content1" {
				foundFiles["file1"] = true
			} else if contentStr == "content2" {
				foundFiles["file2"] = true
			} else if contentStr == "content3" {
				foundFiles["file3"] = true
			}
		}

		if len(foundFiles) != 3 {
			t.Errorf("Not all file contents were found correctly: %v", foundFiles)
		}
	})
}

// TestCustomFilesNoCommonBase tests Files option with files that have no common parent
func TestCustomFilesNoCommonBase(t *testing.T) {
	// Test edge case: files with minimal common base (e.g., just "/" on Unix)
	// This verifies that even in extreme cases, we get valid relative paths

	workDir := t.TempDir()
	archivePath := filepath.Join(workDir, "test.delta")
	destDir := t.TempDir()

	// Create files in separate temp directories (no common parent except root)
	file1 := filepath.Join(t.TempDir(), "file1.txt")
	file2 := filepath.Join(t.TempDir(), "file2.txt")

	if err := os.WriteFile(file1, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}

	// Compress with Files option
	opts := &compress.Options{
		Files:      []string{file1, file2},
		OutputPath: archivePath,
		Level:      5,
		MaxThreads: 2,
	}

	result, err := compress.Compress(opts, nil)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	if result.FilesProcessed != 2 {
		t.Errorf("Expected 2 files compressed, got %d", result.FilesProcessed)
	}

	// Decompress
	decOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: destDir,
	}

	decResult, err := decompress.Decompress(decOpts, nil)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	if decResult.FilesProcessed != 2 {
		t.Errorf("Expected 2 files decompressed, got %d", decResult.FilesProcessed)
	}

	// Verify extracted files have valid paths (not "." or empty)
	var extractedFiles []string
	err = filepath.Walk(destDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(destDir, path)
		extractedFiles = append(extractedFiles, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to walk destination: %v", err)
	}

	t.Logf("Extracted files: %v", extractedFiles)

	for _, path := range extractedFiles {
		if path == "." || path == "" {
			t.Errorf("Found invalid path: %q", path)
		}
	}

	if len(extractedFiles) != 2 {
		t.Errorf("Expected 2 extracted files, got %d", len(extractedFiles))
	}
}
