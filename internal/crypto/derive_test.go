package crypto

import (
	"testing"
)

func TestDeriveKey(t *testing.T) {
	tests := []struct {
		name      string
		master    []byte
		info      string
		wantLen   int
		shouldErr bool
	}{
		{
			name:    "same master and info produces same key",
			master:  []byte("test-master-key-32bytes-long!!!"),
			info:    "hermes:aes-gcm:v1",
			wantLen: 32,
		},
		{
			name:    "different info produces different key",
			master:  []byte("test-master-key-32bytes-long!!!"),
			info:    "hermes:session-hmac:v1",
			wantLen: 32,
		},
		{
			name:    "empty info is valid",
			master:  []byte("test-master-key-32bytes-long!!!"),
			info:    "",
			wantLen: 32,
		},
		{
			name:    "short master key is valid",
			master:  []byte("short"),
			info:    "test-info",
			wantLen: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := DeriveKey(tt.master, tt.info)
			if (err != nil) != tt.shouldErr {
				t.Fatalf("DeriveKey error = %v, wantErr %v", err, tt.shouldErr)
			}
			if len(key) != tt.wantLen {
				t.Fatalf("DeriveKey len = %d, want %d", len(key), tt.wantLen)
			}
		})
	}

	// Test determinism: same inputs produce same output
	t.Run("determinism: same input produces same key", func(t *testing.T) {
		master := []byte("test-master-key-32bytes-long!!!")
		info := "hermes:aes-gcm:v1"
		key1, err1 := DeriveKey(master, info)
		key2, err2 := DeriveKey(master, info)
		if err1 != nil || err2 != nil {
			t.Fatalf("DeriveKey errors: %v, %v", err1, err2)
		}
		for i := range key1 {
			if key1[i] != key2[i] {
				t.Fatal("DeriveKey is not deterministic: same inputs produced different keys")
			}
		}
	})

	// Test domain separation: different info produces different keys
	t.Run("domain separation: different info produces different keys", func(t *testing.T) {
		master := []byte("test-master-key-32bytes-long!!!")
		key1, err1 := DeriveKey(master, "hermes:aes-gcm:v1")
		key2, err2 := DeriveKey(master, "hermes:session-hmac:v1")
		if err1 != nil || err2 != nil {
			t.Fatalf("DeriveKey errors: %v, %v", err1, err2)
		}
		different := false
		for i := range key1 {
			if key1[i] != key2[i] {
				different = true
				break
			}
		}
		if !different {
			t.Fatal("DeriveKey domain separation failed: different info should produce different keys")
		}
	})
}
