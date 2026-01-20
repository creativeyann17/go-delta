## What's New

### Added
- **ZIP archive verification** - Full structural and data integrity validation for ZIP files
- **XZ compression format** - LZMA2 compression with `--xz` flag for best compression ratio
- **Multi-part archive verification** - Auto-detects and verifies all parts (ZIP and XZ)

### Improved
- Refactored shared I/O utilities for better code reuse
- Centralized format detection logic
- Updated documentation with verification performance notes

### Performance Notes
- ZIP verification is fast (reads central directory without decompression)
- XZ verification is slower (streaming format requires full decompression)
