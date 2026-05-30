package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
)

type Cipher struct {
	gcm cipher.AEAD
}

func NewCipher(secret string) (*Cipher, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

func (c *Cipher) SealJSON(value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.gcm.Seal(nonce, nonce, plain, nil), nil
}

func (c *Cipher) OpenJSON(ciphertext []byte, value any) error {
	if len(ciphertext) < c.gcm.NonceSize() {
		return fmt.Errorf("ciphertext too short")
	}
	nonce := ciphertext[:c.gcm.NonceSize()]
	payload := ciphertext[c.gcm.NonceSize():]
	plain, err := c.gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, value)
}
