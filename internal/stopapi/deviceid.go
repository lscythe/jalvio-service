package stopapi

import (
	"crypto/rand"
	"fmt"

	"github.com/lscythe/jalvio-service/internal/idfile"
)

// LoadOrCreateDeviceID returns the device ID stored at path, creating a
// random UUID on first use so the upstream sees one device across restarts.
func LoadOrCreateDeviceID(path string) (string, error) {
	return idfile.LoadOrCreate(path, func() string {
		b := make([]byte, 16)
		_, _ = rand.Read(b) // never fails; see crypto/rand.Read
		b[6] = b[6]&0x0f | 0x40
		b[8] = b[8]&0x3f | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	})
}
