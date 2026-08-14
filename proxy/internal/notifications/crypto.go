package notifications

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

type SecretCipher struct {
	aead cipher.AEAD
}

func NewSecretCipher(key string) (*SecretCipher, error) {
	if len(key) < 16 {
		return nil, fmt.Errorf("NOTIFICATION_ENCRYPTION_KEY must contain at least 16 characters")
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretCipher{aead: aead}, nil
}

func (c *SecretCipher) Encrypt(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(value), nil), nil
}

func (c *SecretCipher) Decrypt(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	nonceSize := c.aead.NonceSize()
	if len(value) < nonceSize {
		return "", fmt.Errorf("encrypted notification secret is truncated")
	}
	plain, err := c.aead.Open(nil, value[:nonceSize], value[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt notification secret: %w", err)
	}
	return string(plain), nil
}
