// Package atomicfile writes files that survive a crash: the bytes land
// in a temporary file beside the target and replace it in one rename.
package atomicfile

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Write stores data at path with mode 0600, replacing what is there in
// one rename. A missing parent directory is made.
func Write(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteJSON stores v at path as indented JSON, the way the app's files
// read best by hand.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return Write(path, data)
}
