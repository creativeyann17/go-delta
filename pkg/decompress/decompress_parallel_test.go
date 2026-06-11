// pkg/decompress/decompress_parallel_test.go
package decompress_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/creativeyann17/go-delta/pkg/compress"
	"github.com/creativeyann17/go-delta/pkg/decompress"
)

// buildTestInput creates a directory tree with many small files plus files
// sharing duplicated blocks (dedup-friendly for chunked mode).
func buildTestInput(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)

	shared := bytes.Repeat([]byte("shared block content 0123456789 abcdefghij "), 2048) // ~88KB

	for i := 0; i < 60; i++ {
		rel := fmt.Sprintf("sub%d/file_%03d.txt", i%4, i)
		content := []byte(fmt.Sprintf("file %d unique line\n", i))
		if i%3 == 0 {
			// Mix shared data in so chunked mode dedups across files
			content = append(append([]byte{}, shared...), content...)
		}
		files[rel] = content

		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, content, 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Empty file edge case
	files["empty.txt"] = []byte{}
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0644); err != nil {
		t.Fatalf("write empty: %v", err)
	}

	return files
}

func verifyOutput(t *testing.T, dir string, want map[string][]byte) {
	t.Helper()
	for rel, expected := range want {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("%s: content mismatch (got %d bytes, want %d)", rel, len(got), len(expected))
		}
	}
}

func roundTrip(t *testing.T, compressOpts *compress.Options, threads int, want map[string][]byte) {
	t.Helper()

	if _, err := compress.Compress(compressOpts, nil); err != nil {
		t.Fatalf("compress: %v", err)
	}

	extractDir := t.TempDir()
	result, err := decompress.Decompress(&decompress.Options{
		InputPath:  compressOpts.OutputPath,
		OutputPath: extractDir,
		MaxThreads: threads,
		Overwrite:  true,
		Quiet:      true,
	}, nil)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("decompress errors: %v", result.Errors)
	}
	if result.FilesProcessed != len(want) {
		t.Errorf("expected %d files processed, got %d", len(want), result.FilesProcessed)
	}

	verifyOutput(t, extractDir, want)
}

// TestParallelDecompressGDelta01 verifies parallel GDELTA01 decompression
// produces identical content at various thread counts.
func TestParallelDecompressGDelta01(t *testing.T) {
	inputDir := t.TempDir()
	want := buildTestInput(t, inputDir)

	for _, threads := range []int{1, 2, 8} {
		t.Run(fmt.Sprintf("threads=%d", threads), func(t *testing.T) {
			roundTrip(t, &compress.Options{
				InputPath:  inputDir,
				OutputPath: filepath.Join(t.TempDir(), "a.delta"),
				MaxThreads: 4,
				Level:      3,
				Quiet:      true,
			}, threads, want)
		})
	}
}

// TestParallelDecompressGDelta02 verifies parallel chunked decompression with
// the shared decompressed-chunk cache.
func TestParallelDecompressGDelta02(t *testing.T) {
	inputDir := t.TempDir()
	want := buildTestInput(t, inputDir)

	for _, threads := range []int{1, 2, 8} {
		t.Run(fmt.Sprintf("threads=%d", threads), func(t *testing.T) {
			roundTrip(t, &compress.Options{
				InputPath:  inputDir,
				OutputPath: filepath.Join(t.TempDir(), "a.delta"),
				MaxThreads: 4,
				ChunkSize:  16 * 1024,
				Level:      3,
				Quiet:      true,
			}, threads, want)
		})
	}
}

// TestParallelDecompressNoOverwrite verifies existing files are reported as
// errors (not overwritten) under parallel decompression.
func TestParallelDecompressNoOverwrite(t *testing.T) {
	inputDir := t.TempDir()
	buildTestInput(t, inputDir)

	archive := filepath.Join(t.TempDir(), "a.delta")
	if _, err := compress.Compress(&compress.Options{
		InputPath:  inputDir,
		OutputPath: archive,
		MaxThreads: 4,
		Level:      3,
		Quiet:      true,
	}, nil); err != nil {
		t.Fatalf("compress: %v", err)
	}

	extractDir := t.TempDir()
	// Pre-create one of the output files
	if err := os.WriteFile(filepath.Join(extractDir, "empty.txt"), []byte("keep me"), 0644); err != nil {
		t.Fatalf("pre-create: %v", err)
	}

	result, err := decompress.Decompress(&decompress.Options{
		InputPath:  archive,
		OutputPath: extractDir,
		MaxThreads: 4,
		Overwrite:  false,
		Quiet:      true,
	}, nil)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 file-exists error, got %d: %v", len(result.Errors), result.Errors)
	}
	got, _ := os.ReadFile(filepath.Join(extractDir, "empty.txt"))
	if string(got) != "keep me" {
		t.Errorf("existing file was overwritten")
	}
}
