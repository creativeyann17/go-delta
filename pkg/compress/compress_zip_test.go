// pkg/compress/compress_zip_test.go
package compress

import (
	"archive/zip"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creativeyann17/go-delta/pkg/decompress"
)

func TestZipCompressDecompress(t *testing.T) {
	// Create temp directory with test files
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputZip := filepath.Join(tempDir, "output.zip")
	extractDir := filepath.Join(tempDir, "extracted")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create test files
	testFiles := map[string]string{
		"file1.txt":        "Hello, World!\n",
		"file2.txt":        "This is a test file with some content.\n",
		"subdir/file3.txt": "Nested file content.\n",
	}

	originalHashes := make(map[string]string)
	for relPath, content := range testFiles {
		fullPath := filepath.Join(inputDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create dir for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write %s: %v", relPath, err)
		}
		// Store hash for verification
		hash := md5.Sum([]byte(content))
		originalHashes[relPath] = fmt.Sprintf("%x", hash)
	}

	// Compress to ZIP
	compressOpts := &Options{
		InputPath:    inputDir,
		OutputPath:   outputZip,
		MaxThreads:   2,
		Level:        5,
		UseZipFormat: true,
		Verbose:      false,
		Quiet:        true,
	}

	compressResult, err := Compress(compressOpts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// Verify compress results
	if compressResult.FilesProcessed != len(testFiles) {
		t.Errorf("Expected %d files compressed, got %d", len(testFiles), compressResult.FilesProcessed)
	}

	if compressResult.CompressedSize == 0 {
		t.Error("Compressed size should not be zero")
	}

	if compressResult.CompressedSize >= compressResult.OriginalSize {
		t.Logf("Warning: Compressed size (%d) >= original size (%d) - small test files may not compress well",
			compressResult.CompressedSize, compressResult.OriginalSize)
	}

	// Verify ZIP files are valid (multi-part archives: output_01.zip, output_02.zip, etc.)
	// Try single file first for backward compatibility
	if _, err := os.Stat(outputZip); os.IsNotExist(err) {
		// Multi-part archive - check for _01.zip
		baseOutput := strings.TrimSuffix(outputZip, ".zip")
		firstPart := baseOutput + "_01.zip"
		if _, err := os.Stat(firstPart); err != nil {
			t.Fatalf("Neither single ZIP nor multi-part ZIP found: %v", err)
		}
		outputZip = firstPart // Use first part for decompression

		// Verify first part is valid
		zipReader, err := zip.OpenReader(firstPart)
		if err != nil {
			t.Fatalf("Failed to open ZIP: %v", err)
		}
		zipReader.Close()
	} else {
		// Single file mode - verify it's valid
		zipReader, err := zip.OpenReader(outputZip)
		if err != nil {
			t.Fatalf("Failed to open ZIP: %v", err)
		}
		zipReader.Close()
	}

	// Decompress
	decompressOpts := &decompress.Options{
		InputPath:  outputZip,
		OutputPath: extractDir,
		Overwrite:  true,
		Verbose:    false,
		Quiet:      true,
	}

	decompressResult, err := decompress.Decompress(decompressOpts, nil)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	// Verify decompress results
	if decompressResult.FilesProcessed != len(testFiles) {
		t.Errorf("Expected %d files decompressed, got %d", len(testFiles), decompressResult.FilesProcessed)
	}

	// Verify file contents match
	for relPath, originalContent := range testFiles {
		extractedPath := filepath.Join(extractDir, relPath)
		extractedData, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Errorf("Failed to read extracted file %s: %v", relPath, err)
			continue
		}

		if string(extractedData) != originalContent {
			t.Errorf("Content mismatch for %s:\nExpected: %q\nGot: %q",
				relPath, originalContent, string(extractedData))
		}

		// Verify hash
		hash := md5.Sum(extractedData)
		extractedHash := fmt.Sprintf("%x", hash)
		if extractedHash != originalHashes[relPath] {
			t.Errorf("Hash mismatch for %s", relPath)
		}
	}
}

func TestZipCompressionLevels(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create a file with repetitive content (compresses well)
	testFile := filepath.Join(inputDir, "test.txt")
	repetitiveContent := make([]byte, 1024*100) // 100KB of zeros
	for i := range repetitiveContent {
		repetitiveContent[i] = byte(i % 256)
	}
	if err := os.WriteFile(testFile, repetitiveContent, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	levels := []int{1, 5, 9}
	var prevSize uint64

	for _, level := range levels {
		outputZip := filepath.Join(tempDir, fmt.Sprintf("level%d.zip", level))
		opts := &Options{
			InputPath:    inputDir,
			OutputPath:   outputZip,
			MaxThreads:   1,
			Level:        level,
			UseZipFormat: true,
			Quiet:        true,
		}

		result, err := Compress(opts, nil)
		if err != nil {
			t.Fatalf("Compress at level %d failed: %v", level, err)
		}

		t.Logf("Level %d: Original=%d, Compressed=%d, Ratio=%.1f%%",
			level, result.OriginalSize, result.CompressedSize, result.CompressionRatio())

		// Higher levels should generally produce smaller files (but level 1 uses Store, so it's different)
		if level > 1 && prevSize > 0 {
			if result.CompressedSize > prevSize*2 {
				t.Errorf("Level %d produced significantly larger file than level %d", level, level-1)
			}
		}
		prevSize = result.CompressedSize
	}
}

func TestZipDryRun(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputZip := filepath.Join(tempDir, "output.zip")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(inputDir, "test.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	opts := &Options{
		InputPath:    inputDir,
		OutputPath:   outputZip,
		UseZipFormat: true,
		DryRun:       true,
		Quiet:        true,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Dry run failed: %v", err)
	}

	if result.FilesProcessed != 1 {
		t.Errorf("Expected 1 file processed, got %d", result.FilesProcessed)
	}

	// Verify no file was created
	if _, err := os.Stat(outputZip); err == nil {
		t.Error("Dry run should not create output file")
	}
}

func TestZipWithChunkingShouldFail(t *testing.T) {
	tempDir := t.TempDir()

	opts := &Options{
		InputPath:    tempDir,
		OutputPath:   "output.zip",
		UseZipFormat: true,
		ChunkSize:    64 * 1024, // Should fail
		Quiet:        true,
	}

	err := opts.Validate()
	if err == nil {
		t.Error("Expected error when combining ZIP format with chunking")
	}

	if err != ErrZipNoChunking {
		t.Errorf("Expected ErrZipNoChunking, got: %v", err)
	}
}

func TestZipThreadSafety(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create many small files to test concurrent writes
	numFiles := 100
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(inputDir, fmt.Sprintf("file%04d.txt", i))
		content := fmt.Sprintf("File number %d\n", i)
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file %d: %v", i, err)
		}
	}

	outputZip := filepath.Join(tempDir, "output.zip")
	opts := &Options{
		InputPath:    inputDir,
		OutputPath:   outputZip,
		MaxThreads:   8, // High thread count to stress-test
		Level:        5,
		UseZipFormat: true,
		Quiet:        true,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if result.FilesProcessed != numFiles {
		t.Errorf("Expected %d files, got %d", numFiles, result.FilesProcessed)
	}

	// Verify ZIP files are valid (multi-part)
	baseOutput := strings.TrimSuffix(outputZip, ".zip")
	totalFilesInZip := 0

	// Count files across all parts
	for i := 1; i <= opts.MaxThreads; i++ {
		partPath := fmt.Sprintf("%s_%02d.zip", baseOutput, i)
		if _, err := os.Stat(partPath); os.IsNotExist(err) {
			continue
		}

		zipReader, err := zip.OpenReader(partPath)
		if err != nil {
			t.Fatalf("Failed to open ZIP part %d: %v", i, err)
		}
		totalFilesInZip += len(zipReader.File)
		zipReader.Close()
	}

	if totalFilesInZip != numFiles {
		t.Errorf("Expected %d files in ZIP, got %d across all parts", numFiles, totalFilesInZip)
	}
}

func TestZipWithDisableGC(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputZip := filepath.Join(tempDir, "output.zip")
	extractDir := filepath.Join(tempDir, "extracted")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create test files
	testFiles := map[string]string{
		"file1.txt":        "Hello, World!\n",
		"file2.txt":        "This is a test file with some content.\n",
		"subdir/file3.txt": "Nested file content.\n",
	}

	originalHashes := make(map[string]string)
	for relPath, content := range testFiles {
		fullPath := filepath.Join(inputDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create dir for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write %s: %v", relPath, err)
		}
		hash := md5.Sum([]byte(content))
		originalHashes[relPath] = fmt.Sprintf("%x", hash)
	}

	// Compress with DisableGC enabled
	compressOpts := &Options{
		InputPath:    inputDir,
		OutputPath:   outputZip,
		MaxThreads:   2,
		Level:        5,
		UseZipFormat: true,
		DisableGC:    true, // Key difference: use pooled buffers
		Verbose:      false,
		Quiet:        true,
	}

	compressResult, err := Compress(compressOpts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if compressResult.FilesProcessed != len(testFiles) {
		t.Errorf("Expected %d files compressed, got %d", len(testFiles), compressResult.FilesProcessed)
	}

	// Find the ZIP file (multi-part)
	baseOutput := strings.TrimSuffix(outputZip, ".zip")
	firstPart := baseOutput + "_01.zip"
	if _, err := os.Stat(firstPart); err != nil {
		t.Fatalf("ZIP file not found: %v", err)
	}

	// Decompress and verify
	decompressOpts := &decompress.Options{
		InputPath:  firstPart,
		OutputPath: extractDir,
		Overwrite:  true,
		Quiet:      true,
	}

	_, err = decompress.Decompress(decompressOpts, nil)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	// Verify file contents match
	for relPath, originalContent := range testFiles {
		extractedPath := filepath.Join(extractDir, relPath)
		extractedData, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Errorf("Failed to read extracted file %s: %v", relPath, err)
			continue
		}

		if string(extractedData) != originalContent {
			t.Errorf("Content mismatch for %s:\nExpected: %q\nGot: %q",
				relPath, originalContent, string(extractedData))
		}

		hash := md5.Sum(extractedData)
		extractedHash := fmt.Sprintf("%x", hash)
		if extractedHash != originalHashes[relPath] {
			t.Errorf("Hash mismatch for %s", relPath)
		}
	}
}
