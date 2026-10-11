package mqttfeed

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/lscythe/jalvio-service/internal/idfile"
)

// LoadOrCreateClientID returns the client ID stored at path, creating a
// random one on first use so the broker sees the same client across restarts.
func LoadOrCreateClientID(path string) (string, error) {
	return idfile.LoadOrCreate(path, func() string {
		b := make([]byte, 8)
		_, _ = rand.Read(b) // never fails; see crypto/rand.Read
		return "jalvio-" + hex.EncodeToString(b)
	})
}
