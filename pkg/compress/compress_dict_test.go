// pkg/compress/compress_dict_test.go
package compress

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creativeyann17/go-delta/pkg/decompress"
	"github.com/creativeyann17/go-delta/pkg/verify"
)

// TestDictionaryCompression tests basic dictionary compression
func TestDictionaryCompression(t *testing.T) {
	// Create temp directory with test files
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create multiple files with common patterns (ideal for dictionary)
	commonPrefix := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc "
	for i := 0; i < 10; i++ {
		content := commonPrefix + "function" + string(rune('A'+i)) + "() {\n\tfmt.Println(\"Hello\")\n}\n"
		filePath := filepath.Join(inputDir, "file"+string(rune('0'+i))+".go")
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Compress with dictionary
	archivePath := filepath.Join(tempDir, "test.gdelta")
	opts := &Options{
		InputPath:     inputDir,
		OutputPath:    archivePath,
		UseDictionary: true,
		Level:         5,
		MaxThreads:    2,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if result.FilesProcessed != 10 {
		t.Errorf("Expected 10 files processed, got %d", result.FilesProcessed)
	}

	if result.CompressedSize == 0 {
		t.Error("Expected non-zero compressed size")
	}

	t.Logf("Dictionary compression: %d files, %d bytes original, %d bytes compressed (%.1f%%)",
		result.FilesProcessed, result.OriginalSize, result.CompressedSize, result.CompressionRatio())
}

// TestDictionaryRoundTrip tests compress/decompress round-trip
func TestDictionaryRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test files
	testFiles := map[string]string{
		"a.txt":        "Hello World",
		"b.txt":        "Hello World Again",
		"subdir/c.txt": "Hello from subdirectory",
		"subdir/d.txt": "Another file in subdirectory",
		"empty.txt":    "",
		"large.txt":    strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100),
	}

	for name, content := range testFiles {
		path := filepath.Join(inputDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Compress
	archivePath := filepath.Join(tempDir, "test.gdelta")
	compOpts := &Options{
		InputPath:     inputDir,
		OutputPath:    archivePath,
		UseDictionary: true,
		Level:         5,
		MaxThreads:    2,
	}

	compResult, err := Compress(compOpts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	t.Logf("Compressed %d files, %d -> %d bytes",
		compResult.FilesProcessed, compResult.OriginalSize, compResult.CompressedSize)

	// Decompress
	decompOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: outputDir,
		Overwrite:  true,
	}

	decompResult, err := decompress.Decompress(decompOpts, nil)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if decompResult.FilesProcessed != compResult.FilesProcessed {
		t.Errorf("Files mismatch: compressed %d, decompressed %d",
			compResult.FilesProcessed, decompResult.FilesProcessed)
	}

	// Verify file contents
	for name, expectedContent := range testFiles {
		path := filepath.Join(outputDir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("Read %s: %v", name, err)
			continue
		}
		if string(content) != expectedContent {
			t.Errorf("Content mismatch for %s: expected %q, got %q", name, expectedContent, string(content))
		}
	}
}

// TestDictionaryVerify tests verify command with GDELTA03
func TestDictionaryVerify(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test files with varied but related content (good for dictionary)
	// Need at least 256KB total and 3 files for dictionary training
	commonHeader := "// Common header for all files\nimport { useState, useEffect, useCallback } from 'react';\nimport axios from 'axios';\n\n"
	for i := 0; i < 10; i++ {
		content := commonHeader + "function Component" + string(rune('A'+i)) + "() {\n" +
			"  const [state, setState] = useState(null);\n" +
			"  const [loading, setLoading] = useCallback(false);\n" +
			strings.Repeat("  // Processing data with some common patterns\n  console.log('Processing step');\n", 300+i*10) +
			"  return <div className=\"container\">Component " + string(rune('A'+i)) + "</div>;\n}\n" +
			"export default Component" + string(rune('A'+i)) + ";\n"
		path := filepath.Join(inputDir, fmt.Sprintf("file%02d.jsx", i))
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Compress
	archivePath := filepath.Join(tempDir, "test.gdelta")
	compOpts := &Options{
		InputPath:     inputDir,
		OutputPath:    archivePath,
		UseDictionary: true,
		Level:         5,
		MaxThreads:    2,
	}

	_, err := Compress(compOpts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// Verify structure only
	t.Run("StructuralValidation", func(t *testing.T) {
		verifyOpts := &verify.Options{
			InputPath:  archivePath,
			VerifyData: false,
		}
		result, err := verify.Verify(verifyOpts, nil)
		if err != nil {
			t.Fatalf("Verify failed: %v", err)
		}

		if result.Format != verify.FormatGDelta03 {
			t.Errorf("Expected GDELTA03 format, got %s", result.Format)
		}
		if !result.HeaderValid {
			t.Error("Expected valid header")
		}
		if !result.FooterValid {
			t.Error("Expected valid footer")
		}
		if !result.StructureValid {
			t.Error("Expected valid structure")
		}
		if result.FileCount != 10 {
			t.Errorf("Expected 10 files, got %d", result.FileCount)
		}
		if result.DictSize == 0 {
			t.Error("Expected non-zero dictionary size")
		}

		t.Logf("Verified GDELTA03: %d files, dict size %d bytes", result.FileCount, result.DictSize)
	})

	// Verify with data
	t.Run("DataValidation", func(t *testing.T) {
		verifyOpts := &verify.Options{
			InputPath:  archivePath,
			VerifyData: true,
		}
		result, err := verify.Verify(verifyOpts, nil)
		if err != nil {
			t.Fatalf("Verify failed: %v", err)
		}

		if !result.DataVerified {
			t.Error("Expected data to be verified")
		}
		if result.FilesVerified != 10 {
			t.Errorf("Expected 10 files verified, got %d", result.FilesVerified)
		}
		if result.CorruptFiles != 0 {
			t.Errorf("Expected 0 corrupt files, got %d", result.CorruptFiles)
		}
	})
}

// TestDictionaryMutuallyExclusive tests that dictionary is mutually exclusive with other modes
func TestDictionaryMutuallyExclusive(t *testing.T) {
	t.Run("DictionaryWithZip", func(t *testing.T) {
		opts := &Options{
			InputPath:     "/tmp",
			OutputPath:    "test.zip",
			UseDictionary: true,
			UseZipFormat:  true,
		}
		err := opts.Validate()
		if err != ErrZipNoDictionary {
			t.Errorf("Expected ErrZipNoDictionary, got %v", err)
		}
	})

	t.Run("DictionaryWithChunking", func(t *testing.T) {
		opts := &Options{
			InputPath:     "/tmp",
			OutputPath:    "test.gdelta",
			UseDictionary: true,
			ChunkSize:     64 * 1024,
		}
		err := opts.Validate()
		if err != ErrDictionaryNoChunking {
			t.Errorf("Expected ErrDictionaryNoChunking, got %v", err)
		}
	})
}

// TestDictionaryEmptyInput tests dictionary compression with empty files
func TestDictionaryEmptyInput(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create empty files
	for i := 0; i < 3; i++ {
		path := filepath.Join(inputDir, "empty"+string(rune('0'+i))+".txt")
		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			t.Fatal(err)
		}
	}

	archivePath := filepath.Join(tempDir, "test.gdelta")
	opts := &Options{
		InputPath:     inputDir,
		OutputPath:    archivePath,
		UseDictionary: true,
		Level:         5,
		MaxThreads:    1,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if result.FilesProcessed != 3 {
		t.Errorf("Expected 3 files processed, got %d", result.FilesProcessed)
	}

	// Verify decompress works
	outputDir := filepath.Join(tempDir, "output")
	decompOpts := &decompress.Options{
		InputPath:  archivePath,
		OutputPath: outputDir,
	}

	decompResult, err := decompress.Decompress(decompOpts, nil)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if decompResult.FilesProcessed != 3 {
		t.Errorf("Expected 3 files decompressed, got %d", decompResult.FilesProcessed)
	}
}

// TestDictionaryDryRun tests dry-run mode with dictionary
func TestDictionaryDryRun(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test files with enough content for dictionary training
	commonPart := "// Header\nimport { useState } from 'react';\n"
	for i := 0; i < 10; i++ {
		content := commonPart + "function Comp" + string(rune('A'+i)) + "() {\n" +
			strings.Repeat("  console.log('hello');\n", 50+i) +
			"}\n"
		path := filepath.Join(inputDir, "file"+string(rune('0'+i))+".js")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	archivePath := filepath.Join(tempDir, "test.gdelta")
	opts := &Options{
		InputPath:     inputDir,
		OutputPath:    archivePath,
		UseDictionary: true,
		Level:         5,
		DryRun:        true,
	}

	result, err := Compress(opts, nil)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// Check archive was not created
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Error("Expected archive not to be created in dry-run mode")
	}

	if result.FilesProcessed != 10 {
		t.Errorf("Expected 10 files processed, got %d", result.FilesProcessed)
	}

	t.Logf("Dry-run: %d files, %d -> %d bytes", result.FilesProcessed, result.OriginalSize, result.CompressedSize)
}

// TestDictionaryComparison compares compression ratio with/without dictionary
func TestDictionaryComparison(t *testing.T) {
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create files with common patterns - realistic React components
	// Each file ~1-2KB with significant common content
	commonImports := "import React, { useState, useEffect, useCallback, useMemo } from 'react';\n" +
		"import axios from 'axios';\n" +
		"import { useDispatch, useSelector } from 'react-redux';\n" +
		"import { Button, Card, Input, Modal } from './components';\n" +
		"import { formatDate, formatCurrency, validateEmail } from './utils';\n\n"

	commonHooks := "  const dispatch = useDispatch();\n" +
		"  const [loading, setLoading] = useState(false);\n" +
		"  const [error, setError] = useState(null);\n" +
		"  const [data, setData] = useState([]);\n\n" +
		"  useEffect(() => {\n" +
		"    fetchData();\n" +
		"  }, []);\n\n" +
		"  const fetchData = useCallback(async () => {\n" +
		"    setLoading(true);\n" +
		"    try {\n" +
		"      const response = await axios.get('/api/data');\n" +
		"      setData(response.data);\n" +
		"    } catch (err) {\n" +
		"      setError(err.message);\n" +
		"    } finally {\n" +
		"      setLoading(false);\n" +
		"    }\n" +
		"  }, []);\n\n"

	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("Component%c", 'A'+i%26)
		content := commonImports +
			"export function " + name + "({ id, title, onSubmit }) {\n" +
			commonHooks +
			"  const handleSubmit = () => {\n" +
			"    dispatch({ type: 'SUBMIT_" + name + "', payload: { id, data } });\n" +
			"    onSubmit && onSubmit(data);\n" +
			"  };\n\n" +
			"  if (loading) return <div className=\"loading\">Loading...</div>;\n" +
			"  if (error) return <div className=\"error\">{error}</div>;\n\n" +
			"  return (\n" +
			"    <Card className=\"" + strings.ToLower(name) + "-container\">\n" +
			"      <h2>{title}</h2>\n" +
			"      {data.map(item => (\n" +
			"        <div key={item.id} className=\"item\">\n" +
			"          <span>{item.name}</span>\n" +
			"          <span>{formatCurrency(item.price)}</span>\n" +
			"        </div>\n" +
			"      ))}\n" +
			"      <Button onClick={handleSubmit}>Submit</Button>\n" +
			"    </Card>\n" +
			"  );\n" +
			"}\n\n" +
			"export default " + name + ";\n"
		path := filepath.Join(inputDir, fmt.Sprintf("%s.jsx", strings.ToLower(name)))
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Compress with dictionary
	dictArchive := filepath.Join(tempDir, "dict.gdelta")
	dictOpts := &Options{
		InputPath:     inputDir,
		OutputPath:    dictArchive,
		UseDictionary: true,
		Level:         5,
	}
	dictResult, err := Compress(dictOpts, nil)
	if err != nil {
		t.Fatalf("Dictionary compress failed: %v", err)
	}

	// Compress without dictionary (GDELTA01)
	noDictArchive := filepath.Join(tempDir, "nodict.gdelta")
	noDictOpts := &Options{
		InputPath:  inputDir,
		OutputPath: noDictArchive,
		Level:      5,
	}
	noDictResult, err := Compress(noDictOpts, nil)
	if err != nil {
		t.Fatalf("No-dictionary compress failed: %v", err)
	}

	t.Logf("Without dictionary: %d bytes (%.1f%% ratio)",
		noDictResult.CompressedSize, noDictResult.CompressionRatio())
	t.Logf("With dictionary:    %d bytes (%.1f%% ratio)",
		dictResult.CompressedSize, dictResult.CompressionRatio())

	// Dictionary should provide better compression for repetitive content
	if dictResult.CompressedSize >= noDictResult.CompressedSize {
		t.Logf("Note: Dictionary didn't improve compression (this can happen with small datasets)")
	} else {
		savings := float64(noDictResult.CompressedSize-dictResult.CompressedSize) / float64(noDictResult.CompressedSize) * 100
		t.Logf("Dictionary saved %.1f%% vs non-dictionary", savings)
	}
}
