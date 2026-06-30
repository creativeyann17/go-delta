// pkg/decompress/safepath.go
package decompress

import (
	"path/filepath"
	"strings"
)

// safeJoin joins an archive-supplied entry name onto outputDir and verifies
// the result still resolves inside outputDir. Archive formats (zip, tar,
// and this package's own dict/chunked/zstd formats) store entry paths as
// untrusted strings — without this check, an entry like "../../etc/passwd"
// or an absolute path lets extraction write anywhere the process can reach
// (zip-slip). Returns ErrUnsafeEntryPath if the entry tries to escape.
func safeJoin(outputDir, entryName string) (string, error) {
	cleanOutputDir := filepath.Clean(outputDir)
	joined := filepath.Join(cleanOutputDir, entryName)
	if joined != cleanOutputDir &&
		!strings.HasPrefix(joined, cleanOutputDir+string(filepath.Separator)) {
		return "", ErrUnsafeEntryPath
	}
	return joined, nil
}
