// pkg/decompress/decompress_xz.go
package decompress

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"
)

// decompressXz extracts files from standard .tar.xz archive(s)
// Supports both single archives and multi-part archives (archive_01.tar.xz, archive_02.tar.xz, ...)
func decompressXz(opts *Options, progressCb ProgressCallback, result *Result) error {
	// Detect if this is a multi-part archive (ends with _XX.tar.xz pattern)
	xzPaths := []string{opts.InputPath}

	baseName := filepath.Base(opts.InputPath)
	if strings.Contains(baseName, "_") && strings.HasSuffix(baseName, ".tar.xz") {
		// Check if this looks like archive_01.tar.xz pattern
		nameWithoutExt := baseName[:len(baseName)-7] // remove .tar.xz
		parts := strings.Split(nameWithoutExt, "_")
		if len(parts) >= 2 {
			lastPart := parts[len(parts)-1]
			// Check if last part is a number
			if len(lastPart) == 2 && lastPart[0] >= '0' && lastPart[0] <= '9' && lastPart[1] >= '0' && lastPart[1] <= '9' {
				// Multi-part archive detected - find all parts
				basePattern := strings.Join(parts[:len(parts)-1], "_")
				dirPath := filepath.Dir(opts.InputPath)

				// Find all matching parts
				xzPaths = []string{}
				for i := 1; i <= 99; i++ { // Support up to 99 parts
					partPath := filepath.Join(dirPath, fmt.Sprintf("%s_%02d.tar.xz", basePattern, i))
					if _, err := os.Stat(partPath); err == nil {
						xzPaths = append(xzPaths, partPath)
					} else {
						break // No more parts
					}
				}

				if len(xzPaths) == 0 {
					return fmt.Errorf("no multi-part archive files found matching pattern: %s_XX.tar.xz", basePattern)
				}
			}
		}
	}

	// Count total files across all archives (quick scan)
	var totalFiles int
	if !opts.Quiet && len(xzPaths) > 1 {
		fmt.Printf("Detecting multi-part archive: scanning %d parts...\n", len(xzPaths))
	}
	for _, xzPath := range xzPaths {
		count, err := countTarXzFiles(xzPath)
		if err != nil {
			return fmt.Errorf("scan archive %s: %w", xzPath, err)
		}
		totalFiles += count
	}
	if !opts.Quiet && len(xzPaths) > 1 {
		fmt.Printf("Found %d files across %d archive parts\n\n", totalFiles, len(xzPaths))
	}

	result.FilesTotal = totalFiles

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:  EventStart,
			Total: int64(totalFiles),
		})
	}

	// Extract each archive in sequence
	for _, xzPath := range xzPaths {
		if err := extractTarXzFile(xzPath, opts, progressCb, result); err != nil {
			return fmt.Errorf("extract %s: %w", xzPath, err)
		}
	}

	if progressCb != nil {
		progressCb(ProgressEvent{
			Type:    EventComplete,
			Current: int64(result.FilesProcessed),
			Total:   int64(totalFiles),
		})
	}

	return nil
}

// countTarXzFiles counts the number of files in a .tar.xz archive
func countTarXzFiles(xzPath string) (int, error) {
	file, err := os.Open(xzPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	xzReader, err := xz.NewReader(file)
	if err != nil {
		return 0, err
	}

	tarReader := tar.NewReader(xzReader)
	count := 0
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		if header.Typeflag == tar.TypeReg {
			count++
		}
	}
	return count, nil
}

// extractTarXzFile extracts a single .tar.xz archive
func extractTarXzFile(xzPath string, opts *Options, progressCb ProgressCallback, result *Result) error {
	file, err := os.Open(xzPath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	// Get archive size for stats
	stat, _ := file.Stat()
	if stat != nil {
		result.CompressedSize += uint64(stat.Size())
	}

	xzReader, err := xz.NewReader(file)
	if err != nil {
		return fmt.Errorf("create xz reader: %w", err)
	}

	tarReader := tar.NewReader(xzReader)

	// Extract each file
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}

		// Skip directories (they'll be created as needed)
		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Notify file start
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileStart,
				FilePath: header.Name,
				Total:    header.Size,
			})
		}

		// Construct output path
		outPath := filepath.Join(opts.OutputPath, header.Name)

		// Check if file already exists
		if !opts.Overwrite {
			if _, err := os.Stat(outPath); err == nil {
				err := fmt.Errorf("%s: file exists (use --overwrite to replace)", header.Name)
				result.Errors = append(result.Errors, err)

				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventError,
						FilePath: header.Name,
					})
				}
				// Skip the file data
				if _, err := io.CopyN(io.Discard, tarReader, header.Size); err != nil && err != io.EOF {
					return fmt.Errorf("skip file data: %w", err)
				}
				continue
			}
		}

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: mkdir: %w", header.Name, err))
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:     EventError,
					FilePath: header.Name,
				})
			}
			// Skip the file data
			if _, err := io.CopyN(io.Discard, tarReader, header.Size); err != nil && err != io.EOF {
				return fmt.Errorf("skip file data: %w", err)
			}
			continue
		}

		// Create output file
		outFile, err := os.Create(outPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: create: %w", header.Name, err))
			if progressCb != nil {
				progressCb(ProgressEvent{
					Type:     EventError,
					FilePath: header.Name,
				})
			}
			// Skip the file data
			if _, err := io.CopyN(io.Discard, tarReader, header.Size); err != nil && err != io.EOF {
				return fmt.Errorf("skip file data: %w", err)
			}
			continue
		}

		// Copy data with progress tracking
		var written int64
		buf := make([]byte, 32*1024) // 32KB buffer
		for {
			nr, errRead := tarReader.Read(buf)
			if nr > 0 {
				nw, errWrite := outFile.Write(buf[0:nr])
				if errWrite != nil {
					outFile.Close()
					result.Errors = append(result.Errors, fmt.Errorf("%s: write: %w", header.Name, errWrite))
					if progressCb != nil {
						progressCb(ProgressEvent{
							Type:     EventError,
							FilePath: header.Name,
						})
					}
					break
				}
				written += int64(nw)

				// Report progress
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventFileProgress,
						FilePath: header.Name,
						Current:  written,
						Total:    header.Size,
					})
				}
			}
			if errRead == io.EOF {
				break
			}
			if errRead != nil {
				outFile.Close()
				result.Errors = append(result.Errors, fmt.Errorf("%s: read: %w", header.Name, errRead))
				if progressCb != nil {
					progressCb(ProgressEvent{
						Type:     EventError,
						FilePath: header.Name,
					})
				}
				break
			}
		}

		outFile.Close()

		// Track stats
		result.FilesProcessed++
		result.DecompressedSize += uint64(header.Size)

		// Notify file complete
		if progressCb != nil {
			progressCb(ProgressEvent{
				Type:     EventFileComplete,
				FilePath: header.Name,
				Current:  header.Size,
				Total:    header.Size,
			})
		}
	}

	return nil
}
