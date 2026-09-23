package secure

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripUsesRandomNonce(t *testing.T) {
	cipher, err := NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	first, err := cipher.Encrypt("sk-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := cipher.Encrypt("sk-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("ciphertexts should differ because each encryption needs a fresh nonce")
	}
	plaintext, err := cipher.Decrypt(first)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plaintext != "sk-secret" {
		t.Fatalf("unexpected plaintext: %q", plaintext)
	}
}
