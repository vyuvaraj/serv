package storage

import (
	"bytes"
	"testing"
)

func TestCryptoRoundTrip(t *testing.T) {
	key := deriveKey("my-test-passphrase")
	plaintext := []byte("Hello, ServStore encryption!")

	ciphertext, err := encryptPayload(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext must not equal plaintext")
	}

	decrypted, err := decryptPayload(key, ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestCryptoWrongKey(t *testing.T) {
	key1 := deriveKey("correct-passphrase")
	key2 := deriveKey("wrong-passphrase")
	plaintext := []byte("secret data")

	ciphertext, err := encryptPayload(key1, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = decryptPayload(key2, ciphertext)
	if err == nil {
		t.Fatal("expected decryption to fail with wrong key")
	}
}

func TestCryptoNonceUniqueness(t *testing.T) {
	key := deriveKey("passphrase")
	plaintext := []byte("same plaintext")

	c1, _ := encryptPayload(key, plaintext)
	c2, _ := encryptPayload(key, plaintext)

	if bytes.Equal(c1, c2) {
		t.Fatal("two encryptions of the same plaintext must produce different ciphertexts (nonce reuse)")
	}
}

func TestDeriveKeyLength(t *testing.T) {
	key := deriveKey("any passphrase")
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key))
	}
}
