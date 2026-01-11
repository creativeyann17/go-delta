// pkg/compress/compress_zip_multipart_test.go
package compress

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creativeyann17/go-delta/pkg/decompress"
)

// TestZipMultiPartArchive verifies that multi-threaded ZIP compression
// creates multiple archive files (one per thread) and decompression
// correctly handles all parts.
func TestZipMultiPartArchive(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	extractDir := filepath.Join(tempDir, "extracted")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create 30 test files
	numFiles := 30
	testFiles := make(map[string]string)
	for i := 0; i < numFiles; i++ {
		filename := fmt.Sprintf("test_file_%03d.txt", i)
		filepath := filepath.Join(inputDir, filename)
		content := fmt.Sprintf("Test file number %d with some content to compress\n", i)
		if err := os.WriteFile(filepath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file %d: %v", i, err)
		}
		testFiles[filename] = content
	}

	// Compress with 4 threads - should create 4 separate ZIP files
	outputZip := filepath.Join(tempDir, "archive.zip")
	numThreads := 4
	compressOpts := &Options{
		InputPath:    inputDir,
		OutputPath:   outputZip,
		MaxThreads:   numThreads,
		Level:        5,
		UseZipFormat: true,
		Quiet:        true,
	}

	compressResult, err := Compress(compressOpts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// Verify compress results
	if compressResult.FilesProcessed != numFiles {
		t.Errorf("Expected %d files compressed, got %d", numFiles, compressResult.FilesProcessed)
	}

	// Verify multi-part archives were created (archive_01.zip, archive_02.zip, etc.)
	baseOutput := strings.TrimSuffix(outputZip, ".zip")
	partsFound := 0
	totalFilesInParts := 0

	for i := 1; i <= numThreads; i++ {
		partPath := fmt.Sprintf("%s_%02d.zip", baseOutput, i)
		if _, err := os.Stat(partPath); err != nil {
			t.Errorf("Expected ZIP part %d not found: %s", i, partPath)
			continue
		}

		// Verify each part is a valid ZIP
		zipReader, err := zip.OpenReader(partPath)
		if err != nil {
			t.Fatalf("Failed to open ZIP part %d: %v", i, err)
		}

		filesInPart := len(zipReader.File)
		totalFilesInParts += filesInPart
		partsFound++

		t.Logf("Part %d contains %d files", i, filesInPart)
		zipReader.Close()
	}

	if partsFound != numThreads {
		t.Errorf("Expected %d ZIP parts, found %d", numThreads, partsFound)
	}

	if totalFilesInParts != numFiles {
		t.Errorf("Expected %d total files across parts, found %d", numFiles, totalFilesInParts)
	}

	// Decompress using first part - should auto-detect and extract all parts
	firstPart := fmt.Sprintf("%s_01.zip", baseOutput)
	decompressOpts := &decompress.Options{
		InputPath:  firstPart,
		OutputPath: extractDir,
		Overwrite:  true,
		Quiet:      true,
	}

	decompressResult, err := decompress.Decompress(decompressOpts, nil)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	// Verify decompress results
	if decompressResult.FilesProcessed != numFiles {
		t.Errorf("Expected %d files decompressed, got %d", numFiles, decompressResult.FilesProcessed)
	}

	// Verify all files extracted with correct content
	for filename, expectedContent := range testFiles {
		extractedPath := filepath.Join(extractDir, filename)
		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Errorf("Failed to read extracted file %s: %v", filename, err)
			continue
		}

		if string(content) != expectedContent {
			t.Errorf("Content mismatch for %s", filename)
		}
	}
}

// TestZipMultiPartNaming verifies the naming convention for multi-part archives
func TestZipMultiPartNaming(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	// Create test files
	for i := 0; i < 6; i++ {
		filename := filepath.Join(inputDir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
	}

	testCases := []struct {
		name           string
		outputPath     string
		threads        int
		expectedPrefix string
	}{
		{
			name:           "Simple name",
			outputPath:     filepath.Join(tempDir, "backup.zip"),
			threads:        3,
			expectedPrefix: "backup",
		},
		{
			name:           "Name without extension",
			outputPath:     filepath.Join(tempDir, "mybackup"),
			threads:        2,
			expectedPrefix: "mybackup",
		},
		{
			name:           "Path with subdirs",
			outputPath:     filepath.Join(tempDir, "archives", "data.zip"),
			threads:        2,
			expectedPrefix: "data",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create subdirectory if needed
			if err := os.MkdirAll(filepath.Dir(tc.outputPath), 0755); err != nil {
				t.Fatalf("Failed to create output dir: %v", err)
			}

			opts := &Options{
				InputPath:    inputDir,
				OutputPath:   tc.outputPath,
				MaxThreads:   tc.threads,
				UseZipFormat: true,
				Quiet:        true,
			}

			_, err := Compress(opts, nil)
			if err != nil {
				t.Fatalf("Compress failed: %v", err)
			}

			// Verify naming pattern
			baseOutput := strings.TrimSuffix(tc.outputPath, ".zip")
			for i := 1; i <= tc.threads; i++ {
				expectedPath := fmt.Sprintf("%s_%02d.zip", baseOutput, i)
				if _, err := os.Stat(expectedPath); err != nil {
					t.Errorf("Expected part %s not found", expectedPath)
				}

				baseName := filepath.Base(expectedPath)
				if !strings.HasPrefix(baseName, tc.expectedPrefix) {
					t.Errorf("Expected part to start with %s, got %s", tc.expectedPrefix, baseName)
				}
			}
		})
	}
}
