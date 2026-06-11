// pkg/compress/pools.go
package compress

import "sync"

// readBufferSize is the file IO buffer size. Large enough to keep syscall
// count low on multi-MB files without hurting many-small-file workloads
// (buffers are pooled, not per-file allocations).
const readBufferSize = 256 * 1024

var readBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, readBufferSize)
		return &buf
	},
}

// getReadBuffer returns a read buffer from the pool
func getReadBuffer() []byte {
	return *readBufferPool.Get().(*[]byte)
}

// putReadBuffer returns a buffer to the pool
func putReadBuffer(buf []byte) {
	readBufferPool.Put(&buf)
}
