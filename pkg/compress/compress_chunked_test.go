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
	// Test with content-defined chunking (FastCDC)
	// Chunk counts are variable based on content patterns, not fixed sizes
	tests := []struct {
		name      string
		avgSize   uint64
		dataSize  int
		minChunks int // Minimum expected chunks
	}{
		{"Small file, large chunks", 1024 * 1024, 500, 1},
		{"Medium file", 1024, 5000, 1},
		{"Large file", 16 * 1024, 200 * 1024, 1}, // At least 1 chunk
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := chunker.New(tt.avgSize)
			data := bytes.Repeat([]byte("test data content "), tt.dataSize/18+1)
			data = data[:tt.dataSize] // Trim to exact size

			chunks, err := c.Split(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Split failed: %v", err)
			}

			if len(chunks) < tt.minChunks {
				t.Errorf("Expected at least %d chunks, got %d", tt.minChunks, len(chunks))
			}

			// Verify total size matches
			var totalSize uint64
			for _, chunk := range chunks {
				totalSize += chunk.OrigSize
			}

			if totalSize != uint64(tt.dataSize) {
				t.Errorf("Total chunk size %d doesn't match data size %d", totalSize, tt.dataSize)
			}

			// Verify reassembly
			var reassembled []byte
			for _, chunk := range chunks {
				reassembled = append(reassembled, chunk.Data...)
			}
			if !bytes.Equal(reassembled, data) {
				t.Error("Reassembled data doesn't match original")
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

// TestChunkedSmallerThanNonChunked verifies that GDELTA02 (chunked with CDC)
// produces smaller archives than GDELTA01 (non-chunked) when files share content.
// This demonstrates the value of content-defined chunking for deduplication.
func TestChunkedSmallerThanNonChunked(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create pseudo-random but reproducible base content (~100KB)
	// This simulates real data that doesn't compress as well as pure repetition
	// but has shared content across files (like log files, backups, etc.)
	baseContent := make([]byte, 100*1024)
	for i := range baseContent {
		// Pseudo-random pattern that's not easily compressible
		baseContent[i] = byte((i*7 + i/256*13 + i/65536*17) % 256)
	}

	// Create multiple files with the same base content but different prefixes/suffixes
	// With CDC, the shared content will deduplicate despite the shifts
	files := []struct {
		name    string
		content []byte
	}{
		{"file1.bin", baseContent},
		{"file2.bin", append([]byte("PREFIX_A:"), baseContent...)},
		{"file3.bin", append([]byte("DIFFERENT_PREFIX_BB:"), baseContent...)},
		{"file4.bin", append(baseContent, []byte(":SUFFIX_C")...)},
		{"file5.bin", baseContent}, // Exact duplicate
	}

	var totalOriginalSize int64
	for _, f := range files {
		path := filepath.Join(inputDir, f.name)
		if err := os.WriteFile(path, f.content, 0644); err != nil {
			t.Fatal(err)
		}
		totalOriginalSize += int64(len(f.content))
	}

	// Compress without chunking (GDELTA01)
	nonChunkedPath := filepath.Join(tempDir, "non-chunked.gdelta")
	nonChunkedOpts := &Options{
		InputPath:  inputDir,
		OutputPath: nonChunkedPath,
		ChunkSize:  0, // No chunking
		Level:      5,
	}

	_, err := Compress(nonChunkedOpts, nil)
	if err != nil {
		t.Fatalf("Non-chunked compression failed: %v", err)
	}

	nonChunkedInfo, err := os.Stat(nonChunkedPath)
	if err != nil {
		t.Fatalf("Failed to stat non-chunked archive: %v", err)
	}
	nonChunkedSize := nonChunkedInfo.Size()

	// Compress with chunking (GDELTA02 + FastCDC)
	chunkedPath := filepath.Join(tempDir, "chunked.gdelta")
	chunkedOpts := &Options{
		InputPath:  inputDir,
		OutputPath: chunkedPath,
		ChunkSize:  8 * 1024, // 8KB average chunk size
		Level:      5,
	}

	chunkedResult, err := Compress(chunkedOpts, nil)
	if err != nil {
		t.Fatalf("Chunked compression failed: %v", err)
	}

	chunkedInfo, err := os.Stat(chunkedPath)
	if err != nil {
		t.Fatalf("Failed to stat chunked archive: %v", err)
	}
	chunkedSize := chunkedInfo.Size()

	// Log results
	t.Logf("Original size:     %d bytes", totalOriginalSize)
	t.Logf("Non-chunked size:  %d bytes (%.1f%% of original)", nonChunkedSize, float64(nonChunkedSize)/float64(totalOriginalSize)*100)
	t.Logf("Chunked size:      %d bytes (%.1f%% of original)", chunkedSize, float64(chunkedSize)/float64(totalOriginalSize)*100)
	t.Logf("Chunked savings:   %d bytes (%.1f%% smaller than non-chunked)", nonChunkedSize-chunkedSize, float64(nonChunkedSize-chunkedSize)/float64(nonChunkedSize)*100)
	t.Logf("Dedup stats:       %d total chunks, %d unique, %d deduped (%.1f%% ratio)",
		chunkedResult.TotalChunks, chunkedResult.UniqueChunks, chunkedResult.DedupedChunks,
		float64(chunkedResult.DedupedChunks)/float64(chunkedResult.TotalChunks)*100)

	// Assert: chunked should be smaller than non-chunked for data with duplicates
	if chunkedSize >= nonChunkedSize {
		t.Errorf("Chunked archive (%d bytes) should be smaller than non-chunked (%d bytes) when files share content",
			chunkedSize, nonChunkedSize)
	}

	// Assert: deduplication should have occurred
	if chunkedResult.DedupedChunks == 0 {
		t.Error("Expected some chunks to be deduplicated")
	}

	// Assert: meaningful savings from deduplication
	// Even 5% is significant when dealing with large backup datasets
	savingsPercent := float64(nonChunkedSize-chunkedSize) / float64(nonChunkedSize) * 100
	if savingsPercent < 5 {
		t.Errorf("Expected at least 5%% size reduction from dedup, got %.1f%%", savingsPercent)
	}

	// Verify round-trip works
	outputDir := filepath.Join(tempDir, "output")
	decompressOpts := &decompress.Options{
		InputPath:  chunkedPath,
		OutputPath: outputDir,
	}

	_, err = decompress.Decompress(decompressOpts, nil)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	// Verify content integrity
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(outputDir, f.name))
		if err != nil {
			t.Fatalf("Failed to read %s: %v", f.name, err)
		}
		if !bytes.Equal(content, f.content) {
			t.Errorf("Content mismatch for %s", f.name)
		}
	}
}

// BenchmarkChunkedVsNonChunked compares compression performance and size
func BenchmarkChunkedVsNonChunked(b *testing.B) {
	tempDir := b.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	os.MkdirAll(inputDir, 0755)

	// Create pseudo-random test data with shared content
	baseContent := make([]byte, 100*1024)
	for i := range baseContent {
		baseContent[i] = byte((i*7 + i/256*13) % 256)
	}
	for i := 0; i < 5; i++ {
		prefix := bytes.Repeat([]byte{byte('A' + i)}, i*20)
		content := append(prefix, baseContent...)
		os.WriteFile(filepath.Join(inputDir, "file"+string(rune('1'+i))+".bin"), content, 0644)
	}

	b.Run("NonChunked", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			archivePath := filepath.Join(tempDir, "bench-nonchunked.gdelta")
			opts := &Options{
				InputPath:  inputDir,
				OutputPath: archivePath,
				ChunkSize:  0,
				Level:      3,
			}
			Compress(opts, nil)
			os.Remove(archivePath)
		}
	})

	b.Run("Chunked8KB", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			archivePath := filepath.Join(tempDir, "bench-chunked.gdelta")
			opts := &Options{
				InputPath:  inputDir,
				OutputPath: archivePath,
				ChunkSize:  8 * 1024,
				Level:      3,
			}
			Compress(opts, nil)
			os.Remove(archivePath)
		}
	})
}

func TestDryRunDedupStats(t *testing.T) {
	tempDir := t.TempDir()

	// Create files with duplicate content
	content := bytes.Repeat([]byte("ABCDEFGHIJ"), 10000) // 100KB of repeated content
	if err := os.WriteFile(filepath.Join(tempDir, "file1.txt"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "file2.txt"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "file3.txt"), content, 0644); err != nil {
		t.Fatal(err)
	}

	// Dry-run with chunking
	opts := &Options{
		InputPath:  tempDir,
		OutputPath: filepath.Join(tempDir, "test.gdelta"),
		ChunkSize:  64 * 1024, // 64KB chunks
		DryRun:     true,
		MaxThreads: 1, // Single thread for predictable results
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Dry-run failed: %v", err)
	}

	// Should have deduplication stats
	if result.TotalChunks == 0 {
		t.Error("TotalChunks should be > 0")
	}

	// With 3 identical files, we should see deduplication
	// Files 2 and 3 should deduplicate against file 1's chunks
	if result.DedupedChunks == 0 {
		t.Error("DedupedChunks should be > 0 for identical files")
	}

	// Dedup ratio should be significant (at least 50% since 2/3 files are duplicates)
	dedupRatio := float64(result.DedupedChunks) / float64(result.TotalChunks) * 100
	if dedupRatio < 50 {
		t.Errorf("Expected dedup ratio >= 50%%, got %.1f%%", dedupRatio)
	}

	t.Logf("Dry-run stats: %d total, %d unique, %d deduped (%.1f%% ratio)",
		result.TotalChunks, result.UniqueChunks, result.DedupedChunks, dedupRatio)

	// Verify no archive was created
	if _, err := os.Stat(opts.OutputPath); !os.IsNotExist(err) {
		t.Error("Dry-run should not create archive file")
	}
}
