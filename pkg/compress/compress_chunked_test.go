// pkg/compress/compress_chunked_test.go
package compress

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/creativeyann17/go-delta/internal/chunker"
	"github.com/creativeyann17/go-delta/internal/chunkstore"
	"github.com/creativeyann17/go-delta/pkg/decompress"
)

func TestChunkedCompression(t *testing.T) {
	// Create temp directory with test files
	tempDir := t.TempDir()

	// Create files with duplicate content for deduplication
	content1 := bytes.Repeat([]byte("Hello World! This is a test. "), 1000) // ~29KB
	content2 := bytes.Repeat([]byte("Hello World! This is a test. "), 1000) // Same content (should deduplicate)
	content3 := bytes.Repeat([]byte("Different content here. "), 1000)      // ~24KB

	if err := os.WriteFile(filepath.Join(tempDir, "file1.txt"), content1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "file2.txt"), content2, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "file3.txt"), content3, 0644); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(tempDir, "test-chunked.gdelta")

	// Compress with chunking (1MB chunks)
	opts := &Options{
		InputPath:  tempDir,
		OutputPath: archivePath,
		ChunkSize:  1024 * 1024, // 1MB chunks
		Level:      5,
		MaxThreads: 2,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// Verify result
	if result.FilesProcessed != 3 {
		t.Errorf("Expected 3 files processed, got %d", result.FilesProcessed)
	}

	if result.TotalChunks == 0 {
		t.Error("Expected chunks to be processed")
	}

	if result.UniqueChunks == 0 {
		t.Error("Expected unique chunks to be stored")
	}

	// With duplicate content, we should have deduplication
	if result.DedupedChunks == 0 {
		t.Error("Expected some chunks to be deduplicated (file1 and file2 have same content)")
	}

	t.Logf("Compression stats: %d total chunks, %d unique, %d deduped (%.1f%% ratio)",
		result.TotalChunks, result.UniqueChunks, result.DedupedChunks, result.DedupRatio())

	// Verify archive was created
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("Archive file was not created")
	}
}

func TestChunkedRoundTrip(t *testing.T) {
	// Create temp directory with test files
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test files with various sizes
	testFiles := map[string][]byte{
		"small.txt":  []byte("Small file content"),
		"medium.txt": bytes.Repeat([]byte("Medium file content. "), 500), // ~10KB
		"large.txt":  bytes.Repeat([]byte("Large file content. "), 5000), // ~100KB
		"dup1.txt":   bytes.Repeat([]byte("Duplicate content. "), 1000),  // ~20KB
		"dup2.txt":   bytes.Repeat([]byte("Duplicate content. "), 1000),  // Same as dup1
	}

	for filename, content := range testFiles {
		if err := os.WriteFile(filepath.Join(inputDir, filename), content, 0644); err != nil {
			t.Fatal(err)
		}
	}

	archivePath := filepath.Join(tempDir, "roundtrip.gdelta")

	// Compress with chunking
	compressOpts := &Options{
		InputPath:  inputDir,
		OutputPath: archivePath,
		ChunkSize:  16 * 1024, // 16KB chunks
		Level:      3,
		MaxThreads: 4,
	}

	compressResult, err := Compress(compressOpts, nil)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	if compressResult.FilesProcessed != len(testFiles) {
		t.Errorf("Expected %d files compressed, got %d", len(testFiles), compressResult.FilesProcessed)
	}

	// Verify deduplication happened (dup1 and dup2 share chunks)
	if compressResult.DedupedChunks == 0 {
		t.Error("Expected deduplication between dup1.txt and dup2.txt")
	}

	t.Logf("Deduplication: %d/%d chunks deduplicated (%.1f%%)",
		compressResult.DedupedChunks, compressResult.TotalChunks, compressResult.DedupRatio())

	// Decompress
	decompressOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: outputDir,
		Overwrite:  true,
	}

	decompressResult, err := decompress.Decompress(decompressOpts, nil)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	if decompressResult.FilesProcessed != len(testFiles) {
		t.Errorf("Expected %d files decompressed, got %d", len(testFiles), decompressResult.FilesProcessed)
	}

	// Verify all files match
	for filename, expectedContent := range testFiles {
		actualContent, err := os.ReadFile(filepath.Join(outputDir, filename))
		if err != nil {
			t.Errorf("Failed to read decompressed file %s: %v", filename, err)
			continue
		}

		if !bytes.Equal(actualContent, expectedContent) {
			t.Errorf("File %s content mismatch (expected %d bytes, got %d bytes)",
				filename, len(expectedContent), len(actualContent))
		}
	}
}

func TestChunkedWithSubdirectories(t *testing.T) {
	// Create temp directory with nested structure
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputDir := filepath.Join(tempDir, "output")

	// Create nested directory structure
	dirs := []string{
		"",
		"subdir1",
		"subdir2",
		"subdir1/nested",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(inputDir, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create files with duplicate content across directories
	sharedContent := bytes.Repeat([]byte("Shared content across directories. "), 500)

	testFiles := map[string][]byte{
		"root.txt":                 sharedContent,
		"subdir1/file1.txt":        sharedContent, // Same as root.txt
		"subdir1/file2.txt":        []byte("Unique content in subdir1"),
		"subdir2/file3.txt":        sharedContent, // Same as root.txt
		"subdir1/nested/file4.txt": []byte("Unique content in nested"),
	}

	for relPath, content := range testFiles {
		if err := os.WriteFile(filepath.Join(inputDir, relPath), content, 0644); err != nil {
			t.Fatal(err)
		}
	}

	archivePath := filepath.Join(tempDir, "nested.gdelta")

	// Compress with chunking
	compressOpts := &Options{
		InputPath:  inputDir,
		OutputPath: archivePath,
		ChunkSize:  8 * 1024, // 8KB chunks
		Level:      5,
		MaxThreads: 2,
	}

	compressResult, err := Compress(compressOpts, nil)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// Should deduplicate chunks from shared content
	if compressResult.DedupedChunks == 0 {
		t.Error("Expected deduplication across directories")
	}

	t.Logf("Cross-directory dedup: %d chunks saved", compressResult.DedupedChunks)

	// Decompress and verify
	decompressOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: outputDir,
	}

	_, err = decompress.Decompress(decompressOpts, nil)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	// Verify all files
	for relPath, expectedContent := range testFiles {
		actualContent, err := os.ReadFile(filepath.Join(outputDir, relPath))
		if err != nil {
			t.Errorf("Failed to read %s: %v", relPath, err)
			continue
		}

		if !bytes.Equal(actualContent, expectedContent) {
			t.Errorf("Content mismatch for %s", relPath)
		}
	}
}

func TestChunkerSplitting(t *testing.T) {
	tests := []struct {
		name       string
		chunkSize  uint64
		dataSize   int
		wantChunks int
	}{
		{"Small file, large chunks", 1024 * 1024, 500, 1},
		{"Exact chunk size", 1024, 1024, 1},
		{"Multiple chunks", 1024, 3000, 3},
		{"Large file", 16 * 1024, 100 * 1024, 7}, // 100KB / 16KB = ~7 chunks
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := chunker.New(tt.chunkSize)
			data := bytes.Repeat([]byte("x"), tt.dataSize)

			chunks, err := c.Split(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Split failed: %v", err)
			}

			if len(chunks) != tt.wantChunks {
				t.Errorf("Expected %d chunks, got %d", tt.wantChunks, len(chunks))
			}

			// Verify total size matches
			var totalSize uint64
			for _, chunk := range chunks {
				totalSize += chunk.OrigSize
			}

			if totalSize != uint64(tt.dataSize) {
				t.Errorf("Total chunk size %d doesn't match data size %d", totalSize, tt.dataSize)
			}
		})
	}
}

func TestChunkStoreDeduplication(t *testing.T) {
	store := chunkstore.NewStore()

	// Create some test chunks
	chunk1 := []byte("This is chunk 1")
	chunk2 := []byte("This is chunk 2")
	chunk3 := []byte("This is chunk 1") // Duplicate of chunk1

	hash1 := [32]byte{1}
	hash2 := [32]byte{2}
	hash3 := [32]byte{1} // Same hash as chunk1

	offset := uint64(0)

	// Add first chunk
	_, isNew, err := store.GetOrAdd(hash1, uint64(len(chunk1)), func() (uint64, uint64, error) {
		result := offset
		offset += uint64(len(chunk1))
		return result, uint64(len(chunk1)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("First chunk should be new")
	}

	// Add second chunk
	_, isNew, err = store.GetOrAdd(hash2, uint64(len(chunk2)), func() (uint64, uint64, error) {
		result := offset
		offset += uint64(len(chunk2))
		return result, uint64(len(chunk2)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("Second chunk should be new")
	}

	// Add duplicate of first chunk
	_, isNew, err = store.GetOrAdd(hash3, uint64(len(chunk3)), func() (uint64, uint64, error) {
		t.Error("Write function should not be called for duplicate chunk")
		return 0, 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Error("Third chunk should be deduplicated (duplicate of first)")
	}

	// Check statistics
	stats := store.Stats()
	if stats.UniqueChunks != 2 {
		t.Errorf("Expected 2 unique chunks, got %d", stats.UniqueChunks)
	}
	if stats.DedupedChunks != 1 {
		t.Errorf("Expected 1 deduplicated chunk, got %d", stats.DedupedChunks)
	}
	// TotalChunks counts ALL chunks processed (including duplicates): 2 unique + 1 duplicate = 3
	if stats.TotalChunks != 3 {
		t.Errorf("Expected 3 total chunks (2 unique + 1 duplicate), got %d", stats.TotalChunks)
	}

	t.Logf("Store stats: %d total, %d unique, %d deduped",
		stats.TotalChunks, stats.UniqueChunks, stats.DedupedChunks)
}

func TestEmptyFileWithChunking(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create an empty file
	emptyFile := filepath.Join(inputDir, "empty.txt")
	if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(tempDir, "empty.gdelta")

	// Compress with chunking
	compressOpts := &Options{
		InputPath:  inputDir,
		OutputPath: archivePath,
		ChunkSize:  1024 * 1024,
		Level:      5,
	}

	_, err := Compress(compressOpts, nil)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	// Decompress
	decompressOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: outputDir,
	}

	_, err = decompress.Decompress(decompressOpts, nil)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	// Verify empty file exists
	content, err := os.ReadFile(filepath.Join(outputDir, "empty.txt"))
	if err != nil {
		t.Fatalf("Failed to read decompressed file: %v", err)
	}

	if len(content) != 0 {
		t.Errorf("Empty file should have 0 bytes, got %d", len(content))
	}
}
