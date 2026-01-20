// pkg/compress/compress_zip_bench_test.go
package compress

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkZipCompression benchmarks ZIP compression with and without GC
func BenchmarkZipCompression(b *testing.B) {
	// Setup: create temp directory with test files
	tempDir := b.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		b.Fatalf("Failed to create input dir: %v", err)
	}

	// Create a mix of files with realistic data
	for i := 0; i < 50; i++ {
		filename := filepath.Join(inputDir, fmt.Sprintf("file%04d.txt", i))
		// Create files with semi-compressible content
		content := make([]byte, 32*1024) // 32KB each
		for j := range content {
			content[j] = byte((i + j) % 256)
		}
		if err := os.WriteFile(filename, content, 0644); err != nil {
			b.Fatalf("Failed to write file %d: %v", i, err)
		}
	}

	b.Run("WithGC", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			outputZip := filepath.Join(tempDir, fmt.Sprintf("with_gc_%d.zip", i))
			opts := &Options{
				InputPath:    inputDir,
				OutputPath:   outputZip,
				MaxThreads:   4,
				Level:        5,
				UseZipFormat: true,
				DisableGC:    false,
				Quiet:        true,
			}
			result, err := Compress(opts, nil)
			if err != nil {
				b.Fatalf("Compress failed: %v", err)
			}
			b.ReportMetric(float64(result.FilesProcessed), "files")
			b.ReportMetric(float64(result.OriginalSize)/(1024*1024), "MB_in")
		}
	})

	b.Run("NoGC", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			outputZip := filepath.Join(tempDir, fmt.Sprintf("no_gc_%d.zip", i))
			opts := &Options{
				InputPath:    inputDir,
				OutputPath:   outputZip,
				MaxThreads:   4,
				Level:        5,
				UseZipFormat: true,
				DisableGC:    true,
				Quiet:        true,
			}
			result, err := Compress(opts, nil)
			if err != nil {
				b.Fatalf("Compress failed: %v", err)
			}
			b.ReportMetric(float64(result.FilesProcessed), "files")
			b.ReportMetric(float64(result.OriginalSize)/(1024*1024), "MB_in")
		}
	})
}

// BenchmarkZipManySmallFiles benchmarks ZIP compression with many small files
func BenchmarkZipManySmallFiles(b *testing.B) {
	tempDir := b.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		b.Fatalf("Failed to create input dir: %v", err)
	}

	// Create many small files (worst case for per-file allocations)
	numFiles := 200
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(inputDir, fmt.Sprintf("small%04d.txt", i))
		content := make([]byte, 1024) // 1KB each
		for j := range content {
			content[j] = byte((i + j) % 256)
		}
		if err := os.WriteFile(filename, content, 0644); err != nil {
			b.Fatalf("Failed to write file %d: %v", i, err)
		}
	}

	b.Run("WithGC", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			outputZip := filepath.Join(tempDir, fmt.Sprintf("small_with_gc_%d.zip", i))
			opts := &Options{
				InputPath:    inputDir,
				OutputPath:   outputZip,
				MaxThreads:   4,
				Level:        5,
				UseZipFormat: true,
				DisableGC:    false,
				Quiet:        true,
			}
			result, err := Compress(opts, nil)
			if err != nil {
				b.Fatalf("Compress failed: %v", err)
			}
			b.ReportMetric(float64(result.FilesProcessed), "files")
		}
	})

	b.Run("NoGC", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			outputZip := filepath.Join(tempDir, fmt.Sprintf("small_no_gc_%d.zip", i))
			opts := &Options{
				InputPath:    inputDir,
				OutputPath:   outputZip,
				MaxThreads:   4,
				Level:        5,
				UseZipFormat: true,
				DisableGC:    true,
				Quiet:        true,
			}
			result, err := Compress(opts, nil)
			if err != nil {
				b.Fatalf("Compress failed: %v", err)
			}
			b.ReportMetric(float64(result.FilesProcessed), "files")
		}
	})
}

// BenchmarkZipFewLargeFiles benchmarks ZIP compression with few large files
func BenchmarkZipFewLargeFiles(b *testing.B) {
	tempDir := b.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		b.Fatalf("Failed to create input dir: %v", err)
	}

	// Create a few large files
	numFiles := 5
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(inputDir, fmt.Sprintf("large%04d.bin", i))
		content := make([]byte, 1024*1024) // 1MB each
		for j := range content {
			content[j] = byte((i + j) % 256)
		}
		if err := os.WriteFile(filename, content, 0644); err != nil {
			b.Fatalf("Failed to write file %d: %v", i, err)
		}
	}

	b.Run("WithGC", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			outputZip := filepath.Join(tempDir, fmt.Sprintf("large_with_gc_%d.zip", i))
			opts := &Options{
				InputPath:    inputDir,
				OutputPath:   outputZip,
				MaxThreads:   4,
				Level:        5,
				UseZipFormat: true,
				DisableGC:    false,
				Quiet:        true,
			}
			result, err := Compress(opts, nil)
			if err != nil {
				b.Fatalf("Compress failed: %v", err)
			}
			b.ReportMetric(float64(result.FilesProcessed), "files")
			b.ReportMetric(float64(result.OriginalSize)/(1024*1024), "MB_in")
		}
	})

	b.Run("NoGC", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			outputZip := filepath.Join(tempDir, fmt.Sprintf("large_no_gc_%d.zip", i))
			opts := &Options{
				InputPath:    inputDir,
				OutputPath:   outputZip,
				MaxThreads:   4,
				Level:        5,
				UseZipFormat: true,
				DisableGC:    true,
				Quiet:        true,
			}
			result, err := Compress(opts, nil)
			if err != nil {
				b.Fatalf("Compress failed: %v", err)
			}
			b.ReportMetric(float64(result.FilesProcessed), "files")
			b.ReportMetric(float64(result.OriginalSize)/(1024*1024), "MB_in")
		}
	})
}
