// pkg/compress/compress.go
package compress

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/klauspost/compress/zstd"
)

// resolveParallelism determines the actual parallelism strategy.
// If auto, it analyzes the folder structure to decide.
func resolveParallelism(parallelism Parallelism, folders []folderTask, maxThreads int) Parallelism {
	if parallelism != ParallelismAuto {
		return parallelism
	}

	// Count top-level folders (direct children of input root)
	topLevelFolders := 0
	for _, f := range folders {
		// A top-level folder has no path separators, or is the root ("")
		if f.FolderPath == "" || !strings.Contains(f.FolderPath, string(filepath.Separator)) {
			topLevelFolders++
		}
	}

	// Use folder mode if we have enough top-level folders to keep workers busy
	// Otherwise use file mode for better parallelism
	if topLevelFolders >= maxThreads*2 {
		return ParallelismFolder
	}
	return ParallelismFile
}

// folderHash returns a consistent hash for a folder path, used to assign files to workers
func folderHash(folderPath string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(folderPath))
	return h.Sum64()
}

type fileTask struct {
	AbsPath  string
	RelPath  string
	Info     os.FileInfo
	OrigSize uint64
}

type folderTask struct {
	FolderPath string     // Relative folder path
	Files      []fileTask // Files in this folder
}

// ProgressCallback is called for various progress events
type ProgressCallback func(event ProgressEvent)

// ProgressEvent contains progress information
type ProgressEvent struct {
	Type           EventType
	FilePath       string
	Current        int64
	Total          int64
	CurrentBytes   uint64
	TotalBytes     uint64
	CompressedSize uint64
}

// EventType indicates the type of progress event
type EventType int

const (
	EventStart EventType = iota
	EventFileStart
	EventFileProgress
	EventFileComplete
	EventComplete
	EventError
)

// Compress compresses files from inputPath into an archive at outputPath
func Compress(opts *Options, progressCb ProgressCallback) (*Result, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	result := &Result{}

	// Collect all files from either Files list or InputPath
	foldersToCompress, totalFiles, totalOrigSize, err := collectFiles(opts, result)
	if err != nil {
		return nil, err
	}

	if totalFiles == 0 {
		return nil, ErrNoFiles
	}

	result.FilesTotal = totalFiles
	result.OriginalSize = totalOrigSize

	// Resolve parallelism strategy
	resolvedParallelism := resolveParallelism(opts.Parallelism, foldersToCompress, opts.MaxThreads)

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:       EventStart,
			Total:      int64(totalFiles),
			TotalBytes: totalOrigSize,
		})
	}

	// Route to ZIP compression if UseZipFormat is enabled
	if opts.UseZipFormat {
		return result, compressToZip(opts, progressCb, foldersToCompress, totalFiles, totalOrigSize, result, resolvedParallelism)
	}

	// Route to chunked compression if ChunkSize > 0
	if opts.ChunkSize > 0 {
		return result, compressWithChunking(opts, progressCb, foldersToCompress, totalFiles, totalOrigSize, result, resolvedParallelism)
	}

	// Traditional GDELTA01 compression (file-level)
	// Uses streaming through temp files to avoid memory accumulation

	// Create archive file (if not dry-run)
	var writer io.WriteSeeker
	var writerMu sync.Mutex

	if !opts.DryRun {
		// Ensure output directory exists
		outputDir := filepath.Dir(opts.OutputPath)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return nil, fmt.Errorf("create output directory: %w", err)
		}

		outFile, err := os.Create(opts.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("create output file: %w", err)
		}
		defer outFile.Close()

		writer = outFile

		// Write archive header
		if err := format.WriteArchiveHeader(writer, uint32(totalFiles)); err != nil {
			return nil, fmt.Errorf("write archive header: %w", err)
		}
	}

	// Process files with worker pool
	var totalComprSize uint64
	var processedCount atomic.Uint32
	var errorsMu sync.Mutex

	var wg sync.WaitGroup

	// Helper function to write a single file entry with streaming from temp file
	writeFileEntry := func(relPath string, origSize uint64, tempFilePath string, compressedSize uint64) error {
		writerMu.Lock()
		defer writerMu.Unlock()

		// Write file entry header
		entryStart, err := format.WriteFileEntry(writer, relPath, origSize)
		if err != nil {
			return fmt.Errorf("write entry: %w", err)
		}

		// Get data offset
		dataStart, err := writer.Seek(0, io.SeekCurrent)
		if err != nil {
			return fmt.Errorf("seek: %w", err)
		}

		// Stream compressed data from temp file
		tempFile, err := os.Open(tempFilePath)
		if err != nil {
			return fmt.Errorf("open temp file: %w", err)
		}
		defer tempFile.Close()

		if _, err := io.Copy(writer, tempFile); err != nil {
			return fmt.Errorf("copy compressed data: %w", err)
		}

		// Update entry with compressed size and offset
		if err := format.UpdateFileEntry(writer, entryStart, compressedSize, uint64(dataStart)); err != nil {
			return fmt.Errorf("update entry: %w", err)
		}

		return nil
	}

	// Worker function to process a single file task
	// Streams compressed data to temp file to avoid memory accumulation
	processFileTask := func(task fileTask) (tempPath string, comprSize uint64, err error) {
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: task.RelPath,
				Total:    int64(task.OrigSize),
			})
		}

		if opts.DryRun {
			// Dry-run mode: just compress to discard
			_, err := compressFileToWriter(task, io.Discard, opts.Level, progressCb)
			if err != nil {
				return "", 0, err
			}
			return "", 0, nil
		}

		// Create temp file for compressed data
		tempFile, err := os.CreateTemp("", "godelta-file-*.tmp")
		if err != nil {
			return "", 0, fmt.Errorf("create temp file: %w", err)
		}
		tempPath = tempFile.Name()

		// Compress directly to temp file (streaming, no memory buffer)
		compressedSize, err := compressFileToWriter(task, tempFile, opts.Level, progressCb)
		tempFile.Close()

		if err != nil {
			os.Remove(tempPath)
			return "", 0, err
		}

		return tempPath, compressedSize, nil
	}

	if resolvedParallelism == ParallelismFolder {
		// Folder-based parallelism: workers grab whole folders
		folderCh := make(chan folderTask, len(foldersToCompress))

		for i := 0; i < opts.MaxThreads; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				for folder := range folderCh {
					for _, task := range folder.Files {
						tempPath, comprSize, err := processFileTask(task)

						if err != nil {
							errorsMu.Lock()
							result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
							errorsMu.Unlock()
							if progressCb != nil {
								progressCb(ProgressEvent{
									Type:     EventError,
									FilePath: task.RelPath,
								})
							}
							continue
						}

						// Write to archive immediately and clean up temp file
						if !opts.DryRun {
							if err := writeFileEntry(task.RelPath, task.OrigSize, tempPath, comprSize); err != nil {
								errorsMu.Lock()
								result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
								errorsMu.Unlock()
							}
							os.Remove(tempPath)
							atomic.AddUint64(&totalComprSize, comprSize)
						}

						processedCount.Add(1)
						if progressCb != nil {
							progressCb(ProgressEvent{
								Type:           EventFileComplete,
								FilePath:       task.RelPath,
								Current:        int64(task.OrigSize),
								Total:          int64(task.OrigSize),
								CompressedSize: comprSize,
							})
						}
					}
				}
			}()
		}

		// Feed folder tasks
		go func() {
			for _, task := range foldersToCompress {
				folderCh <- task
			}
			close(folderCh)
		}()
	} else {
		// File-based parallelism: per-worker channels with folder affinity
		// Files from the same folder go to the same worker for locality
		workerChannels := make([]chan fileTask, opts.MaxThreads)
		for i := range workerChannels {
			workerChannels[i] = make(chan fileTask, 64) // Buffer some tasks per worker
		}

		for i := 0; i < opts.MaxThreads; i++ {
			wg.Add(1)
			go func(workerCh chan fileTask) {
				defer wg.Done()

				for task := range workerCh {
					tempPath, comprSize, err := processFileTask(task)

					if err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
						errorsMu.Unlock()
						if progressCb != nil {
							progressCb(ProgressEvent{
								Type:     EventError,
								FilePath: task.RelPath,
							})
						}
						continue
					}

					// Write to archive immediately and clean up temp file
					if !opts.DryRun {
						if err := writeFileEntry(task.RelPath, task.OrigSize, tempPath, comprSize); err != nil {
							errorsMu.Lock()
							result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
							errorsMu.Unlock()
						}
						os.Remove(tempPath)
						atomic.AddUint64(&totalComprSize, comprSize)
					}

					processedCount.Add(1)
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:           EventFileComplete,
							FilePath:       task.RelPath,
							Current:        int64(task.OrigSize),
							Total:          int64(task.OrigSize),
							CompressedSize: comprSize,
						})
					}
				}
			}(workerChannels[i])
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
	}

	wg.Wait()

	// Write archive footer (if not dry-run)
	if !opts.DryRun && writer != nil {
		if err := format.WriteArchiveFooter(writer); err != nil {
			return nil, fmt.Errorf("write archive footer: %w", err)
		}
	}

	result.FilesProcessed = int(processedCount.Load())
	result.CompressedSize = totalComprSize

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:           EventComplete,
			Current:        int64(result.FilesProcessed),
			Total:          int64(result.FilesTotal),
			TotalBytes:     result.OriginalSize,
			CompressedSize: result.CompressedSize,
		})
	}

	return result, nil
}

// compressFileToWriter compresses a file directly to a writer
func compressFileToWriter(
	task fileTask,
	writer io.Writer,
	level int,
	progressCb ProgressCallback,
) (uint64, error) {
	src, err := os.Open(task.AbsPath)
	if err != nil {
		return 0, fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	// Track compressed bytes
	var compressedBytes uint64
	targetWriter := &progressWriter{
		Writer: writer,
		onWrite: func(n int) {
			compressedBytes += uint64(n)
		},
	}

	// Create zstd encoder
	enc, err := zstd.NewWriter(targetWriter,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
		zstd.WithZeroFrames(true),
	)
	if err != nil {
		return 0, fmt.Errorf("create zstd writer: %w", err)
	}

	// Progress tracking reader
	uncompressedRead := uint64(0)
	proxy := &progressReader{
		Reader: src,
		onRead: func(n int) {
			uncompressedRead += uint64(n)
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:         EventFileProgress,
					FilePath:     task.RelPath,
					Current:      int64(uncompressedRead),
					Total:        int64(task.OrigSize),
					CurrentBytes: uncompressedRead,
				})
			}
		},
	}

	// Perform compression
	_, err = io.Copy(enc, proxy)
	if err != nil {
		enc.Close()
		return 0, fmt.Errorf("copy/compress failed: %w", err)
	}

	// Flush and close encoder
	if err = enc.Close(); err != nil {
		return 0, fmt.Errorf("close zstd encoder: %w", err)
	}

	return compressedBytes, nil
}

type progressWriter struct {
	io.Writer
	onWrite func(int)
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.Writer.Write(p)
	if n > 0 && pw.onWrite != nil {
		pw.onWrite(n)
	}
	return n, err
}

type progressReader struct {
	io.Reader
	onRead func(int)
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	if n > 0 && pr.onRead != nil {
		pr.onRead(n)
	}
	return n, err
}

// collectFiles gathers all files from either the Files list or InputPath
// Returns folder tasks, total file count, total size, and any error
func collectFiles(opts *Options, result *Result) ([]folderTask, int, uint64, error) {
	folderMap := make(map[string][]fileTask)
	seenRelPaths := make(map[string]string) // relPath -> original source (for overlap detection)
	var totalOrigSize uint64
	var totalFiles int

	// Function to add a file task with overlap checking
	addFile := func(absPath, relPath string, info os.FileInfo, source string) error {
		// Check for overlapping relative paths
		if existingSource, exists := seenRelPaths[relPath]; exists {
			return fmt.Errorf("path overlap: %q from %q conflicts with %q", relPath, source, existingSource)
		}
		seenRelPaths[relPath] = source

		// Group by immediate parent folder
		folderPath := filepath.Dir(relPath)
		if folderPath == "." {
			folderPath = "" // Root level files
		}

		task := fileTask{
			AbsPath:  absPath,
			RelPath:  relPath,
			Info:     info,
			OrigSize: uint64(info.Size()),
		}

		folderMap[folderPath] = append(folderMap[folderPath], task)
		totalOrigSize += uint64(info.Size())
		totalFiles++
		return nil
	}

	if len(opts.Files) > 0 {
		// Custom file list mode: use paths as provided by the user
		for _, inputPath := range opts.Files {
			cleanPath := filepath.Clean(inputPath)
			info, err := os.Stat(cleanPath)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", inputPath, err))
				continue
			}

			if info.IsDir() {
				// Walk directory, paths are relative to this directory
				dirBase := filepath.Base(cleanPath)
				err := filepath.Walk(cleanPath, func(path string, finfo os.FileInfo, err error) error {
					if err != nil {
						result.Errors = append(result.Errors, fmt.Errorf("%s: %w", path, err))
						return nil
					}
					if finfo.IsDir() || !finfo.Mode().IsRegular() {
						return nil
					}

					// RelPath = dirBase + path relative to cleanPath
					relToDir, _ := filepath.Rel(cleanPath, path)
					relPath := filepath.Join(dirBase, relToDir)

					if err := addFile(path, relPath, finfo, inputPath); err != nil {
						return err
					}
					return nil
				})
				if err != nil {
					return nil, 0, 0, err
				}
			} else if info.Mode().IsRegular() {
				// Single file: use just the filename
				relPath := filepath.Base(cleanPath)
				if err := addFile(cleanPath, relPath, info, inputPath); err != nil {
					return nil, 0, 0, err
				}
			}
		}
	} else {
		// InputPath mode: walk and use paths relative to InputPath
		baseDir := opts.InputPath
		err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", path, err))
				return nil
			}
			if info.IsDir() || !info.Mode().IsRegular() {
				return nil
			}

			relPath, err := filepath.Rel(baseDir, path)
			if err != nil {
				relPath = filepath.Base(path)
			}

			if err := addFile(path, relPath, info, baseDir); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return nil, 0, 0, fmt.Errorf("directory walk failed: %w", err)
		}
	}

	// Convert folder map to task list
	foldersToCompress := make([]folderTask, 0, len(folderMap))
	for folderPath, files := range folderMap {
		foldersToCompress = append(foldersToCompress, folderTask{
			FolderPath: folderPath,
			Files:      files,
		})
	}

	return foldersToCompress, totalFiles, totalOrigSize, nil
}
