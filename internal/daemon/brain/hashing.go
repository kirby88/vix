package brain

import (
	"crypto/sha256"
	"fmt"
)

// ContentHash returns the SHA-256 hex digest of data.
func ContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// SymbolID returns a stable 16-char hex identifier for a symbol.
func SymbolID(filePath, name, kind string) string {
	key := filePath + "|" + name + "|" + kind
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h)[:16]
}
