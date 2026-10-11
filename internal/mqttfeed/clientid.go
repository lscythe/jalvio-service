package mqttfeed

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lscythe/jalvio-service/internal/atomicfile"
)

// LoadOrCreateClientID returns the client ID stored at path, creating a
// random one on first use so the broker sees the same client across restarts.
func LoadOrCreateClientID(path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id == "" {
			return "", fmt.Errorf("client ID file %s is empty", path)
		}
		return id, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	id := "jalvio-" + hex.EncodeToString(randomBytes(8))
	if err := atomicfile.Write(path, []byte(id+"\n")); err != nil {
		return "", err
	}
	return id, nil
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails; see crypto/rand.Read
	return b
}
