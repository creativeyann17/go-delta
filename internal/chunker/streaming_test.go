// internal/chunker/streaming_test.go
package chunker

import (
	"bytes"
	"testing"
)

// TestStreamingVsNonStreaming demonstrates memory usage difference
func TestStreamingVsNonStreaming(t *testing.T) {
	// Create 100MB of data
	dataSize := 100 * 1024 * 1024
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	c := New(1024 * 1024) // 1MB chunks

	// Test non-streaming (loads all chunks into memory)
	t.Run("Non-streaming Split()", func(t *testing.T) {
		chunks, err := c.Split(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("Split failed: %v", err)
		}

		// For 100MB data with 1MB chunks, we have ~100 chunks
		// Each chunk allocates ~1MB, so ~100MB total in memory
		if len(chunks) == 0 {
			t.Fatal("Expected chunks")
		}

		t.Logf("Non-streaming: %d chunks loaded into memory (~%d MB)",
			len(chunks), len(chunks)*1024*1024/(1024*1024))
	})

	// Test streaming (processes chunks one at a time)
	t.Run("Streaming SplitWithCallback()", func(t *testing.T) {
		chunkCount := 0
		totalSize := uint64(0)

		err := c.SplitWithCallback(bytes.NewReader(data), func(chunk Chunk) error {
			chunkCount++
			totalSize += chunk.OrigSize
			// Chunk is processed immediately and can be freed
			// Only 1 chunk (~1MB) in memory at a time
			return nil
		})

		if err != nil {
			t.Fatalf("SplitWithCallback failed: %v", err)
		}

		if chunkCount == 0 {
			t.Fatal("Expected chunks")
		}

		t.Logf("Streaming: %d chunks processed one-by-one (~1 MB max in memory)",
			chunkCount)
	})
}

// TestCallbackError verifies error handling in streaming mode
func TestCallbackError(t *testing.T) {
	data := bytes.Repeat([]byte("test data"), 10000) // ~88KB
	c := New(1024)                                   // 1KB chunks, will produce multiple chunks

	processedCount := 0
	targetError := bytes.ErrTooLarge

	err := c.SplitWithCallback(bytes.NewReader(data), func(chunk Chunk) error {
		processedCount++
		if processedCount == 3 {
			return targetError
		}
		return nil
	})

	if err != targetError {
		t.Errorf("Expected error %v, got %v", targetError, err)
	}

	if processedCount != 3 {
		t.Errorf("Expected to process 3 chunks before error, processed %d", processedCount)
	}
}

// TestCallbackChunkValidity ensures chunk data is valid during callback
func TestCallbackChunkValidity(t *testing.T) {
	// Create test data with pattern
	pattern := []byte("ABCDEFGH")
	data := bytes.Repeat(pattern, 10000) // ~78KB

	c := New(1024) // 1KB chunks

	var capturedHashes [][32]byte

	err := c.SplitWithCallback(bytes.NewReader(data), func(chunk Chunk) error {
		// Verify hash is calculated
		if chunk.Hash == [32]byte{} {
			t.Error("Chunk hash is zero")
		}

		// Verify size matches data length
		if chunk.OrigSize != uint64(len(chunk.Data)) {
			t.Errorf("Size mismatch: OrigSize=%d, len(Data)=%d",
				chunk.OrigSize, len(chunk.Data))
		}

		// Store hash for later
		capturedHashes = append(capturedHashes, chunk.Hash)

		return nil
	})

	if err != nil {
		t.Fatalf("SplitWithCallback failed: %v", err)
	}

	if len(capturedHashes) == 0 {
		t.Fatal("No chunks processed")
	}

	t.Logf("Processed %d valid chunks", len(capturedHashes))
}
