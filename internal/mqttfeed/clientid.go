package mqttfeed

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/lscythe/jalvio-service/internal/atomicfile"
	"github.com/lscythe/jalvio-service/internal/idfile"
)

// LoadOrCreateClientID returns the client ID stored at path, creating a
// random one on first use so the broker sees the same client across restarts.
// A stored ID without the prefix is replaced, since brokers may authorise
// clients by their ID prefix.
func LoadOrCreateClientID(path, prefix string) (string, error) {
	id, err := idfile.LoadOrCreate(path, func() string { return newClientID(prefix) })
	if err != nil || strings.HasPrefix(id, prefix) {
		return id, err
	}
	id = newClientID(prefix)
	return id, atomicfile.Write(path, []byte(id+"\n"))
}

func newClientID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // never fails; see crypto/rand.Read
	return prefix + hex.EncodeToString(b)
}
