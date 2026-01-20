// pkg/compress/pools.go
package compress

import "sync"

// Buffer pools for reduced allocations during compression.
// These pools are used when DisableGC is enabled to minimize heap allocations.

var (
	// readBufferPool provides 32KB read buffers for file I/O
	readBufferPool = sync.Pool{
		New: func() any {
			buf := make([]byte, 32*1024)
			return &buf
		},
	}
)

// getReadBuffer returns a 32KB buffer from the pool
func getReadBuffer() []byte {
	return *readBufferPool.Get().(*[]byte)
}

// putReadBuffer returns a buffer to the pool
func putReadBuffer(buf []byte) {
	readBufferPool.Put(&buf)
}
