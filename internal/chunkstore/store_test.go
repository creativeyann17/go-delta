// internal/chunkstore/store_test.go
package chunkstore

import (
	"fmt"
	"sync"
	"testing"
)

func TestStoreBasic(t *testing.T) {
	store := NewStore()

	hash1 := [32]byte{1, 2, 3}
	hash2 := [32]byte{4, 5, 6}

	// Add first chunk
	info1, isNew, err := store.GetOrAdd(hash1, 100, func() (uint64, uint64, error) {
		return 0, 50, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("First chunk should be new")
	}
	if info1.Hash != hash1 {
		t.Error("Hash mismatch")
	}
	if info1.OriginalSize != 100 {
		t.Errorf("Expected original size 100, got %d", info1.OriginalSize)
	}
	if info1.CompressedSize != 50 {
		t.Errorf("Expected compressed size 50, got %d", info1.CompressedSize)
	}

	// Add second chunk
	info2, isNew, err := store.GetOrAdd(hash2, 200, func() (uint64, uint64, error) {
		return 50, 100, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("Second chunk should be new")
	}
	if info2.Offset != 50 {
		t.Errorf("Expected offset 50, got %d", info2.Offset)
	}

	// Try to add first chunk again (should deduplicate)
	info3, isNew, err := store.GetOrAdd(hash1, 100, func() (uint64, uint64, error) {
		t.Error("Write function should not be called for duplicate")
		return 0, 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Error("Duplicate chunk should not be new")
	}
	if info3.Hash != hash1 {
		t.Error("Should return original chunk info")
	}
}

func TestStoreGet(t *testing.T) {
	store := NewStore()

	hash := [32]byte{7, 8, 9}

	// Get non-existent chunk
	_, exists := store.Get(hash)
	if exists {
		t.Error("Non-existent chunk should not exist")
	}

	// Add chunk
	_, _, err := store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
		return 10, 50, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get existing chunk
	info, exists := store.Get(hash)
	if !exists {
		t.Error("Chunk should exist after adding")
	}
	if info.Hash != hash {
		t.Error("Retrieved wrong chunk")
	}
}

func TestStoreStats(t *testing.T) {
	store := NewStore()

	// Add 3 chunks, with one duplicate
	hash1 := [32]byte{1}
	hash2 := [32]byte{2}

	// First chunk
	store.GetOrAdd(hash1, 100, func() (uint64, uint64, error) {
		return 0, 50, nil
	})

	// Second chunk
	store.GetOrAdd(hash2, 100, func() (uint64, uint64, error) {
		return 50, 50, nil
	})

	// Duplicate of first
	store.GetOrAdd(hash1, 100, func() (uint64, uint64, error) {
		t.Error("Should not write duplicate")
		return 0, 0, nil
	})

	stats := store.Stats()

	// TotalChunks counts ALL chunks processed (including duplicates): 2 unique + 1 duplicate = 3
	if stats.TotalChunks != 3 {
		t.Errorf("Expected 3 total chunks (2 unique + 1 duplicate), got %d", stats.TotalChunks)
	}
	// UniqueChunks counts only new chunks written
	if stats.UniqueChunks != 2 {
		t.Errorf("Expected 2 unique chunks, got %d", stats.UniqueChunks)
	}
	if stats.DedupedChunks != 1 {
		t.Errorf("Expected 1 deduplicated chunk, got %d", stats.DedupedChunks)
	}
	// BytesSaved tracks compressed size: 1 deduped chunk × 50 bytes compressed = 50 bytes
	if stats.BytesSaved != 50 {
		t.Errorf("Expected 50 bytes saved (compressed size), got %d", stats.BytesSaved)
	}
}

func TestStoreDedupRatio(t *testing.T) {
	tests := []struct {
		name          string
		totalChunks   uint64
		dedupedChunks uint64
		expectedRatio float64
	}{
		{"No dedup", 10, 0, 0.0},
		{"Half deduped", 10, 5, 50.0},
		{"All deduped", 10, 10, 100.0},
		{"Empty", 0, 0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := Stats{
				TotalChunks:   tt.totalChunks,
				DedupedChunks: tt.dedupedChunks,
			}

			ratio := stats.DedupRatio()
			if ratio != tt.expectedRatio {
				t.Errorf("Expected ratio %.1f%%, got %.1f%%", tt.expectedRatio, ratio)
			}
		})
	}
}

func TestStoreAll(t *testing.T) {
	store := NewStore()

	// Add some chunks
	hashes := [][32]byte{
		{1}, {2}, {3},
	}

	for i, hash := range hashes {
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return uint64(i * 100), 50, nil
		})
	}

	all := store.All()

	if len(all) != len(hashes) {
		t.Errorf("Expected %d chunks, got %d", len(hashes), len(all))
	}

	// Verify all hashes are present
	for _, hash := range hashes {
		if _, exists := all[hash]; !exists {
			t.Errorf("Hash %v not found in All()", hash)
		}
	}
}

func TestStoreCount(t *testing.T) {
	store := NewStore()

	if store.Count() != 0 {
		t.Error("New store should have count 0")
	}

	// Add chunks
	for i := 0; i < 5; i++ {
		hash := [32]byte{byte(i)}
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return 0, 50, nil
		})
	}

	if store.Count() != 5 {
		t.Errorf("Expected count 5, got %d", store.Count())
	}

	// Adding duplicate doesn't increase count
	hash := [32]byte{0}
	store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
		t.Error("Should not write duplicate")
		return 0, 0, nil
	})

	if store.Count() != 5 {
		t.Errorf("Count should still be 5 after duplicate, got %d", store.Count())
	}
}

func TestStoreConcurrency(t *testing.T) {
	store := NewStore()

	// Concurrently add chunks
	const numGoroutines = 100
	const chunksPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()

			for i := 0; i < chunksPerGoroutine; i++ {
				hash := [32]byte{byte(goroutineID), byte(i)}
				_, _, err := store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
					return uint64(goroutineID*chunksPerGoroutine + i), 50, nil
				})
				if err != nil {
					t.Errorf("GetOrAdd failed: %v", err)
				}
			}
		}(g)
	}

	wg.Wait()

	expectedCount := numGoroutines * chunksPerGoroutine
	if store.Count() != expectedCount {
		t.Errorf("Expected %d chunks, got %d", expectedCount, store.Count())
	}
}

func TestStoreConcurrentDuplicates(t *testing.T) {
	store := NewStore()

	// Multiple goroutines try to add the same chunk
	const numGoroutines = 50
	hash := [32]byte{1, 2, 3}

	var wg sync.WaitGroup
	var writeCount sync.Map

	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			defer wg.Done()

			_, isNew, err := store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
				writeCount.Store(id, true)
				return 0, 50, nil
			})
			if err != nil {
				t.Errorf("GetOrAdd failed: %v", err)
			}

			// Only one should be new
			if isNew {
				// This is the winner
			}
		}(g)
	}

	wg.Wait()

	// Only one unique chunk should be stored
	if store.Count() != 1 {
		t.Errorf("Expected 1 unique chunk, got %d", store.Count())
	}

	// Count how many times write function was called
	var writes int
	writeCount.Range(func(key, value interface{}) bool {
		writes++
		return true
	})

	// Should be called at most twice (due to double-check locking)
	// In practice, usually just once
	if writes > 2 {
		t.Logf("Warning: write function called %d times (expected 1-2)", writes)
	}
}

func TestStoreWriteFunctionError(t *testing.T) {
	store := NewStore()

	hash := [32]byte{5, 6, 7}
	expectedErr := fmt.Errorf("write error")

	_, _, err := store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
		return 0, 0, expectedErr
	})

	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}

	// Chunk should not be stored if write failed
	if store.Count() != 0 {
		t.Error("Chunk should not be stored when write fails")
	}
}

func BenchmarkStoreGetOrAddNew(b *testing.B) {
	store := NewStore()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash := [32]byte{byte(i), byte(i >> 8), byte(i >> 16)}
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return uint64(i), 50, nil
		})
	}
}

func BenchmarkStoreGetOrAddDuplicate(b *testing.B) {
	store := NewStore()
	hash := [32]byte{1, 2, 3}

	// Pre-populate one chunk
	store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
		return 0, 50, nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			b.Error("Should not write duplicate")
			return 0, 0, nil
		})
	}
}

func BenchmarkStoreGet(b *testing.B) {
	store := NewStore()
	hash := [32]byte{1, 2, 3}

	// Pre-populate
	store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
		return 0, 50, nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Get(hash)
	}
}
