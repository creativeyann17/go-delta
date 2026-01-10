# Copilot Instructions for go-delta

## Project Overview
go-delta is a smart delta compression tool for backups written in Go. It creates efficient delta archives from similar file sets using content-based deduplication and delta encoding.

**Purpose**: Reduce backup storage by detecting duplicates and computing deltas between file versions.

## Architecture

### Package Structure
```
cmd/godelta/     - CLI entry point using Cobra framework
internal/config/ - Configuration management (planned)
internal/dedup/  - Content-based deduplication logic (planned)
internal/delta/  - Delta compression algorithms (planned)
internal/format/ - Archive format handlers (planned)
pkg/             - Public APIs (if needed for library use)
```

**Design Principle**: Follow Go's standard layout with `internal/` for private packages and `cmd/` for executables.

### Key Dependencies
- **cobra**: CLI framework for subcommands (compress, decompress, stats, version)
- **pflag**: POSIX-style flag parsing (comes with Cobra)

## Development Workflow

### Building
```bash
make build          # Build for current platform → bin/godelta
make build-all      # Cross-compile for linux/darwin/windows (amd64/arm64) → dist/
make clean          # Remove bin/ and dist/
```

**Version Embedding**: The Makefile automatically injects version metadata via ldflags:
- `VERSION`: git tag or "dev"
- `COMMIT`: short git hash
- `DATE`: ISO 8601 timestamp

These populate the `version`, `commit`, and `date` variables in [cmd/godelta/main.go](cmd/godelta/main.go#L11-L13).

### Testing
```bash
make test    # Run all tests with verbose output
make fmt     # Format code using go fmt
```

### Running
```bash
make run     # Build and execute `./bin/godelta version`
```

## Code Conventions

### CLI Structure (Cobra)
- **Root command**: Defined in `rootCmd` with metadata (Use, Short, Long, Version)
- **Subcommands**: Register in `init()` via `rootCmd.AddCommand()`
- **Version info**: Automatically formatted as `{version} (commit {hash}, built {date})`

Example from [cmd/godelta/main.go](cmd/godelta/main.go#L30-L33):
```go
func init() {
    rootCmd.AddCommand(versionCmd())
}
```

### Version Variables
Always use build-time injected variables for versioning:
```go
var (
    version = "dev"    // Default fallback
    commit  = "none"
    date    = "unknown"
)
```

Override via Makefile ldflags, never hardcode versions.

### Error Handling
Exit with error message to stderr and non-zero status:
```go
if err := rootCmd.Execute(); err != nil {
    fmt.Fprintf(os.Stderr, "Error: %v\n", err)
    os.Exit(1)
}
```

## Planned Architecture (Not Yet Implemented)

### Internal Packages (Skeleton Only)
- **config/**: Will handle configuration files (possibly using Viper)
- **dedup/**: Content-addressed storage, chunking, hash-based dedup
- **delta/**: Binary delta algorithms (e.g., bsdiff, xdelta-like)
- **format/**: Archive serialization/deserialization

### Expected Subcommands
```
godelta compress <source> <archive>   # Create delta archive
godelta decompress <archive> <dest>   # Restore from archive
godelta stats <archive>               # Show compression stats
godelta version                       # Current implementation
```

## Key Files
- [Makefile](Makefile): Build system with cross-compilation and version injection
- [cmd/godelta/main.go](cmd/godelta/main.go): CLI entry point and Cobra setup
- [go.mod](go.mod): Dependencies (Cobra + Viper)

## Important Notes
- **Early Stage**: Most internal packages are empty stubs. Focus on CLI framework first.
- **Cross-Platform**: Build system supports Linux, macOS, Windows on amd64/arm64.
- **No README Content**: README.md is empty; refer to code comments and this file.
