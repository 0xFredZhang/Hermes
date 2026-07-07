package crypto

import (
	"crypto/hkdf"
	"crypto/sha256"
)

// DeriveKey derives a 32-byte key from a master key using HKDF-SHA256.
// The info parameter provides domain separation (e.g., "hermes:aes-gcm:v1").
func DeriveKey(master []byte, info string) ([]byte, error) {
	return hkdf.Key(sha256.New, master, nil, info, 32)
}
