package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

type Cipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

type gcmCipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &gcmCipher{aead: aead}, nil
}

func (g *gcmCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, g.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := g.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (g *gcmCipher) Decrypt(ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	ns := g.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	plain, err := g.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
