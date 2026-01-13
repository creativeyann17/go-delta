// pkg/compress/compress_zip.go
package compress

import (
	"archive/zip"
	"compress/flate"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// compressToZip compresses files into multiple ZIP archives (one per thread) for true parallelism
// Output: archive_01.zip, archive_02.zip, ..., archive_N.zip
func compressToZip(opts *Options, progressCb ProgressCallback, foldersToCompress []folderTask, totalFiles int, totalOrigSize uint64, result *Result) error {
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
	taskCh := make(chan fileTask, totalFiles)

	// Track ZIP files created for later cleanup/stats
	type zipFileInfo struct {
		path string
		size uint64
	}
	zipFiles := make([]zipFileInfo, opts.MaxThreads)
	var zipFilesMu sync.Mutex

	// Start worker goroutines - each creates its own ZIP file
	for i := 0; i < opts.MaxThreads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Create worker-specific ZIP file
			var workerZipWriter *zip.Writer
			var workerZipFile *os.File
			var workerZipPath string

			if !opts.DryRun {
				// Generate worker-specific ZIP filename: archive_01.zip, archive_02.zip, etc.
				workerZipPath = fmt.Sprintf("%s_%02d.zip", baseOutputPath, workerID+1)

				// Ensure output directory exists
				outputDir := filepath.Dir(workerZipPath)
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: create output directory: %w", workerID, err))
					errorsMu.Unlock()
					return
				}

				var err error
				workerZipFile, err = os.Create(workerZipPath)
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: create zip: %w", workerID, err))
					errorsMu.Unlock()
					return
				}
				defer workerZipFile.Close()

				workerZipWriter = zip.NewWriter(workerZipFile)
				defer workerZipWriter.Close()

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
			}

			for task := range taskCh {
				// Notify file start
				if progressCb != nil {
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
					buf := make([]byte, 32*1024) // 32KB buffer
					var written int64
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

							// Report progress
							if progressCb != nil {
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
				} else if opts.DryRun {
					// Dry-run: estimate compression (assume 50% compression ratio for deflate)
					totalCompSize.Add(task.OrigSize / 2)
				}

				file.Close()

				// Notify file complete
				processedCount.Add(1)
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:           EventFileComplete,
						FilePath:       task.RelPath,
						Current:        int64(task.OrigSize),
						Total:          int64(task.OrigSize),
						CompressedSize: task.OrigSize / 2, // Estimate
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

	// Feed tasks to workers (sorted by folder for locality)
	for _, folder := range foldersToCompress {
		for _, task := range folder.Files {
			taskCh <- task
		}
	}
	close(taskCh)

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
