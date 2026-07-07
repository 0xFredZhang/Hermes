package crypto

import (
	"strings"
	"testing"
)

func newTestCipher(t *testing.T) Cipher {
	t.Helper()
	c, err := NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	plain := "AKIA-secret-value-123"

	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(enc, plain) {
		t.Fatal("ciphertext must not contain the plaintext")
	}

	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("Decrypt = %q, want %q", dec, plain)
	}
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if a == b {
		t.Fatal("two encryptions of same plaintext must differ (random nonce)")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	c := newTestCipher(t)
	enc, _ := c.Encrypt("payload")
	tampered := enc[:len(enc)-2] + "AA" // flip last base64 chars
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}

func TestNewCipherRejectsBadKey(t *testing.T) {
	if _, err := NewCipher(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}
