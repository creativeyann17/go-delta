// pkg/compress/compress_xz.go
package compress

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ulikunitz/xz"
)

// compressToXz compresses files into multiple .tar.xz archives (one per thread) for true parallelism
// Output: archive_01.tar.xz, archive_02.tar.xz, ..., archive_N.tar.xz
func compressToXz(opts *Options, progressCb ProgressCallback, foldersToCompress []folderTask, totalFiles int, totalOrigSize uint64, result *Result, parallelism Parallelism) error {
	// Prepare output path base (remove .tar.xz or .xz extension if present)
	baseOutputPath := opts.OutputPath
	if strings.HasSuffix(baseOutputPath, ".tar.xz") {
		baseOutputPath = baseOutputPath[:len(baseOutputPath)-7]
	} else if strings.HasSuffix(baseOutputPath, ".xz") {
		baseOutputPath = baseOutputPath[:len(baseOutputPath)-3]
	}

	// Process files with worker pool - each worker writes to its own .tar.xz file
	var totalCompSize atomic.Uint64
	var processedCount atomic.Uint32
	var errorsMu sync.Mutex

	var wg sync.WaitGroup

	// Create per-worker channels with folder affinity
	workerChannels := make([]chan fileTask, opts.MaxThreads)
	for i := range workerChannels {
		workerChannels[i] = make(chan fileTask, 64)
	}

	// Track archive files created for later cleanup/stats
	type archiveFileInfo struct {
		path string
		size uint64
	}
	archiveFiles := make([]archiveFileInfo, opts.MaxThreads)
	var archiveFilesMu sync.Mutex

	// Start worker goroutines - each creates its own .tar.xz file
	for i := 0; i < opts.MaxThreads; i++ {
		wg.Add(1)
		go func(workerID int, workerCh chan fileTask) {
			defer wg.Done()

			var workerTarWriter *tar.Writer
			var workerXzWriter *xz.Writer
			var workerFile *os.File
			var workerFilePath string

			if !opts.DryRun {
				// Generate worker-specific filename: archive_01.tar.xz, archive_02.tar.xz, etc.
				workerFilePath = fmt.Sprintf("%s_%02d.tar.xz", baseOutputPath, workerID+1)

				// Ensure output directory exists
				outputDir := filepath.Dir(workerFilePath)
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: create output directory: %w", workerID, err))
					errorsMu.Unlock()
					return
				}

				var err error
				workerFile, err = os.Create(workerFilePath)
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: create archive: %w", workerID, err))
					errorsMu.Unlock()
					return
				}
				defer workerFile.Close()

				// Create XZ writer with compression level
				xzConfig := xz.WriterConfig{
					DictCap: 1 << (20 + opts.Level), // Scale dictionary with level
				}
				if opts.Level >= 7 {
					xzConfig.DictCap = 1 << 26 // 64MB for high levels
				}

				workerXzWriter, err = xzConfig.NewWriter(workerFile)
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: create xz writer: %w", workerID, err))
					errorsMu.Unlock()
					return
				}
				defer workerXzWriter.Close()

				workerTarWriter = tar.NewWriter(workerXzWriter)
				defer workerTarWriter.Close()

				// Track archive file for stats
				archiveFilesMu.Lock()
				archiveFiles[workerID].path = workerFilePath
				archiveFilesMu.Unlock()
			}

			for task := range workerCh {
				// Skip progress bar for 0-byte files
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

				if !opts.DryRun && workerTarWriter != nil {
					// Write tar header
					header := &tar.Header{
						Name: task.RelPath,
						Mode: 0644,
						Size: int64(task.OrigSize),
					}

					if err := workerTarWriter.WriteHeader(header); err != nil {
						file.Close()
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: write header: %w", task.RelPath, err))
						errorsMu.Unlock()
						continue
					}

					// Write file data with progress reporting
					buf := make([]byte, 32*1024) // 32KB buffer
					var written int64
					for {
						nr, errRead := file.Read(buf)
						if nr > 0 {
							nw, errWrite := workerTarWriter.Write(buf[0:nr])
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
					// Dry-run: estimate compression (assume 30% for LZMA2)
					totalCompSize.Add(task.OrigSize * 30 / 100)
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
						CompressedSize: task.OrigSize * 30 / 100, // Estimate
					})
				}
			}

			// Close worker archive and record final size
			if !opts.DryRun && workerFile != nil {
				if workerTarWriter != nil {
					if err := workerTarWriter.Close(); err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("worker %d: close tar: %w", workerID, err))
						errorsMu.Unlock()
						return
					}
				}
				if workerXzWriter != nil {
					if err := workerXzWriter.Close(); err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("worker %d: close xz: %w", workerID, err))
						errorsMu.Unlock()
						return
					}
				}
				if err := workerFile.Close(); err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("worker %d: close file: %w", workerID, err))
					errorsMu.Unlock()
					return
				}

				// Record final archive size
				stat, err := os.Stat(workerFilePath)
				if err == nil {
					archiveFilesMu.Lock()
					archiveFiles[workerID].size = uint64(stat.Size())
					archiveFilesMu.Unlock()
				}
			}
		}(i, workerChannels[i])
	}

	// Route files to workers based on folder hash (maintains folder locality)
	go func() {
		for _, folder := range foldersToCompress {
			workerIdx := int(folderHash(folder.FolderPath) % uint64(opts.MaxThreads))
			for _, task := range folder.Files {
				workerChannels[workerIdx] <- task
			}
		}
		for _, ch := range workerChannels {
			close(ch)
		}
	}()

	// Wait for all workers to complete
	wg.Wait()

	result.FilesProcessed = int(processedCount.Load())

	// Calculate total compressed size from all worker archives
	if !opts.DryRun {
		var totalSize uint64
		for _, info := range archiveFiles {
			if info.size > 0 {
				totalSize += info.size
			}
		}
		result.CompressedSize = totalSize

		// Log multi-part archive info if verbose
		if opts.Verbose && !opts.Quiet {
			fmt.Printf("\nCreated %d XZ archives:\n", opts.MaxThreads)
			for _, info := range archiveFiles {
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
