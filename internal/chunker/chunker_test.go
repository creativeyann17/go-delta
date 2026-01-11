// internal/chunker/chunker_test.go
package chunker

import (
	"bytes"
	"fmt"
	"testing"
)

func TestChunkerBasic(t *testing.T) {
	chunkSize := uint64(10)
	c := New(chunkSize)

	if c.ChunkSize() != chunkSize {
		t.Errorf("Expected chunk size %d, got %d", chunkSize, c.ChunkSize())
	}

	data := []byte("Hello World! This is a test.")
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	expectedChunks := 3 // 28 bytes / 10 = 3 chunks
	if len(chunks) != expectedChunks {
		t.Errorf("Expected %d chunks, got %d", expectedChunks, len(chunks))
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

func TestChunkerExactSize(t *testing.T) {
	chunkSize := uint64(100)
	c := New(chunkSize)

	data := bytes.Repeat([]byte("x"), 100) // Exactly one chunk
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks) != 1 {
		t.Errorf("Expected 1 chunk for exact size, got %d", len(chunks))
	}

	if chunks[0].OrigSize != chunkSize {
		t.Errorf("Expected chunk size %d, got %d", chunkSize, chunks[0].OrigSize)
	}
}

func TestChunkerMultipleExactChunks(t *testing.T) {
	chunkSize := uint64(50)
	c := New(chunkSize)

	data := bytes.Repeat([]byte("x"), 150) // Exactly 3 chunks
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks) != 3 {
		t.Errorf("Expected 3 chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		if chunk.OrigSize != chunkSize {
			t.Errorf("Chunk %d: expected size %d, got %d", i, chunkSize, chunk.OrigSize)
		}
	}
}

func TestChunkerPartialLastChunk(t *testing.T) {
	chunkSize := uint64(100)
	c := New(chunkSize)

	data := bytes.Repeat([]byte("x"), 250) // 2 full chunks + 50 byte partial
	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	if len(chunks) != 3 {
		t.Errorf("Expected 3 chunks, got %d", len(chunks))
	}

	// First two should be full size
	for i := 0; i < 2; i++ {
		if chunks[i].OrigSize != chunkSize {
			t.Errorf("Chunk %d: expected size %d, got %d", i, chunkSize, chunks[i].OrigSize)
		}
	}

	// Last chunk should be partial
	if chunks[2].OrigSize != 50 {
		t.Errorf("Last chunk: expected size 50, got %d", chunks[2].OrigSize)
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

func TestChunkerSmallerThanChunkSize(t *testing.T) {
	chunkSize := uint64(1024)
	c := New(chunkSize)

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
	c := New(100)

	data1 := bytes.Repeat([]byte("a"), 100)
	data2 := bytes.Repeat([]byte("b"), 100)

	chunks1, err := c.Split(bytes.NewReader(data1))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	chunks2, err := c.Split(bytes.NewReader(data2))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	// Hashes should be different for different data
	if chunks1[0].Hash == chunks2[0].Hash {
		t.Error("Different data produced same hash")
	}
}

func TestChunkerHashConsistency(t *testing.T) {
	c := New(100)

	data := bytes.Repeat([]byte("test data"), 20)

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

	// Hashes should be identical
	for i := range chunks1 {
		if chunks1[i].Hash != chunks2[i].Hash {
			t.Errorf("Chunk %d: hashes differ for same data", i)
		}
	}
}

func TestChunkerLargeData(t *testing.T) {
	chunkSize := uint64(64 * 1024) // 64KB chunks
	c := New(chunkSize)

	// Create 1MB of data
	data := bytes.Repeat([]byte("Large data test. "), 64*1024)

	chunks, err := c.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split failed: %v", err)
	}

	expectedChunks := len(data) / int(chunkSize)
	if len(data)%int(chunkSize) != 0 {
		expectedChunks++
	}

	if len(chunks) != expectedChunks {
		t.Errorf("Expected %d chunks, got %d", expectedChunks, len(chunks))
	}

	// Verify total size
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

func TestChunkerReadError(t *testing.T) {
	c := New(100)

	// Create a reader that returns a real error (not EOF-related)
	testErr := fmt.Errorf("simulated read error")
	errorReader := &errorReaderImpl{err: testErr}

	_, err := c.Split(errorReader)
	if err == nil {
		t.Error("Expected error from reader, got nil")
	}
	if err != testErr {
		t.Errorf("Expected error %v, got %v", testErr, err)
	}
}

// errorReaderImpl is a test helper that returns an error after some bytes
type errorReaderImpl struct {
	err error
}

func (e *errorReaderImpl) Read(p []byte) (n int, err error) {
	return 0, e.err
}

func BenchmarkChunker1MB(b *testing.B) {
	chunkSize := uint64(64 * 1024) // 64KB chunks
	c := New(chunkSize)
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
	chunkSize := uint64(1024 * 1024) // 1MB chunks
	c := New(chunkSize)
	data := bytes.Repeat([]byte("x"), 10*1024*1024) // 10MB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.Split(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
	}
}
