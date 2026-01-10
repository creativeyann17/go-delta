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
