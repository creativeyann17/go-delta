// pkg/compress/compress.go
package compress

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/creativeyann17/go-delta/pkg/godelta"
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

// feedTasks streams every file into a shared channel, folder by folder, then
// closes it. Workers pull from the channel as they become free, so load stays
// balanced regardless of how files are distributed across folders.
func feedTasks(folders []folderTask, capacity int) <-chan fileTask {
	ch := make(chan fileTask, capacity)
	go func() {
		for _, folder := range folders {
			for _, task := range folder.Files {
				ch <- task
			}
		}
		close(ch)
	}()
	return ch
}

// newWorkerEncoder creates a zstd encoder for a single worker goroutine.
// The encoder is reused across files/chunks via Reset/EncodeAll instead of
// being recreated per item (zstd.NewWriter allocates large buffers).
// Internal encoder concurrency is divided by the worker count so the pool
// doesn't oversubscribe CPUs.
func newWorkerEncoder(level, maxThreads int, dictionary []byte) (*zstd.Encoder, error) {
	concurrency := runtime.GOMAXPROCS(0) / maxThreads
	if concurrency < 1 {
		concurrency = 1
	}
	encOpts := []zstd.EOption{
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
		zstd.WithZeroFrames(true),
		zstd.WithEncoderConcurrency(concurrency),
	}
	if len(dictionary) > 0 {
		encOpts = append(encOpts, zstd.WithEncoderDict(dictionary))
	}
	return zstd.NewWriter(nil, encOpts...)
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
	EventDictTraining // Dictionary training phase for GDELTA03
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
	result.ChunkSize = opts.ChunkSize

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
	// (ZIP mode uses a shared work queue, no parallelism strategy needed)
	if opts.UseZipFormat {
		return result, compressToZip(opts, progressCb, foldersToCompress, totalFiles, totalOrigSize, result)
	}

	// Route to XZ compression if UseXzFormat is enabled
	// (XZ mode uses a shared work queue, no parallelism strategy needed)
	if opts.UseXzFormat {
		return result, compressToXz(opts, progressCb, foldersToCompress, totalFiles, totalOrigSize, result)
	}

	// Route to dictionary compression if UseDictionary is enabled
	if opts.UseDictionary {
		return result, compressWithDictionary(opts, progressCb, foldersToCompress, totalFiles, totalOrigSize, result, resolvedParallelism)
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

	// Helper function to write a single file entry, streaming compressed data
	writeFileEntry := func(relPath string, origSize uint64, data io.Reader, compressedSize uint64) error {
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

		if _, err := io.Copy(writer, data); err != nil {
			return fmt.Errorf("copy compressed data: %w", err)
		}

		// Update entry with compressed size and offset
		if err := format.UpdateFileEntry(writer, entryStart, compressedSize, uint64(dataStart)); err != nil {
			return fmt.Errorf("update entry: %w", err)
		}

		return nil
	}

	recordError := func(task fileTask, err error) {
		errorsMu.Lock()
		result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
		errorsMu.Unlock()
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventError,
				FilePath: task.RelPath,
			})
		}
	}

	// handleTask compresses one file and writes it to the archive.
	// Small files (<= MaxThreadMemory) are compressed into a memory buffer and
	// written directly; larger files stream through a temp file to bound RAM.
	handleTask := func(task fileTask, enc *zstd.Encoder, memBuf *bytes.Buffer) {
		// Skip progress bar for 0-byte files (no progress to show)
		if progressCb != nil && task.OrigSize > 0 {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: task.RelPath,
				Total:    int64(task.OrigSize),
			})
		}

		var comprSize uint64
		var err error

		switch {
		case opts.DryRun:
			// Dry-run mode: just compress to discard
			_, err = compressFileToWriter(task, io.Discard, enc, progressCb)
			if err != nil {
				recordError(task, err)
				return
			}

		case opts.MaxThreadMemory > 0 && task.OrigSize <= opts.MaxThreadMemory:
			// In-memory path: avoids writing compressed data to disk twice
			memBuf.Reset()
			comprSize, err = compressFileToWriter(task, memBuf, enc, progressCb)
			if err != nil {
				recordError(task, err)
				return
			}
			if err := writeFileEntry(task.RelPath, task.OrigSize, memBuf, comprSize); err != nil {
				recordError(task, err)
				return
			}
			atomic.AddUint64(&totalComprSize, comprSize)

		default:
			// Temp-file path: bounded memory for large files
			tempFile, err := os.CreateTemp("", "godelta-file-*.tmp")
			if err != nil {
				recordError(task, fmt.Errorf("create temp file: %w", err))
				return
			}
			tempPath := tempFile.Name()

			comprSize, err = compressFileToWriter(task, tempFile, enc, progressCb)
			tempFile.Close()
			if err != nil {
				os.Remove(tempPath)
				recordError(task, err)
				return
			}

			tempData, err := os.Open(tempPath)
			if err != nil {
				os.Remove(tempPath)
				recordError(task, fmt.Errorf("open temp file: %w", err))
				return
			}
			err = writeFileEntry(task.RelPath, task.OrigSize, tempData, comprSize)
			tempData.Close()
			os.Remove(tempPath)
			if err != nil {
				recordError(task, err)
				return
			}
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

	if resolvedParallelism == ParallelismFolder {
		// Folder-based parallelism: workers grab whole folders
		folderCh := make(chan folderTask, len(foldersToCompress))

		for i := 0; i < opts.MaxThreads; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				enc, err := newWorkerEncoder(opts.Level, opts.MaxThreads, nil)
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("create zstd encoder: %w", err))
					errorsMu.Unlock()
					return
				}
				defer enc.Close()
				var memBuf bytes.Buffer

				for folder := range folderCh {
					for _, task := range folder.Files {
						handleTask(task, enc, &memBuf)
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
		// File-based parallelism: shared work queue, workers pull as they free up
		taskCh := feedTasks(foldersToCompress, opts.MaxThreads*16)

		for i := 0; i < opts.MaxThreads; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				enc, err := newWorkerEncoder(opts.Level, opts.MaxThreads, nil)
				if err != nil {
					errorsMu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("create zstd encoder: %w", err))
					errorsMu.Unlock()
					return
				}
				defer enc.Close()
				var memBuf bytes.Buffer

				for task := range taskCh {
					handleTask(task, enc, &memBuf)
				}
			}()
		}
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

// compressFileToWriter compresses a file directly to a writer.
// The encoder is owned by the calling worker and reused across files via Reset.
func compressFileToWriter(
	task fileTask,
	writer io.Writer,
	enc *zstd.Encoder,
	progressCb ProgressCallback,
) (uint64, error) {
	src, err := os.Open(task.AbsPath)
	if err != nil {
		return 0, fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	// Track compressed bytes
	var compressedBytes uint64
	targetWriter := &godelta.ProgressWriter{
		Writer: writer,
		OnWrite: func(n int) {
			compressedBytes += uint64(n)
		},
	}

	enc.Reset(targetWriter)

	// Progress tracking reader (throttled; EventFileComplete finishes the bar)
	var uncompressedRead, lastReported uint64
	proxy := &godelta.ProgressReader{
		Reader: src,
		OnRead: func(n int) {
			uncompressedRead += uint64(n)
			if progressCb != nil && uncompressedRead-lastReported >= progressReportStep {
				lastReported = uncompressedRead
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

	// Flush and finalize the frame (encoder stays reusable after Reset)
	if err = enc.Close(); err != nil {
		return 0, fmt.Errorf("close zstd encoder: %w", err)
	}

	return compressedBytes, nil
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
				// Create gitignore matcher for this directory if enabled
				var matcher *gitignoreMatcher
				if opts.UseGitignore {
					matcher, _ = newGitignoreMatcher(cleanPath)
				}

				// Walk directory, paths are relative to this directory
				dirBase := filepath.Base(cleanPath)
				err := filepath.Walk(cleanPath, func(path string, finfo os.FileInfo, err error) error {
					if err != nil {
						result.Errors = append(result.Errors, fmt.Errorf("%s: %w", path, err))
						return nil
					}

					// Calculate relative path within the walked directory (for gitignore matching)
					relToDir, _ := filepath.Rel(cleanPath, path)

					// Check gitignore for directories (prune entire subtree)
					if finfo.IsDir() {
						if path != cleanPath && matcher != nil && matcher.ShouldIgnoreDir(relToDir) {
							return filepath.SkipDir
						}
						return nil
					}

					if !finfo.Mode().IsRegular() {
						return nil
					}

					// Check gitignore for files
					if matcher != nil && matcher.ShouldIgnore(relToDir) {
						return nil
					}

					// RelPath = dirBase + path relative to cleanPath
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

		// Create gitignore matcher if enabled
		var matcher *gitignoreMatcher
		if opts.UseGitignore {
			matcher, _ = newGitignoreMatcher(baseDir)
		}

		err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", path, err))
				return nil
			}

			relPath, err := filepath.Rel(baseDir, path)
			if err != nil {
				relPath = filepath.Base(path)
			}

			// Check gitignore for directories (prune entire subtree)
			if info.IsDir() {
				if path != baseDir && matcher != nil && matcher.ShouldIgnoreDir(relPath) {
					return filepath.SkipDir
				}
				return nil
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			// Check gitignore for files
			if matcher != nil && matcher.ShouldIgnore(relPath) {
				return nil
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
