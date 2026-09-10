package appconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	pbkdf2Iterations = 100_000
	keyLength        = 32
	ivLength         = 16
)

func deriveKey(saltBase64, machineID, username string) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(saltBase64)
	if err != nil {
		return nil, fmt.Errorf("decode configuration salt: %w", err)
	}
	return pbkdf2SHA256([]byte(machineID+":"+username), salt, pbkdf2Iterations, keyLength), nil
}

func encryptValue(plaintext string, key []byte, random io.Reader) (string, error) {
	if len(key) != keyLength {
		return "", errors.New("configuration encryption key must be 32 bytes")
	}
	iv := make([]byte, ivLength)
	if _, err := io.ReadFull(random, iv); err != nil {
		return "", fmt.Errorf("generate configuration IV: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, iv, []byte(plaintext), nil)
	tagOffset := len(sealed) - gcm.Overhead()
	return strings.Join([]string{
		base64.StdEncoding.EncodeToString(iv),
		base64.StdEncoding.EncodeToString(sealed[tagOffset:]),
		base64.StdEncoding.EncodeToString(sealed[:tagOffset]),
	}, ":"), nil
}

func decryptValue(value string, key []byte) (string, error) {
	if len(key) != keyLength {
		return "", errors.New("configuration encryption key must be 32 bytes")
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return "", errors.New("invalid encrypted configuration value")
	}
	iv, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode configuration IV: %w", err)
	}
	if len(iv) != ivLength {
		return "", errors.New("invalid encrypted configuration IV length")
	}
	tag, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode configuration authentication tag: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("decode encrypted configuration value: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(tag) != gcm.Overhead() {
		return "", errors.New("invalid encrypted configuration authentication tag length")
	}
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plaintext, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt configuration value: %w", err)
	}
	return string(plaintext), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create configuration cipher: %w", err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, ivLength)
	if err != nil {
		return nil, fmt.Errorf("create configuration GCM: %w", err)
	}
	return gcm, nil
}

func pbkdf2SHA256(password, salt []byte, iterations, length int) []byte {
	blocks := (length + sha256.Size - 1) / sha256.Size
	key := make([]byte, 0, blocks*sha256.Size)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		var counter [4]byte
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		_, _ = mac.Write(counter[:])
		value := mac.Sum(nil)
		result := append([]byte(nil), value...)
		for round := 1; round < iterations; round++ {
			mac.Reset()
			_, _ = mac.Write(value)
			value = mac.Sum(nil)
			for index := range result {
				result[index] ^= value[index]
			}
		}
		key = append(key, result...)
	}
	return key[:length]
}
