// Package idfile keeps identifiers that must survive restarts.
package idfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lscythe/jalvio-service/internal/atomicfile"
)

// LoadOrCreate returns the ID stored at path, storing one from generate on
// first use. The file is readable by its owner only.
func LoadOrCreate(path string, generate func() string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id == "" {
			return "", fmt.Errorf("ID file %s is empty", path)
		}
		return id, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	id := generate()
	if err := atomicfile.Write(path, []byte(id+"\n")); err != nil {
		return "", err
	}
	return id, nil
}
