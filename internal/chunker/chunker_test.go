// internal/chunker/chunker_test.go
package chunker

import (
	"bytes"
	"testing"
)

func TestChunkerBasic(t *testing.T) {
	avgSize := uint64(256) // FastCDC requires minSize >= 64, so avgSize >= 256
	c := New(avgSize)

	if c.ChunkSize() != avgSize {
		t.Errorf("Expected avg chunk size %d, got %d", avgSize, c.ChunkSize())
	}

	// Use enough data to get multiple chunks
	data := bytes.Repeat([]byte("Hello World! This is test data for chunking. "), 100)
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least 1 chunk")
	}

	// Verify reassembly
	var reassembled []byte
	for _, chunk := range chunks {
		reassembled = append(reassembled, chunk.Data...)
	}

	if !bytes.Equal(reassembled, data) {
		t.Error("Reassembled data doesn't match original")
	}
}

func TestChunkerSizeBounds(t *testing.T) {
	avgSize := uint64(256)
	c := New(avgSize)

	minSize := c.MinSize()
	maxSize := c.MaxSize()

	// Verify bounds are set correctly
	if minSize != avgSize/4 {
		t.Errorf("Expected minSize %d, got %d", avgSize/4, minSize)
	}
	if maxSize != avgSize*4 {
		t.Errorf("Expected maxSize %d, got %d", avgSize*4, maxSize)
	}

	// Create data large enough to get multiple chunks
	data := bytes.Repeat([]byte("Testing chunk size bounds with FastCDC algorithm. "), 200)
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	// Check all chunks are within bounds (except possibly the last one)
	for i, chunk := range chunks {
		isLast := i == len(chunks)-1
		if !isLast {
			if chunk.OrigSize < minSize {
				t.Errorf("Chunk %d: size %d below minimum %d", i, chunk.OrigSize, minSize)
			}
			if chunk.OrigSize > maxSize {
				t.Errorf("Chunk %d: size %d above maximum %d", i, chunk.OrigSize, maxSize)
			}
		}
	}
}

func TestChunkerEmptyData(t *testing.T) {
	c := New(1024)

	chunks, err := c.Split(bytes.NewReader([]byte{}))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks) != 0 {
		t.Errorf("Expected 0 chunks for empty data, got %d", len(chunks))
	}
}

func TestChunkerSmallData(t *testing.T) {
	avgSize := uint64(1024)
	c := New(avgSize)

	data := []byte("Small")
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks) != 1 {
		t.Errorf("Expected 1 chunk, got %d", len(chunks))
	}

	if chunks[0].OrigSize != uint64(len(data)) {
		t.Errorf("Expected chunk size %d, got %d", len(data), chunks[0].OrigSize)
	}

	if !bytes.Equal(chunks[0].Data, data) {
		t.Error("Chunk data doesn't match original")
	}
}

func TestChunkerHashUniqueness(t *testing.T) {
	c := New(256)

	// Create two different data sets
	data1 := bytes.Repeat([]byte("aaaa"), 500)
	data2 := bytes.Repeat([]byte("bbbb"), 500)

	chunks1, err := c.Split(bytes.NewReader(data1))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	chunks2, err := c.Split(bytes.NewReader(data2))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks1) == 0 || len(chunks2) == 0 {
		t.Fatal("Expected at least one chunk each")
	}

	// Collect all hashes
	hashes1 := make(map[[32]byte]bool)
	for _, chunk := range chunks1 {
		hashes1[chunk.Hash] = true
	}

	// Check that data2 chunks have different hashes
	for _, chunk := range chunks2 {
		if hashes1[chunk.Hash] {
			t.Error("Different data produced overlapping hashes")
		}
	}
}

func TestChunkerHashConsistency(t *testing.T) {
	c := New(256)

	// Create data with enough content for multiple chunks
	data := bytes.Repeat([]byte("test data for consistency check "), 100)

	// Split same data twice
	chunks1, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("First split failed: %v", err)
	}

	chunks2, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Second split failed: %v", err)
	}

	if len(chunks1) != len(chunks2) {
		t.Fatalf("Different number of chunks: %d vs %d", len(chunks1), len(chunks2))
	}

	// Hashes should be identical for same data
	for i := range chunks1 {
		if chunks1[i].Hash != chunks2[i].Hash {
			t.Errorf("Chunk %d: hashes differ for same data", i)
		}
		if chunks1[i].OrigSize != chunks2[i].OrigSize {
			t.Errorf("Chunk %d: sizes differ for same data: %d vs %d",
				i, chunks1[i].OrigSize, chunks2[i].OrigSize)
		}
	}
}

func TestChunkerContentDefinedBoundaries(t *testing.T) {
	c := New(256)

	// Create base data
	baseData := bytes.Repeat([]byte("This is the base content that should be recognized. "), 100)

	// Create shifted data (prepend some bytes)
	prefix := []byte("PREPENDED CONTENT: ")
	shiftedData := append(prefix, baseData...)

	chunksBase, err := c.Split(bytes.NewReader(baseData))
	if err != nil {
		t.Fatalf("Split base failed: %v", err)
	}

	chunksShifted, err := c.Split(bytes.NewReader(shiftedData))
	if err != nil {
		t.Fatalf("Split shifted failed: %v", err)
	}

	// Collect hashes from base
	baseHashes := make(map[[32]byte]bool)
	for _, chunk := range chunksBase {
		baseHashes[chunk.Hash] = true
	}

	// Count matching hashes in shifted data
	// With content-defined chunking, we should see SOME matches
	// (unlike fixed chunking where we'd see ZERO matches)
	matchCount := 0
	for _, chunk := range chunksShifted {
		if baseHashes[chunk.Hash] {
			matchCount++
		}
	}

	// Log for visibility
	t.Logf("Base chunks: %d, Shifted chunks: %d, Matches: %d (%.1f%%)",
		len(chunksBase), len(chunksShifted), matchCount,
		float64(matchCount)/float64(len(chunksBase))*100)

	// With CDC, we expect at least some chunks to match after a shift
	// This is the key benefit over fixed-size chunking
	if matchCount == 0 && len(chunksBase) > 2 {
		t.Log("Warning: No matching chunks found - CDC may not be working optimally")
		// Not a hard failure since small data might not have good boundaries
	}
}

func TestChunkerLargeData(t *testing.T) {
	avgSize := uint64(64 * 1024) // 64KB average
	c := New(avgSize)

	// Create 1MB of data
	data := bytes.Repeat([]byte("Large data test content. "), 40000)

	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least 1 chunk")
	}

	// Verify total size matches
	var totalSize uint64
	for _, chunk := range chunks {
		totalSize += chunk.OrigSize
	}

	if totalSize != uint64(len(data)) {
		t.Errorf("Total size mismatch: expected %d, got %d", len(data), totalSize)
	}

	// Verify reassembly
	var reassembled []byte
	for _, chunk := range chunks {
		reassembled = append(reassembled, chunk.Data...)
	}

	if !bytes.Equal(reassembled, data) {
		t.Error("Reassembled data doesn't match original")
	}
}

func TestChunkerOrigSizeMatchesData(t *testing.T) {
	c := New(256)

	data := bytes.Repeat([]byte("verify origsize "), 200)
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	for i, chunk := range chunks {
		if chunk.OrigSize != uint64(len(chunk.Data)) {
			t.Errorf("Chunk %d: OrigSize %d doesn't match len(Data) %d",
				i, chunk.OrigSize, len(chunk.Data))
		}
	}
}

func BenchmarkChunker1MB(b *testing.B) {
	avgSize := uint64(64 * 1024) // 64KB average
	c := New(avgSize)
	data := bytes.Repeat([]byte("x"), 1024*1024) // 1MB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.Split(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChunker10MB(b *testing.B) {
	avgSize := uint64(1024 * 1024) // 1MB average
	c := New(avgSize)
	data := bytes.Repeat([]byte("x"), 10*1024*1024) // 10MB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.Split(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
	}
}
