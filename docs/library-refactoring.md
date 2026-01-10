# Library Refactoring Summary

## What Changed

The compression logic has been extracted from the CLI into a reusable library package, allowing go-delta to be used both as a command-line tool and as a Go library.

## New Structure

### Public API (`pkg/compress/`)

**`pkg/compress/compress.go`**
- Main `Compress()` function - the entry point for library users
- `ProgressCallback` type for tracking compression progress
- Internal worker pool and file compression logic

**`pkg/compress/options.go`**
- `Options` struct - configures compression behavior
- `DefaultOptions()` - provides sensible defaults
- `Validate()` - validates and normalizes options

**`pkg/compress/result.go`**
- `Result` struct - contains compression statistics
- `CompressionRatio()` - calculates compression percentage
- `Success()` - checks if operation completed without errors

**`pkg/compress/errors.go`**
- Predefined error types:
  - `ErrInputRequired` - input path not specified
  - `ErrInvalidLevel` - compression level out of range (1-22)
  - `ErrNoFiles` - no files found to compress

### CLI (`cmd/godelta/`)

**`cmd/godelta/compress_cmd.go`**
- Cobra command definition
- Flag parsing and validation
- Progress bar setup using `pb/v3`
- Calls `compress.Compress()` with appropriate callbacks
- Formats and displays results

## Benefits

### For Library Users

```go
import "github.com/yourusername/go-delta/pkg/compress"

// Simple usage
opts := compress.DefaultOptions()
opts.InputPath = "/data"
opts.OutputPath = "backup.delta"
result, err := compress.Compress(opts, nil)

// With progress tracking
result, err := compress.Compress(opts, func(e compress.ProgressEvent) {
    if e.Type == compress.EventFileComplete {
        fmt.Printf("✓ %s\n", e.FilePath)
    }
})
```

### For CLI Users

No changes - the CLI works exactly the same:

```bash
godelta compress -i /data -o backup.delta --threads 8 --level 9
```

## Migration Guide

### From Old compress.go

**Before:**
- All logic in `cmd/godelta/compress.go`
- Tightly coupled to Cobra, pb/v3
- Not reusable

**After:**
- Core logic in `pkg/compress/`
- Clean separation of concerns
- CLI is a thin wrapper around library
- Other Go programs can import and use the library

### Key Differences

1. **Options Structure**: Instead of cobra flags, use `compress.Options`
2. **Progress Tracking**: Instead of progress bars, use `ProgressCallback`
3. **Error Handling**: Library returns `Result` with errors list instead of printing

## Testing the Library

Create a simple test program:

```go
// test/main.go
package main

import (
    "fmt"
    "log"
    "github.com/yourusername/go-delta/pkg/compress"
)

func main() {
    opts := &compress.Options{
        InputPath:  "testdata",
        OutputPath: "test.delta",
        Level:      5,
        MaxThreads: 4,
    }
    
    result, err := compress.Compress(opts, nil)
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("Success! Compressed %d files\n", result.FilesProcessed)
    fmt.Printf("Ratio: %.1f%%\n", result.CompressionRatio())
}
```

## What Stayed the Same

- Archive format (`internal/format/`)
- Compression algorithm (zstd)
- Worker pool implementation
- Overall behavior and performance

## What's Better

✅ **Separation of Concerns** - CLI and library logic separated
✅ **Reusability** - Other Go programs can use the compression library
✅ **Testability** - Library functions easier to unit test
✅ **Maintainability** - Changes to CLI don't affect library
✅ **Documentation** - Clear API with examples
✅ **Flexibility** - Library users can customize progress handling

## Next Steps

1. Add unit tests for `pkg/compress/`
2. Add integration tests
3. Document the archive format in `internal/format/`
4. Implement decompression library
5. Add examples for more use cases
