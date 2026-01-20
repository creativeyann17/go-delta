// pkg/godelta/io.go
package godelta

import "io"

// ProgressWriter wraps an io.Writer with progress tracking
type ProgressWriter struct {
	Writer  io.Writer
	OnWrite func(n int)
}

func (pw *ProgressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.Writer.Write(p)
	if n > 0 && pw.OnWrite != nil {
		pw.OnWrite(n)
	}
	return n, err
}

// ProgressReader wraps an io.Reader with progress tracking
type ProgressReader struct {
	Reader io.Reader
	OnRead func(n int)
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	if n > 0 && pr.OnRead != nil {
		pr.OnRead(n)
	}
	return n, err
}

// CountingWriter wraps an io.Writer and counts bytes written
type CountingWriter struct {
	Writer io.Writer
	Count  int64
}

func (cw *CountingWriter) Write(p []byte) (n int, err error) {
	n, err = cw.Writer.Write(p)
	cw.Count += int64(n)
	return n, err
}

// DiscardCounter counts bytes written while discarding the data
type DiscardCounter struct {
	Count uint64
}

func (dc *DiscardCounter) Write(p []byte) (int, error) {
	dc.Count += uint64(len(p))
	return len(p), nil
}

// PathTracker tracks seen paths and detects duplicates
type PathTracker struct {
	seen map[string]bool
}

// NewPathTracker creates a new PathTracker
func NewPathTracker() *PathTracker {
	return &PathTracker{
		seen: make(map[string]bool),
	}
}

// CheckDuplicate returns true if the path was already seen, otherwise marks it as seen
func (pt *PathTracker) CheckDuplicate(path string) bool {
	if pt.seen[path] {
		return true
	}
	pt.seen[path] = true
	return false
}
