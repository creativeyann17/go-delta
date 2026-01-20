// pkg/compress/compress_dict.go
package compress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/creativeyann17/go-delta/internal/format"
	"github.com/creativeyann17/go-delta/pkg/godelta"
	"github.com/klauspost/compress/dict"
	"github.com/klauspost/compress/zstd"
)

const (
	// MinDictSize is the minimum dictionary size required by zstd encoder
	// The zstd library uses internal history buffers that require at least 32KB
	MinDictSize = 32 * 1024

	// MaxDictSize is the maximum useful dictionary size
	MaxDictSize = 112 * 1024

	// MinSampleSizeForDict is the minimum individual sample size for dictionary training
	// Small samples are fine - the library handles them well
	// Only skip truly tiny samples that add noise without useful patterns
	MinSampleSizeForDict = 64
)

// dictParams holds auto-computed dictionary training parameters
type dictParams struct {
	maxDictSize     int   // Maximum dictionary size in bytes
	maxSampleSize   int64 // Maximum bytes to sample from each file
	maxTotalSamples int64 // Maximum total bytes to collect for training
}

// analyzeDictParams computes optimal dictionary training parameters based on input files
func analyzeDictParams(files []fileTask, verbose bool) dictParams {
	// Default params for edge cases (will skip dict training anyway)
	defaultParams := dictParams{
		maxDictSize:     MinDictSize,
		maxSampleSize:   16 * 1024,
		maxTotalSamples: 1 * 1024 * 1024,
	}

	if len(files) == 0 {
		return defaultParams
	}

	// Calculate statistics
	var totalSize uint64
	var nonEmptyCount int

	for _, f := range files {
		totalSize += f.OrigSize
		if f.OrigSize > 0 {
			nonEmptyCount++
		}
	}

	if nonEmptyCount == 0 {
		return defaultParams
	}

	avgFileSize := totalSize / uint64(nonEmptyCount)

	// Dictionary size: scales with data volume
	// Always at least MinDictSize (32KB) for zstd compatibility
	// Small data (<10MB): 32KB dict
	// Medium data (10-100MB): 64KB dict
	// Large data (>100MB): 112KB dict
	var dictSize int
	switch {
	case totalSize < 10*1024*1024:
		dictSize = MinDictSize
	case totalSize < 100*1024*1024:
		dictSize = 64 * 1024
	default:
		dictSize = MaxDictSize
	}

	// Sample size per file: proportional to average file size
	// Must stay within zstd encoder's block limits to avoid panics (max 64KB)
	// For small files, sample the entire file
	sampleSize := int64(avgFileSize)
	if sampleSize > 64*1024 {
		sampleSize = 64 * 1024
	}
	// Minimum 1KB to avoid tiny samples, but allow small files
	if sampleSize < 1024 {
		sampleSize = 1024
	}

	// Total samples: scale with total size and file count
	// Must be significantly larger than dictionary size (8x minimum recommended)
	// Aim for sampling ~5% of total, with diversity consideration
	totalSamples := int64(totalSize / 20)         // 5% of total
	minSamples := int64(nonEmptyCount) * 4 * 1024 // At least 4KB per file on average

	// Ensure total samples is at least 8x dictionary size for quality training
	minForDict := int64(dictSize) * 8
	if totalSamples < minForDict {
		totalSamples = minForDict
	}
	if totalSamples < minSamples {
		totalSamples = minSamples
	}
	// Bounds: min 512KB, max 50MB
	if totalSamples < 512*1024 {
		totalSamples = 512 * 1024
	}
	if totalSamples > 50*1024*1024 {
		totalSamples = 50 * 1024 * 1024
	}

	if verbose {
		fmt.Printf("Dict params (auto): dictSize=%dKB, sampleSize=%dKB, totalSamples=%dMB (from %d files, %dMB total)\n",
			dictSize/1024, sampleSize/1024, totalSamples/(1024*1024), nonEmptyCount, totalSize/(1024*1024))
	}

	return dictParams{
		maxDictSize:     dictSize,
		maxSampleSize:   sampleSize,
		maxTotalSamples: totalSamples,
	}
}

// compressWithDictionary compresses files using GDELTA03 dictionary-based compression
func compressWithDictionary(
	opts *Options,
	progressCb ProgressCallback,
	foldersToCompress []folderTask,
	totalFiles int,
	totalOrigSize uint64,
	result *Result,
	resolvedParallelism Parallelism,
) error {
	// Flatten files for processing
	var allFiles []fileTask
	for _, folder := range foldersToCompress {
		allFiles = append(allFiles, folder.Files...)
	}

	// Phase 1: Train dictionary
	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:     EventDictTraining,
			FilePath: "Training dictionary...",
		})
	}

	dictionary, err := trainDictionary(allFiles, opts.Verbose)
	if err != nil {
		return fmt.Errorf("train dictionary: %w", err)
	}

	if opts.Verbose {
		if len(dictionary) > 0 {
			fmt.Printf("Dictionary built: %d bytes\n", len(dictionary))
		} else {
			fmt.Printf("Dictionary empty - compression will proceed without dictionary benefit\n")
		}
	}

	if opts.DryRun {
		// In dry-run mode, just simulate compression
		return dryRunDictCompression(allFiles, dictionary, opts, progressCb, result)
	}

	// Phase 2: Create archive
	outputDir := filepath.Dir(opts.OutputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	outFile, err := os.Create(opts.OutputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Write header with dictionary
	if err := format.WriteGDelta03Header(outFile, uint32(len(dictionary)), uint32(totalFiles)); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	// Write dictionary
	if _, err := outFile.Write(dictionary); err != nil {
		return fmt.Errorf("write dictionary: %w", err)
	}

	// Phase 3: Parallel compression using temp files
	var totalComprSize uint64
	var processedCount atomic.Uint32
	var writerMu sync.Mutex
	var errorsMu sync.Mutex
	var wg sync.WaitGroup

	// Helper to write a completed file entry to the archive
	writeFileEntry := func(task fileTask, tempFilePath string, compressedSize uint64) error {
		writerMu.Lock()
		defer writerMu.Unlock()

		// Write file entry header
		if err := format.WriteGDelta03FileEntry(outFile, task.RelPath, task.OrigSize, compressedSize); err != nil {
			return fmt.Errorf("write entry: %w", err)
		}

		// Copy compressed data from temp file
		tempFile, err := os.Open(tempFilePath)
		if err != nil {
			return fmt.Errorf("open temp file: %w", err)
		}
		defer tempFile.Close()

		if _, err := io.Copy(outFile, tempFile); err != nil {
			return fmt.Errorf("copy compressed data: %w", err)
		}

		return nil
	}

	// Worker function to compress a single file
	processFileTask := func(task fileTask) (tempPath string, comprSize uint64, err error) {
		if progressCb != nil && task.OrigSize > 0 {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: task.RelPath,
				Total:    int64(task.OrigSize),
			})
		}

		// Create temp file for compressed data
		tempFile, err := os.CreateTemp("", "godelta-dict-*.tmp")
		if err != nil {
			return "", 0, fmt.Errorf("create temp file: %w", err)
		}
		tempPath = tempFile.Name()

		// Compress with dictionary
		compressedSize, err := compressFileWithDict(task, tempFile, dictionary, opts.Level, progressCb)
		tempFile.Close()

		if err != nil {
			os.Remove(tempPath)
			return "", 0, err
		}

		return tempPath, compressedSize, nil
	}

	if resolvedParallelism == ParallelismFolder {
		// Folder-based parallelism
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
								progressCb(ProgressEvent{Type: EventError, FilePath: task.RelPath})
							}
							continue
						}

						if err := writeFileEntry(task, tempPath, comprSize); err != nil {
							errorsMu.Lock()
							result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
							errorsMu.Unlock()
						}
						os.Remove(tempPath)
						atomic.AddUint64(&totalComprSize, comprSize)

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

		go func() {
			for _, task := range foldersToCompress {
				folderCh <- task
			}
			close(folderCh)
		}()
	} else {
		// File-based parallelism with folder affinity
		workerChannels := make([]chan fileTask, opts.MaxThreads)
		for i := range workerChannels {
			workerChannels[i] = make(chan fileTask, 64)
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
							progressCb(ProgressEvent{Type: EventError, FilePath: task.RelPath})
						}
						continue
					}

					if err := writeFileEntry(task, tempPath, comprSize); err != nil {
						errorsMu.Lock()
						result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
						errorsMu.Unlock()
					}
					os.Remove(tempPath)
					atomic.AddUint64(&totalComprSize, comprSize)

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

	// Write footer
	if err := format.WriteArchiveFooter03(outFile); err != nil {
		return fmt.Errorf("write footer: %w", err)
	}

	// Calculate total archive overhead: header(21) + dictionary + footer(8)
	archiveOverhead := uint64(21 + len(dictionary) + 8)

	result.FilesProcessed = int(processedCount.Load())
	result.CompressedSize = totalComprSize + archiveOverhead

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:           EventComplete,
			Current:        int64(result.FilesProcessed),
			Total:          int64(result.FilesTotal),
			TotalBytes:     result.OriginalSize,
			CompressedSize: result.CompressedSize,
		})
	}

	return nil
}

// trainDictionary collects samples from files and builds a zstd dictionary
func trainDictionary(files []fileTask, verbose bool) ([]byte, error) {
	// Auto-compute optimal parameters based on input
	params := analyzeDictParams(files, verbose)

	var samples [][]byte
	var totalSampled int64
	var skippedEmpty, skippedError int

	var skippedTooSmall int

	// Collect samples from files
	for _, file := range files {
		if totalSampled >= params.maxTotalSamples {
			break
		}

		// Read sample from file
		sampleSize := params.maxSampleSize
		if file.OrigSize < uint64(sampleSize) {
			sampleSize = int64(file.OrigSize)
		}

		if sampleSize == 0 {
			skippedEmpty++
			continue // Skip empty files
		}

		sample, err := readFileSample(file.AbsPath, sampleSize)
		if err != nil {
			skippedError++
			continue
		}

		if len(sample) == 0 {
			skippedEmpty++
			continue
		}

		// Skip samples smaller than zstd's minimum (prevents encoder buffer underflow)
		if len(sample) < MinSampleSizeForDict {
			skippedTooSmall++
			continue
		}

		samples = append(samples, sample)
		totalSampled += int64(len(sample))
	}

	if verbose {
		fmt.Printf("Dictionary training: %d files sampled, %d bytes total, %d empty, %d too small (<%dKB), %d errors\n",
			len(samples), totalSampled, skippedEmpty, skippedTooSmall, MinSampleSizeForDict/1024, skippedError)
	}

	if len(samples) == 0 {
		// No samples available, return empty dictionary
		return []byte{}, nil
	}

	// Need minimum total sample size and sample diversity for dictionary training
	// The dict library requires meaningful content to build a dictionary
	var totalSampleBytes int
	for _, s := range samples {
		totalSampleBytes += len(s)
	}

	// Dictionary training requirements:
	// 1. At least 3 different samples (for diversity)
	// 2. Minimum 2KB total samples (dictionary can be built from small data)
	// The dictionary size will be scaled down if samples are small
	minRequiredSamples := 2 * 1024

	if totalSampleBytes < minRequiredSamples || len(samples) < 3 {
		if verbose {
			fmt.Printf("Dictionary training skipped: need >= %dKB and >= 3 samples (got %dKB, %d samples)\n",
				minRequiredSamples/1024, totalSampleBytes/1024, len(samples))
		}
		return []byte{}, nil
	}

	// Scale dictionary size based on available samples
	// Dictionary should be smaller than total samples for effective training
	actualDictSize := params.maxDictSize
	if totalSampleBytes < actualDictSize*2 {
		// Scale down dictionary to ~half of samples, minimum 1KB
		actualDictSize = totalSampleBytes / 2
		if actualDictSize < 1024 {
			actualDictSize = 1024
		}
	}

	// Build dictionary using recover() to handle library edge cases
	// The klauspost/compress library can panic with certain sample patterns
	dictOpts := dict.Options{
		MaxDictSize: actualDictSize,
		HashBytes:   6, // Recommended value for general-purpose dictionaries
		ZstdLevel:   zstd.SpeedFastest,
	}

	var dictBytes []byte
	var buildErr error

	func() {
		defer func() {
			if r := recover(); r != nil {
				if verbose {
					fmt.Printf("Dictionary training failed (library panic): %v - proceeding without dictionary\n", r)
				}
				dictBytes = []byte{}
			}
		}()
		dictBytes, buildErr = dict.BuildZstdDict(samples, dictOpts)
	}()

	if buildErr != nil {
		return nil, fmt.Errorf("build dictionary: %w", buildErr)
	}

	return dictBytes, nil
}

// readFileSample reads up to maxBytes from the beginning of a file
func readFileSample(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sample := make([]byte, maxBytes)
	n, err := io.ReadFull(f, sample)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	return sample[:n], nil
}

// compressFileWithDict compresses a file using a pre-trained dictionary
func compressFileWithDict(
	task fileTask,
	writer io.Writer,
	dictionary []byte,
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
	targetWriter := &godelta.ProgressWriter{
		Writer: writer,
		OnWrite: func(n int) {
			compressedBytes += uint64(n)
		},
	}

	// Create encoder options
	encOpts := []zstd.EOption{
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
		zstd.WithZeroFrames(true),
	}

	// Add dictionary if present
	if len(dictionary) > 0 {
		encOpts = append(encOpts, zstd.WithEncoderDict(dictionary))
	}

	// Create encoder with dictionary
	enc, err := zstd.NewWriter(targetWriter, encOpts...)
	if err != nil {
		return 0, fmt.Errorf("create zstd writer: %w", err)
	}

	// Progress tracking
	uncompressedRead := uint64(0)
	proxy := &godelta.ProgressReader{
		Reader: src,
		OnRead: func(n int) {
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

	// Compress
	if _, err := io.Copy(enc, proxy); err != nil {
		enc.Close()
		return 0, fmt.Errorf("compress: %w", err)
	}

	if err := enc.Close(); err != nil {
		return 0, fmt.Errorf("close encoder: %w", err)
	}

	return compressedBytes, nil
}

// dryRunDictCompression simulates dictionary compression without writing
func dryRunDictCompression(
	files []fileTask,
	dictionary []byte,
	opts *Options,
	progressCb ProgressCallback,
	result *Result,
) error {
	var totalComprSize uint64

	for _, task := range files {
		if progressCb != nil && task.OrigSize > 0 {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: task.RelPath,
				Total:    int64(task.OrigSize),
			})
		}

		// Compress to discard to measure size
		comprSize, err := compressFileWithDict(task, &godelta.DiscardCounter{}, dictionary, opts.Level, progressCb)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.RelPath, err))
			if progressCb != nil {
				progressCb(ProgressEvent{Type: EventError, FilePath: task.RelPath})
			}
			continue
		}

		totalComprSize += comprSize
		result.FilesProcessed++

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

	return nil
}
