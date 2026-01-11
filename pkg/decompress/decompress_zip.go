// pkg/decompress/decompress_zip.go
package decompress

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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

	// Extract each ZIP file in sequence
	for _, zipPath := range zipPaths {
		if err := extractZipFile(zipPath, opts, progressCb, result); err != nil {
			return fmt.Errorf("extract %s: %w", zipPath, err)
		}
	}

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

// extractZipFile extracts a single ZIP archive
func extractZipFile(zipPath string, opts *Options, progressCb ProgressCallback, result *Result) error {
	// Open ZIP archive
	zipReader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zipReader.Close()

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

		// Check if file already exists
		if !opts.Overwrite {
			if _, err := os.Stat(outPath); err == nil {
				err := fmt.Errorf("%s: file exists (use --overwrite to replace)", zipFile.Name)
				result.Errors = append(result.Errors, err)

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
			result.Errors = append(result.Errors, fmt.Errorf("%s: mkdir: %w", zipFile.Name, err))
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
			result.Errors = append(result.Errors, fmt.Errorf("%s: open: %w", zipFile.Name, err))
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
			result.Errors = append(result.Errors, fmt.Errorf("%s: create: %w", zipFile.Name, err))
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:     EventError,
					FilePath: zipFile.Name,
				})
			}
			continue
		}

		// Copy data with progress tracking
		var written int64
		buf := make([]byte, 32*1024) // 32KB buffer
		for {
			nr, errRead := rc.Read(buf)
			if nr > 0 {
				nw, errWrite := outFile.Write(buf[0:nr])
				if errWrite != nil {
					outFile.Close()
					rc.Close()
					result.Errors = append(result.Errors, fmt.Errorf("%s: write: %w", zipFile.Name, errWrite))
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:     EventError,
							FilePath: zipFile.Name,
						})
					}
					break
				}
				written += int64(nw)

				// Report progress
				if progressCb != nil {
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
				result.Errors = append(result.Errors, fmt.Errorf("%s: read: %w", zipFile.Name, errRead))
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
		result.FilesProcessed++
		result.DecompressedSize += zipFile.UncompressedSize64
		result.CompressedSize += zipFile.CompressedSize64

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
