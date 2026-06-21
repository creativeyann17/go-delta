// pkg/decompress/decompress_zip.go
package decompress

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/klauspost/compress/flate"
)

// progressReportStep is the minimum number of bytes between two
// EventFileProgress emissions (see compress side for rationale).
const progressReportStep = 1 << 20

// decompressZip extracts files from standard ZIP archive(s)
// Supports both single ZIP files and multi-part archives (archive_01.zip, archive_02.zip, ...)
func decompressZip(opts *Options, progressCb ProgressCallback, result *Result) error {
	// Detect if this is a multi-part archive (ends with _XX.zip pattern)
	zipPaths := []string{opts.InputPath}

	baseName := filepath.Base(opts.InputPath)
	if strings.Contains(baseName, "_") && strings.HasSuffix(baseName, ".zip") {
		// Check if this looks like archive_01.zip pattern
		parts := strings.Split(baseName[:len(baseName)-4], "_")
		if len(parts) >= 2 {
			lastPart := parts[len(parts)-1]
			// Check if last part is a number
			if len(lastPart) == 2 && lastPart[0] >= '0' && lastPart[0] <= '9' && lastPart[1] >= '0' && lastPart[1] <= '9' {
				// Multi-part archive detected - find all parts
				basePattern := strings.Join(parts[:len(parts)-1], "_")
				dirPath := filepath.Dir(opts.InputPath)

				// Find all matching parts
				zipPaths = []string{}
				for i := 1; i <= 99; i++ { // Support up to 99 parts
					partPath := filepath.Join(dirPath, fmt.Sprintf("%s_%02d.zip", basePattern, i))
					if _, err := os.Stat(partPath); err == nil {
						zipPaths = append(zipPaths, partPath)
					} else {
						break // No more parts
					}
				}

				if len(zipPaths) == 0 {
					return fmt.Errorf("no multi-part archive files found matching pattern: %s_XX.zip", basePattern)
				}
			}
		}
	}

	// Count total files across all ZIP parts
	var totalFiles int
	if !opts.Quiet && len(zipPaths) > 1 {
		fmt.Printf("Detecting multi-part archive: scanning %d parts...\n", len(zipPaths))
	}
	for _, zipPath := range zipPaths {
		zr, err := zip.OpenReader(zipPath)
		if err != nil {
			return fmt.Errorf("open zip archive %s: %w", zipPath, err)
		}
		totalFiles += len(zr.File)
		zr.Close()
	}
	if !opts.Quiet && len(zipPaths) > 1 {
		fmt.Printf("Found %d files across %d archive parts\n\n", totalFiles, len(zipPaths))
	}

	result.FilesTotal = totalFiles

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:  EventStart,
			Total: int64(totalFiles),
		})
	}

	// Extract ZIP parts in parallel (parts are independent archives with
	// disjoint file sets, one worker per part)
	workers := opts.MaxThreads
	if workers > len(zipPaths) {
		workers = len(zipPaths)
	}

	var mu sync.Mutex // guards result
	var wg sync.WaitGroup
	pathCh := make(chan string)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for zipPath := range pathCh {
				if err := extractZipFile(zipPath, opts, progressCb, result, &mu); err != nil {
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("extract %s: %w", zipPath, err))
					mu.Unlock()
				}
			}
		}()
	}

	for _, zipPath := range zipPaths {
		pathCh <- zipPath
	}
	close(pathCh)
	wg.Wait()

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventComplete,
			Current: int64(result.FilesProcessed),
			Total:   int64(totalFiles),
		})
	}

	// Don't return error here - let command layer handle result.Errors
	// This matches GDELTA decompression behavior
	return nil
}

// extractZipFile extracts a single ZIP archive. It may run concurrently with
// other parts; result mutations go through mu.
func extractZipFile(zipPath string, opts *Options, progressCb ProgressCallback, result *Result, mu *sync.Mutex) error {
	// Open ZIP archive
	zipReader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zipReader.Close()

	// Use klauspost/compress inflate (faster than stdlib compress/flate)
	zipReader.RegisterDecompressor(zip.Deflate, func(r io.Reader) io.ReadCloser {
		return flate.NewReader(r)
	})

	recordError := func(err error) {
		mu.Lock()
		result.Errors = append(result.Errors, err)
		mu.Unlock()
	}

	// Reused across files in this part
	buf := make([]byte, 256*1024)

	// Extract each file
	for _, zipFile := range zipReader.File {
		// Notify file start
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: zipFile.Name,
				Total:    int64(zipFile.UncompressedSize64),
			})
		}

		// Construct output path
		outPath := filepath.Join(opts.OutputPath, zipFile.Name)

		// Directory entries: just ensure the directory exists and move on.
		if zipFile.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0755); err != nil {
				recordError(fmt.Errorf("%s: mkdir: %w", zipFile.Name, err))
			}
			mu.Lock()
			result.FilesProcessed++
			mu.Unlock()
			continue
		}

		// Check if file already exists
		if !opts.Overwrite {
			if _, err := os.Stat(outPath); err == nil {
				recordError(fmt.Errorf("%s: file exists (use --overwrite to replace)", zipFile.Name))

				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventError,
						FilePath: zipFile.Name,
					})
				}
				continue
			}
		}

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			recordError(fmt.Errorf("%s: mkdir: %w", zipFile.Name, err))
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:     EventError,
					FilePath: zipFile.Name,
				})
			}
			continue
		}

		// Open file from ZIP
		rc, err := zipFile.Open()
		if err != nil {
			recordError(fmt.Errorf("%s: open: %w", zipFile.Name, err))
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:     EventError,
					FilePath: zipFile.Name,
				})
			}
			continue
		}

		// Create output file
		outFile, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			recordError(fmt.Errorf("%s: create: %w", zipFile.Name, err))
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:     EventError,
					FilePath: zipFile.Name,
				})
			}
			continue
		}

		// Copy data with progress tracking
		var written, lastReported int64
		for {
			nr, errRead := rc.Read(buf)
			if nr > 0 {
				nw, errWrite := outFile.Write(buf[0:nr])
				if errWrite != nil {
					outFile.Close()
					rc.Close()
					recordError(fmt.Errorf("%s: write: %w", zipFile.Name, errWrite))
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:     EventError,
							FilePath: zipFile.Name,
						})
					}
					break
				}
				written += int64(nw)

				// Report progress (throttled; EventFileComplete finishes the bar)
				if progressCb != nil && written-lastReported >= progressReportStep {
					lastReported = written
					progressCb(ProgressEvent{
						Type:     EventFileProgress,
						FilePath: zipFile.Name,
						Current:  written,
						Total:    int64(zipFile.UncompressedSize64),
					})
				}
			}
			if errRead == io.EOF {
				break
			}
			if errRead != nil {
				outFile.Close()
				rc.Close()
				recordError(fmt.Errorf("%s: read: %w", zipFile.Name, errRead))
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventError,
						FilePath: zipFile.Name,
					})
				}
				break
			}
		}

		outFile.Close()
		rc.Close()

		// Track stats
		mu.Lock()
		result.FilesProcessed++
		result.DecompressedSize += zipFile.UncompressedSize64
		result.CompressedSize += zipFile.CompressedSize64
		mu.Unlock()

		// Notify file complete
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileComplete,
				FilePath: zipFile.Name,
				Current:  int64(zipFile.UncompressedSize64),
				Total:    int64(zipFile.UncompressedSize64),
			})
		}
	}

	return nil
}
