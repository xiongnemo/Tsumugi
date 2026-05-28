package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/argon2"
)

const (
	keyringService = "Tsumugi"
	keyringUser    = "local-db"
)

var ErrPassphraseRequired = errors.New("passphrase required because OS keyring is unavailable")

type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func ResolveMasterKey(salt []byte) ([]byte, string, error) {
	if encoded, err := keyring.Get(keyringService, keyringUser); err == nil && encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, "", fmt.Errorf("decode keyring key: %w", err)
		}
		return key, "keyring", nil
	}

	if passphrase := os.Getenv("TSUMUGI_PASSPHRASE"); passphrase != "" {
		return DeriveKey(passphrase, salt), "passphrase", nil
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, "", err
	}
	if err := keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(key)); err == nil {
		return key, "keyring", nil
	}

	return nil, "", ErrPassphraseRequired
}

func DeriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
}

func (c *Cipher) Seal(table, rowID, field string, plaintext []byte) ([]byte, error) {
	if c == nil {
		return plaintext, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad := aad(table, rowID, field)
	out := make([]byte, 0, len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, nonce...)
	out = c.aead.Seal(out, nonce, plaintext, aad)
	return out, nil
}

func (c *Cipher) Open(table, rowID, field string, ciphertext []byte) ([]byte, error) {
	if c == nil {
		return ciphertext, nil
	}
	if len(ciphertext) < c.aead.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonceSize := c.aead.NonceSize()
	nonce := ciphertext[:nonceSize]
	body := ciphertext[nonceSize:]
	return c.aead.Open(nil, nonce, body, aad(table, rowID, field))
}

func aad(table, rowID, field string) []byte {
	sum := sha256.Sum256([]byte(table + "\x00" + rowID + "\x00" + field))
	return sum[:]
}

func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(rand.Reader, b)
	return b, err
}
