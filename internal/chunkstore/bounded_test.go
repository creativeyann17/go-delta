// internal/chunkstore/bounded_test.go
package chunkstore

import (
	"testing"
)

func TestBoundedStoreCapacity(t *testing.T) {
	// Create store with capacity of 3 chunks
	store := NewStoreWithCapacity(3)

	// Add 3 chunks
	for i := 0; i < 3; i++ {
		hash := [32]byte{byte(i)}
		_, isNew, err := store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return uint64(i * 100), 50, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !isNew {
			t.Errorf("Chunk %d should be new", i)
		}
	}

	if store.Count() != 3 {
		t.Errorf("Expected 3 chunks, got %d", store.Count())
	}

	// Add 4th chunk - should evict LRU (chunk 0)
	hash3 := [32]byte{3}
	_, isNew, err := store.GetOrAdd(hash3, 100, func() (uint64, uint64, error) {
		return 300, 50, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("4th chunk should be new")
	}

	// Store should still have 3 chunks
	if store.Count() != 3 {
		t.Errorf("Expected 3 chunks after eviction, got %d", store.Count())
	}

	// Check eviction counter
	stats := store.Stats()
	if stats.Evictions != 1 {
		t.Errorf("Expected 1 eviction, got %d", stats.Evictions)
	}

	// Chunk 0 should be evicted
	hash0 := [32]byte{0}
	_, exists := store.Get(hash0)
	if exists {
		t.Error("Chunk 0 should have been evicted")
	}

	// Chunks 1, 2, 3 should still exist
	for i := 1; i <= 3; i++ {
		hash := [32]byte{byte(i)}
		_, exists := store.Get(hash)
		if !exists {
			t.Errorf("Chunk %d should still exist", i)
		}
	}
}

func TestBoundedStoreLRU(t *testing.T) {
	// Create store with capacity of 2 chunks
	store := NewStoreWithCapacity(2)

	hash0 := [32]byte{0}
	hash1 := [32]byte{1}
	hash2 := [32]byte{2}

	// Add chunks 0 and 1
	store.GetOrAdd(hash0, 100, func() (uint64, uint64, error) {
		return 0, 50, nil
	})
	store.GetOrAdd(hash1, 100, func() (uint64, uint64, error) {
		return 50, 50, nil
	})

	// Access chunk 0 (makes it MRU)
	store.GetOrAdd(hash0, 100, func() (uint64, uint64, error) {
		t.Error("Should not write - chunk 0 should exist")
		return 0, 0, nil
	})

	// Add chunk 2 - should evict chunk 1 (LRU), not chunk 0
	store.GetOrAdd(hash2, 100, func() (uint64, uint64, error) {
		return 100, 50, nil
	})

	// Chunk 1 should be evicted
	_, exists := store.Get(hash1)
	if exists {
		t.Error("Chunk 1 should have been evicted (LRU)")
	}

	// Chunk 0 should still exist (was accessed recently)
	_, exists = store.Get(hash0)
	if !exists {
		t.Error("Chunk 0 should still exist (MRU)")
	}

	// Chunk 2 should exist
	_, exists = store.Get(hash2)
	if !exists {
		t.Error("Chunk 2 should exist")
	}
}

func TestBoundedStoreUnlimited(t *testing.T) {
	// Store with maxChunks=0 should be unlimited
	store := NewStoreWithCapacity(0)

	// Add many chunks
	for i := 0; i < 100; i++ {
		hash := [32]byte{byte(i)}
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return uint64(i * 100), 50, nil
		})
	}

	// All chunks should still exist
	if store.Count() != 100 {
		t.Errorf("Expected 100 chunks, got %d", store.Count())
	}

	// No evictions
	stats := store.Stats()
	if stats.Evictions != 0 {
		t.Errorf("Expected 0 evictions, got %d", stats.Evictions)
	}
}

func TestBoundedStoreRefCount(t *testing.T) {
	store := NewStoreWithCapacity(2)

	hash0 := [32]byte{0}
	hash1 := [32]byte{1}

	// Add chunk 0
	store.GetOrAdd(hash0, 100, func() (uint64, uint64, error) {
		return 0, 50, nil
	})

	// Add chunk 1
	store.GetOrAdd(hash1, 100, func() (uint64, uint64, error) {
		return 50, 50, nil
	})

	// Access chunk 0 multiple times (increases refcount)
	for i := 0; i < 5; i++ {
		info, isNew, _ := store.GetOrAdd(hash0, 100, func() (uint64, uint64, error) {
			t.Error("Should not write")
			return 0, 0, nil
		})
		if isNew {
			t.Error("Chunk should not be new on repeated access")
		}
		if info.Hash != hash0 {
			t.Error("Should return chunk 0")
		}
	}

	// Dedup stats should show 5 deduplicated chunks
	stats := store.Stats()
	if stats.DedupedChunks != 5 {
		t.Errorf("Expected 5 deduplicated chunks, got %d", stats.DedupedChunks)
	}
	// BytesSaved tracks compressed size: 5 deduped chunks × 50 bytes compressed = 250 bytes
	if stats.BytesSaved != 250 {
		t.Errorf("Expected 250 bytes saved (compressed size), got %d", stats.BytesSaved)
	}
}

func TestBoundedStoreEvictionStats(t *testing.T) {
	store := NewStoreWithCapacity(2)

	// Add 5 chunks - should cause 3 evictions
	for i := 0; i < 5; i++ {
		hash := [32]byte{byte(i)}
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return uint64(i * 100), 50, nil
		})
	}

	stats := store.Stats()

	// Should have evicted 3 chunks (5 added - 2 capacity)
	if stats.Evictions != 3 {
		t.Errorf("Expected 3 evictions, got %d", stats.Evictions)
	}

	// Should have 2 chunks in store
	if store.Count() != 2 {
		t.Errorf("Expected 2 chunks in store, got %d", store.Count())
	}

	// Should have processed 5 total chunks
	if stats.TotalChunks != 5 {
		t.Errorf("Expected 5 total chunks, got %d", stats.TotalChunks)
	}

	// Should have 5 unique chunks (all were unique before eviction)
	if stats.UniqueChunks != 5 {
		t.Errorf("Expected 5 unique chunks, got %d", stats.UniqueChunks)
	}
}

func BenchmarkBoundedStoreWithEviction(b *testing.B) {
	store := NewStoreWithCapacity(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash := [32]byte{byte(i), byte(i >> 8), byte(i >> 16)}
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return uint64(i), 50, nil
		})
	}
}

func BenchmarkUnboundedStore(b *testing.B) {
	store := NewStoreWithCapacity(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash := [32]byte{byte(i), byte(i >> 8), byte(i >> 16)}
		store.GetOrAdd(hash, 100, func() (uint64, uint64, error) {
			return uint64(i), 50, nil
		})
	}
}
