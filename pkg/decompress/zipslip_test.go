// pkg/decompress/zipslip_test.go
package decompress_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/creativeyann17/go-delta/pkg/decompress"
)

// TestDecompressZipRejectsPathTraversal crafts a zip whose entry name
// escapes the output directory (zip-slip) and asserts extraction neither
// writes outside the output dir nor silently succeeds.
func TestDecompressZipRejectsPathTraversal(t *testing.T) {
	srcDir := t.TempDir()
	zipPath := filepath.Join(srcDir, "evil.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)

	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write entry %s: %v", name, err)
		}
	}
	write("../escaped.txt", "should not land outside extract dir")
	write("safe.txt", "this one is fine")

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}

	extractDir := filepath.Join(t.TempDir(), "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("mkdir extract dir: %v", err)
	}

	result, err := decompress.Decompress(&decompress.Options{
		InputPath:  zipPath,
		OutputPath: extractDir,
		MaxThreads: 1,
		Overwrite:  true,
		Quiet:      true,
	}, nil)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	if len(result.Errors) == 0 {
		t.Fatal("expected an error for the path-traversal entry, got none")
	}

	// The malicious entry must not exist anywhere outside extractDir.
	escapedPath := filepath.Join(filepath.Dir(extractDir), "escaped.txt")
	if _, err := os.Stat(escapedPath); err == nil {
		t.Fatalf("zip-slip succeeded: file written outside output dir at %s", escapedPath)
	}

	// The well-behaved sibling entry must still extract normally.
	safePath := filepath.Join(extractDir, "safe.txt")
	data, err := os.ReadFile(safePath)
	if err != nil {
		t.Fatalf("safe.txt should have extracted normally: %v", err)
	}
	if string(data) != "this one is fine" {
		t.Errorf("safe.txt content mismatch: got %q", string(data))
	}
}
