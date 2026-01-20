// pkg/compress/compress_xz_test.go
package compress

import (
	"archive/tar"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creativeyann17/go-delta/pkg/decompress"
	"github.com/ulikunitz/xz"
)

func TestXzCompressDecompress(t *testing.T) {
	// Create temp directory with test files
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputXz := filepath.Join(tempDir, "output.tar.xz")
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

	// Compress to XZ
	compressOpts := &Options{
		InputPath:   inputDir,
		OutputPath:  outputXz,
		MaxThreads:  2,
		Level:       5,
		UseXzFormat: true,
		Verbose:     false,
		Quiet:       true,
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

	// Verify XZ files are valid (multi-part archives: output_01.tar.xz, output_02.tar.xz, etc.)
	baseOutput := strings.TrimSuffix(outputXz, ".tar.xz")
	firstPart := baseOutput + "_01.tar.xz"
	if _, err := os.Stat(firstPart); err != nil {
		t.Fatalf("XZ archive not found: %v", err)
	}

	// Verify first part is valid by reading it
	xzFile, err := os.Open(firstPart)
	if err != nil {
		t.Fatalf("Failed to open XZ: %v", err)
	}
	xzReader, err := xz.NewReader(xzFile)
	if err != nil {
		xzFile.Close()
		t.Fatalf("Failed to create XZ reader: %v", err)
	}
	tarReader := tar.NewReader(xzReader)
	// Read at least one entry to verify
	_, err = tarReader.Next()
	if err != nil {
		xzFile.Close()
		t.Fatalf("Failed to read tar entry: %v", err)
	}
	xzFile.Close()

	// Decompress
	decompressOpts := &decompress.Options{
		InputPath:  firstPart,
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

		hash := md5.Sum(extractedData)
		extractedHash := fmt.Sprintf("%x", hash)
		if extractedHash != originalHashes[relPath] {
			t.Errorf("Hash mismatch for %s", relPath)
		}
	}
}

func TestXzCompressionLevels(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create a file with repetitive content (compresses well)
	testFile := filepath.Join(inputDir, "test.txt")
	repetitiveContent := make([]byte, 1024*100) // 100KB
	for i := range repetitiveContent {
		repetitiveContent[i] = byte(i % 256)
	}
	if err := os.WriteFile(testFile, repetitiveContent, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	levels := []int{1, 5, 9}
	var prevSize uint64

	for _, level := range levels {
		outputXz := filepath.Join(tempDir, fmt.Sprintf("level%d.tar.xz", level))
		opts := &Options{
			InputPath:   inputDir,
			OutputPath:  outputXz,
			MaxThreads:  1,
			Level:       level,
			UseXzFormat: true,
			Quiet:       true,
		}

		result, err := Compress(opts, nil)
		if err != nil {
			t.Fatalf("Compress at level %d failed: %v", level, err)
		}

		t.Logf("Level %d: Original=%d, Compressed=%d, Ratio=%.1f%%",
			level, result.OriginalSize, result.CompressedSize, result.CompressionRatio())

		// Higher levels should produce smaller or equal files
		if level > 1 && prevSize > 0 {
			if result.CompressedSize > prevSize*2 {
				t.Errorf("Level %d produced significantly larger file than level %d", level, level-1)
			}
		}
		prevSize = result.CompressedSize
	}
}

func TestXzDryRun(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputXz := filepath.Join(tempDir, "output.tar.xz")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(inputDir, "test.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	opts := &Options{
		InputPath:   inputDir,
		OutputPath:  outputXz,
		UseXzFormat: true,
		DryRun:      true,
		Quiet:       true,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Dry run failed: %v", err)
	}

	if result.FilesProcessed != 1 {
		t.Errorf("Expected 1 file processed, got %d", result.FilesProcessed)
	}

	// Verify no file was created
	baseOutput := strings.TrimSuffix(outputXz, ".tar.xz")
	if _, err := os.Stat(baseOutput + "_01.tar.xz"); err == nil {
		t.Error("Dry run should not create output file")
	}
}

func TestXzWithChunkingShouldFail(t *testing.T) {
	tempDir := t.TempDir()

	opts := &Options{
		InputPath:   tempDir,
		OutputPath:  "output.tar.xz",
		UseXzFormat: true,
		ChunkSize:   64 * 1024, // Should fail
		Quiet:       true,
	}

	err := opts.Validate()
	if err == nil {
		t.Error("Expected error when combining XZ format with chunking")
	}

	if err != ErrXzNoChunking {
		t.Errorf("Expected ErrXzNoChunking, got: %v", err)
	}
}

func TestXzThreadSafety(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create many small files to test concurrent writes
	numFiles := 50 // Fewer files since XZ is slower
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(inputDir, fmt.Sprintf("file%04d.txt", i))
		content := fmt.Sprintf("File number %d\n", i)
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file %d: %v", i, err)
		}
	}

	outputXz := filepath.Join(tempDir, "output.tar.xz")
	opts := &Options{
		InputPath:   inputDir,
		OutputPath:  outputXz,
		MaxThreads:  4,
		Level:       1, // Low level for speed
		UseXzFormat: true,
		Quiet:       true,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if result.FilesProcessed != numFiles {
		t.Errorf("Expected %d files, got %d", numFiles, result.FilesProcessed)
	}

	// Verify archives are valid
	baseOutput := strings.TrimSuffix(outputXz, ".tar.xz")
	totalFilesInXz := 0

	// Count files across all parts
	for i := 1; i <= opts.MaxThreads; i++ {
		partPath := fmt.Sprintf("%s_%02d.tar.xz", baseOutput, i)
		if _, err := os.Stat(partPath); os.IsNotExist(err) {
			continue
		}

		file, err := os.Open(partPath)
		if err != nil {
			t.Fatalf("Failed to open XZ part %d: %v", i, err)
		}
		xzReader, err := xz.NewReader(file)
		if err != nil {
			file.Close()
			t.Fatalf("Failed to create XZ reader for part %d: %v", i, err)
		}
		tarReader := tar.NewReader(xzReader)
		for {
			header, err := tarReader.Next()
			if err != nil {
				break
			}
			if header.Typeflag == tar.TypeReg {
				totalFilesInXz++
			}
		}
		file.Close()
	}

	if totalFilesInXz != numFiles {
		t.Errorf("Expected %d files in XZ, got %d across all parts", numFiles, totalFilesInXz)
	}
}
