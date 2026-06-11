// internal/chunkstore/store.go
package chunkstore

import (
	"container/list"
	"sync"
	"sync/atomic"

	"github.com/creativeyann17/go-delta/internal/format"
)

// ChunkInfo is an alias for format.ChunkInfo for convenience
type ChunkInfo = format.ChunkInfo

// chunkEntry tracks chunk info with LRU metadata
type chunkEntry struct {
	info     ChunkInfo
	refCount uint64 // Number of times chunk was referenced
	lruNode  *list.Element
}

// inflightChunk tracks a chunk currently being written by another goroutine,
// so concurrent writers of the same hash wait instead of compressing twice
// (a duplicate write would leave orphan bytes in the archive).
type inflightChunk struct {
	done chan struct{} // closed when the write finishes
	info ChunkInfo     // valid after done is closed, if err == nil
	err  error
}

// Store maintains a thread-safe map of chunks for deduplication with bounded capacity
type Store struct {
	mu        sync.RWMutex
	chunks    map[[32]byte]*chunkEntry    // LRU cache for dedup lookups
	allChunks map[[32]byte]ChunkInfo      // Complete index, never evicted
	inflight  map[[32]byte]*inflightChunk // Writes in progress
	lruList   *list.List                  // LRU list of hash keys
	maxChunks int                         // Maximum chunks to keep in memory (0 = unlimited)

	// Statistics
	totalChunks   atomic.Uint64
	uniqueChunks  atomic.Uint64
	dedupedChunks atomic.Uint64
	bytesSaved    atomic.Uint64
	evictions     atomic.Uint64 // Chunks evicted due to capacity
}

// NewStore creates a new chunk store with unlimited capacity
func NewStore() *Store {
	return NewStoreWithCapacity(0)
}

// NewStoreWithCapacity creates a chunk store with a maximum capacity
// maxChunks: maximum number of chunks to keep (0 = unlimited)
func NewStoreWithCapacity(maxChunks int) *Store {
	return &Store{
		chunks:    make(map[[32]byte]*chunkEntry),
		allChunks: make(map[[32]byte]ChunkInfo), // Never evicted
		inflight:  make(map[[32]byte]*inflightChunk),
		lruList:   list.New(),
		maxChunks: maxChunks,
	}
}

// GetOrAdd checks if a chunk exists, and if not, calls writeFunc to store it
// Returns (ChunkInfo, isNew, error)
// If isNew=false, the chunk was deduplicated
func (s *Store) GetOrAdd(hash [32]byte, origSize uint64, writeFunc func() (offset uint64, comprSize uint64, err error)) (ChunkInfo, bool, error) {
	// Always count total chunks processed
	s.totalChunks.Add(1)

	for {
		s.mu.Lock()

		// Check LRU cache
		if entry, exists := s.chunks[hash]; exists {
			entry.refCount++
			s.lruList.MoveToFront(entry.lruNode)
			info := entry.info
			s.mu.Unlock()

			s.dedupedChunks.Add(1)
			// Track compressed bytes saved, not original bytes
			s.bytesSaved.Add(info.CompressedSize)
			return info, false, nil
		}

		// Check permanent index (evicted from LRU but data already in archive)
		if info, exists := s.allChunks[hash]; exists {
			s.mu.Unlock()

			s.dedupedChunks.Add(1)
			s.bytesSaved.Add(info.CompressedSize)
			return info, false, nil
		}

		// Another goroutine is writing this chunk right now: wait for it
		// instead of compressing and writing the same data twice.
		if fl, exists := s.inflight[hash]; exists {
			s.mu.Unlock()
			<-fl.done
			if fl.err == nil {
				s.dedupedChunks.Add(1)
				s.bytesSaved.Add(fl.info.CompressedSize)
				return fl.info, false, nil
			}
			// The writer failed; retry the whole lookup/write.
			continue
		}

		// Chunk doesn't exist anywhere: register as in-flight and write it
		fl := &inflightChunk{done: make(chan struct{})}
		s.inflight[hash] = fl
		s.mu.Unlock()

		offset, comprSize, err := writeFunc()

		s.mu.Lock()
		delete(s.inflight, hash)
		if err != nil {
			s.mu.Unlock()
			fl.err = err
			close(fl.done)
			return ChunkInfo{}, false, err
		}

		info := ChunkInfo{
			Hash:           hash,
			Offset:         offset,
			CompressedSize: comprSize,
			OriginalSize:   origSize,
		}

		// Add to permanent index (never evicted)
		s.allChunks[hash] = info

		// Evict LRU chunk if at capacity (only from cache, not from allChunks)
		if s.maxChunks > 0 && len(s.chunks) >= s.maxChunks {
			s.evictLRU()
		}

		// Add new chunk to LRU cache
		lruNode := s.lruList.PushFront(hash)
		s.chunks[hash] = &chunkEntry{
			info:     info,
			refCount: 1,
			lruNode:  lruNode,
		}
		s.uniqueChunks.Add(1)
		s.mu.Unlock()

		fl.info = info
		close(fl.done)
		return info, true, nil
	}
}

// evictLRU removes the least recently used chunk
// Must be called with write lock held
func (s *Store) evictLRU() {
	if s.lruList.Len() == 0 {
		return
	}

	// Get LRU chunk (back of list)
	back := s.lruList.Back()
	if back == nil {
		return
	}

	hash := back.Value.([32]byte)
	delete(s.chunks, hash)
	s.lruList.Remove(back)
	s.evictions.Add(1)
}

// Get retrieves chunk info by hash (read-only)
func (s *Store) Get(hash [32]byte) (ChunkInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if entry, exists := s.chunks[hash]; exists {
		return entry.info, true
	}
	return ChunkInfo{}, false
}

// All returns all chunks ever seen (including evicted ones)
// This is critical: evicted chunks are removed from s.chunks but their
// metadata (hash, offset, sizes) must be preserved for the archive index
func (s *Store) All() map[[32]byte]ChunkInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return all chunks including those evicted from the LRU cache
	// The allChunks map is never evicted, so it contains complete metadata
	result := make(map[[32]byte]ChunkInfo, len(s.allChunks))
	for k, info := range s.allChunks {
		result[k] = info
	}
	return result
}

// Count returns the number of unique chunks
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.chunks)
}

// Stats returns deduplication statistics
func (s *Store) Stats() Stats {
	return Stats{
		TotalChunks:   s.totalChunks.Load(),
		UniqueChunks:  s.uniqueChunks.Load(),
		DedupedChunks: s.dedupedChunks.Load(),
		BytesSaved:    s.bytesSaved.Load(),
		Evictions:     s.evictions.Load(),
	}
}

// Stats contains deduplication statistics
type Stats struct {
	TotalChunks   uint64 // Total chunks processed
	UniqueChunks  uint64 // Unique chunks stored
	DedupedChunks uint64 // Chunks that were deduplicated
	BytesSaved    uint64 // Bytes saved through deduplication
	Evictions     uint64 // Chunks evicted from store due to capacity limit
}

// DedupRatio returns the deduplication ratio as a percentage
func (s Stats) DedupRatio() float64 {
	if s.TotalChunks == 0 {
		return 0
	}
	return float64(s.DedupedChunks) / float64(s.TotalChunks) * 100
}
