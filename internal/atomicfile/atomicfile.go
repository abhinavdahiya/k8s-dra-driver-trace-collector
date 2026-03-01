// Package atomicfile provides an atomic file write primitive.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile atomically writes data to path by creating a temp file in
// the same directory and renaming it into place. This ensures readers
// never see a partially-written file.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".atomic-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}
