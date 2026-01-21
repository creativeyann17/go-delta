# Changelog

## v1.3.0

- Add XZ compression format with `--xz` flag (LZMA2, best compression ratio)
- Add ZIP archive verification with full structural and data integrity validation
- Add multi-part archive verification (auto-detects and verifies all parts)
- Add SHA256 checksums to releases
- Refactor shared I/O utilities for better code reuse
- Centralize format detection logic
- Bump to Go 1.25

## v1.2.0

- Add `--no-gc` flag for ZIP compression to reduce memory pressure
- Implement GDELTA03 dictionary compression format

## v1.1.0

- Add `.gitignore` support to exclude files during compression

## v1.0.1

- Fix handling of empty files during compression

## v1.0.0

- Add `verify` command for archive integrity checking
- Performance optimizations

## v0.0.10

- Add parallelism modes (folder-based, file-based, auto)
- Add chunk streaming for memory efficiency

## v0.0.9

- Auto-detect optimal memory parameters based on system RAM

## v0.0.8

- Add output directory support for decompression
- Add compressed chunk size reporting
- Switch to FastCDC for content-defined chunking
- Fix Windows infinite loop issue

## v0.0.7

- Remove unnecessary stat calls for performance

## v0.0.6

- Fix relative file path handling

## v0.0.5

- Add shared progress callback for library usage

## v0.0.4

- Add custom file filtering feature

## v0.0.3

- Add ZIP format support
- Compress release binaries

## v0.0.2

- Add GDELTA02 chunk-based deduplication format

## v0.0.1

- Initial release
- Multi-threaded compression by folders
- Progress bars with mpb
- Basic compress/decompress functionality
- GDELTA01 zstd format
