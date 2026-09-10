package appconfig

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestDeriveKeyMatchesLegacyPBKDF2Vector(t *testing.T) {
	key, err := deriveKey("bGVnYWN5LWNvbmZpZy1zYWx0", "machine-id", "alice")
	if err != nil {
		t.Fatalf("deriveKey() returned an error: %v", err)
	}
	if got, want := hex.EncodeToString(key), "7f30e9a513db373c895c4d0282ada956e89e78411e6b17e65e6a9918bee5cd37"; got != want {
		t.Fatalf("deriveKey() = %s, want %s", got, want)
	}
}

func TestEncryptAndDecryptMatchLegacyAESGCMVector(t *testing.T) {
	key, err := deriveKey("bGVnYWN5LWNvbmZpZy1zYWx0", "machine-id", "alice")
	if err != nil {
		t.Fatalf("deriveKey() returned an error: %v", err)
	}

	got, err := encryptValue("token: hello \u4f60\u597d", key, bytes.NewReader([]byte("0123456789abcdef")))
	if err != nil {
		t.Fatalf("encryptValue() returned an error: %v", err)
	}
	const want = "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="
	if got != want {
		t.Fatalf("encryptValue() = %q, want %q", got, want)
	}

	plaintext, err := decryptValue(want, key)
	if err != nil {
		t.Fatalf("decryptValue() returned an error: %v", err)
	}
	if plaintext != "token: hello \u4f60\u597d" {
		t.Fatalf("decryptValue() = %q", plaintext)
	}
}

func TestDecryptValueRejectsInvalidSerialization(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	for _, value := range []string{"", "one:two", "not-base64:still-not-base64:also-not-base64"} {
		t.Run(value, func(t *testing.T) {
			if _, err := decryptValue(value, key); err == nil {
				t.Fatal("decryptValue() returned nil error")
			}
		})
	}
}
