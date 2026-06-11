// pkg/compress/compress_zip.go
package compress

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/flate"
)

// progressReportStep is the minimum number of bytes between two
// EventFileProgress emissions. Reporting on every 32KB read is measurably
// expensive on fast disks; 1 MiB keeps bars smooth at a fraction of the cost.
const progressReportStep = 1 << 20

// compressToZip compresses files into multiple ZIP archives (one per thread) for true parallelism
// Output: archive_01.zip, archive_02.zip, ..., archive_N.zip
func compressToZip(opts *Options, progressCb ProgressCallback, foldersToCompress []folderTask, totalFiles int, totalOrigSize uint64, result *Result) error {
	// GC control: disable GC during compression if requested
	if opts.DisableGC {
		// Force GC before disabling to start with a clean heap
		runtime.GC()
		oldGCPercent := debug.SetGCPercent(-1)
		defer debug.SetGCPercent(oldGCPercent)
	}

	// Prepare output path base (remove .zip extension if present)
	baseOutputPath := opts.OutputPath
	if strings.HasSuffix(baseOutputPath, ".zip") {
		baseOutputPath = baseOutputPath[:len(baseOutputPath)-4]
	}

	// Process files with worker pool - each worker writes to its own ZIP file
	var totalCompSize atomic.Uint64
	var processedCount atomic.Uint32
	var errorsMu sync.Mutex

	var wg sync.WaitGroup

	// Shared task channel: workers pull files as they become free.
	// Folder-hash affinity routing was dropped because it sent every file of a
	// folder to one worker — a flat input directory ran single-threaded.
	taskCh := make(chan fileTask, opts.MaxThreads*16)

	// Track ZIP files created for later cleanup/stats
	type zipFileInfo struct {
		path string
		size uint64
	}
	zipFiles := make([]zipFileInfo, opts.MaxThreads)
	var zipFilesMu sync.Mutex

	// Parts are numbered contiguously in order of first file received, so
	// idle workers don't leave empty (or gap-numbered) archives behind.
	var partCounter atomic.Int32

	// Start worker goroutines - each creates its own ZIP file on first use
	for i := 0; i < opts.MaxThreads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			var workerZipWriter *zip.Writer
			var workerZipFile *os.File
			var workerZipPath string

			// ensureArchive lazily creates this worker's ZIP file on first task
			ensureArchive := func() error {
				if workerZipFile != nil {
					return nil
				}
				partNum := int(partCounter.Add(1))
				workerZipPath = fmt.Sprintf("%s_%02d.zip", baseOutputPath, partNum)

				// Ensure output directory exists
				outputDir := filepath.Dir(workerZipPath)
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					return fmt.Errorf("worker %d: create output directory: %w", workerID, err)
				}

				var err error
				workerZipFile, err = os.Create(workerZipPath)
				if err != nil {
					return fmt.Errorf("worker %d: create zip: %w", workerID, err)
				}

				workerZipWriter = zip.NewWriter(workerZipFile)

				// Register custom deflate compressor with our compression level
				workerZipWriter.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
					if opts.Level <= 1 {
						return flate.NewWriter(out, flate.NoCompression)
					}
					flateLevel := opts.Level - 1
					if flateLevel > flate.BestCompression {
						flateLevel = flate.BestCompression
					}
					return flate.NewWriter(out, flateLevel)
				})

				// Track ZIP file for stats
				zipFilesMu.Lock()
				zipFiles[workerID].path = workerZipPath
				zipFilesMu.Unlock()
				return nil
			}

			for task := range taskCh {
				if !opts.DryRun {
					if err := ensureArchive(); err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, err)
						errorsMu.Unlock()
						return
					}
				}
				// Skip progress bar for 0-byte files (no progress to show)
				if progressCb != nil && task.OrigSize > 0 {
					progressCb(ProgressEvent{
						Type:     EventFileStart,
						FilePath: task.RelPath,
						Total:    int64(task.OrigSize),
					})
				}

				// Open file for reading
				file, err := os.Open(task.AbsPath)
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("%s: open: %w", task.RelPath, err))
					errorsMu.Unlock()

					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:     EventError,
							FilePath: task.RelPath,
						})
					}
					continue
				}

				if !opts.DryRun && workerZipWriter != nil {
					// Write to worker's own ZIP file (NO MUTEX NEEDED - each worker has its own file!)
					header := &zip.FileHeader{
						Name:   task.RelPath,
						Method: zip.Deflate,
					}

					// Use Store method for level 1 (no compression)
					if opts.Level == 1 {
						header.Method = zip.Store
					}

					w, err := workerZipWriter.CreateHeader(header)
					if err != nil {
						file.Close()
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: create header: %w", task.RelPath, err))
						errorsMu.Unlock()
						continue
					}

					// Write data with progress reporting (compression happens here)
					// Use pooled buffer when DisableGC is enabled
					var buf []byte
					var returnBuf func()
					if opts.DisableGC {
						buf = getReadBuffer()
						returnBuf = func() { putReadBuffer(buf) }
					} else {
						buf = make([]byte, 32*1024) // 32KB buffer
						returnBuf = func() {}
					}
					var written, lastReported int64
					for {
						nr, errRead := file.Read(buf)
						if nr > 0 {
							nw, errWrite := w.Write(buf[0:nr])
							if errWrite != nil {
								file.Close()
								errorsMu.Lock()
								result.Errors = append(result.Errors, fmt.Errorf("%s: write: %w", task.RelPath, errWrite))
								errorsMu.Unlock()
								break
							}
							written += int64(nw)

							// Report progress (throttled; EventFileComplete finishes the bar)
							if progressCb != nil && written-lastReported >= progressReportStep {
								lastReported = written
								progressCb(ProgressEvent{
									Type:     EventFileProgress,
									FilePath: task.RelPath,
									Current:  written,
									Total:    int64(task.OrigSize),
								})
							}
						}
						if errRead == io.EOF {
							break
						}
						if errRead != nil {
							file.Close()
							errorsMu.Lock()
							result.Errors = append(result.Errors, fmt.Errorf("%s: read: %w", task.RelPath, errRead))
							errorsMu.Unlock()
							break
						}
					}
					returnBuf()
				} else if opts.DryRun {
					// Dry-run: estimate compression (assume 50% compression ratio for deflate)
					totalCompSize.Add(task.OrigSize / 2)
				}

				file.Close()

				// Notify file complete. CompressedSize stays 0: a ZIP entry's
				// real compressed size is only known once the writer closes
				// the entry, so reporting an estimate here would be a lie.
				processedCount.Add(1)
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventFileComplete,
						FilePath: task.RelPath,
						Current:  int64(task.OrigSize),
						Total:    int64(task.OrigSize),
					})
				}
			}

			// Close worker ZIP file and record final size
			if !opts.DryRun && workerZipFile != nil {
				if err := workerZipWriter.Close(); err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: close zip: %w", workerID, err))
					errorsMu.Unlock()
					return
				}
				if err := workerZipFile.Close(); err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: close file: %w", workerID, err))
					errorsMu.Unlock()
					return
				}

				// Record final ZIP file size
				stat, err := os.Stat(workerZipPath)
				if err == nil {
					zipFilesMu.Lock()
					zipFiles[workerID].size = uint64(stat.Size())
					zipFilesMu.Unlock()
				}
			}
		}(i)
	}

	// Feed all files into the shared channel, folder by folder
	go func() {
		for _, folder := range foldersToCompress {
			for _, task := range folder.Files {
				taskCh <- task
			}
		}
		close(taskCh)
	}()

	// Wait for all workers to complete
	wg.Wait()

	result.FilesProcessed = int(processedCount.Load())

	// Calculate total compressed size from all worker ZIP files
	if !opts.DryRun {
		var totalSize uint64
		for _, info := range zipFiles {
			if info.size > 0 {
				totalSize += info.size
			}
		}
		result.CompressedSize = totalSize

		// Log multi-part archive info if verbose
		if opts.Verbose && !opts.Quiet {
			fmt.Printf("\nCreated %d ZIP files:\n", opts.MaxThreads)
			for _, info := range zipFiles {
				if info.size > 0 {
					fmt.Printf("  %s (%.2f MB)\n",
						filepath.Base(info.path), float64(info.size)/(1024*1024))
				}
			}
		}
	} else {
		result.CompressedSize = totalCompSize.Load()
	}

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:           EventComplete,
			Current:        int64(result.FilesProcessed),
			Total:          int64(totalFiles),
			CompressedSize: result.CompressedSize,
		})
	}

	if len(result.Errors) > 0 {
		return fmt.Errorf("completed with %d errors (see result.Errors)", len(result.Errors))
	}

	return nil
}
