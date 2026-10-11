package stopinfo

import "strings"

// keyAliases maps upstream spellings to the English key clients expect.
var keyAliases = map[string]string{
	"eskalator": "escalator",
}

// FacilityKey turns an upstream facility name into a snake_case key.
func FacilityKey(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			gap = false
			continue
		}
		gap = true
	}
	key := b.String()
	if alias, ok := keyAliases[key]; ok {
		return alias
	}
	return key
}
