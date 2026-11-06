package ai

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

const secretKeyBytes = 32

// secretBox encrypts vendor API keys, so a database dump alone does not expose them.
type secretBox struct {
	aead cipher.AEAD
}

// loadSecretBox reads the server key from the file, creating it on first start.
// The file lives apart from the database and must never reach git.
func loadSecretBox(path string) (*secretBox, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		raw, err = createSecretKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read secret key: %w", err)
	}

	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != secretKeyBytes {
		return nil, fmt.Errorf("secret key in %s is invalid", path)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretBox{aead: aead}, nil
}

func createSecretKey(path string) ([]byte, error) {
	key := make([]byte, secretKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	encoded := []byte(hex.EncodeToString(key))
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, err
	}
	return encoded, nil
}

// seal binds the ciphertext to the account, so it cannot be moved to another row.
func (b *secretBox) seal(plain, userID string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plain), []byte(userID)), nil
}

func (b *secretBox) open(sealed []byte, userID string) (string, error) {
	size := b.aead.NonceSize()
	if len(sealed) < size {
		return "", errors.New("sealed value is too short")
	}
	plain, err := b.aead.Open(nil, sealed[:size], sealed[size:], []byte(userID))
	if err != nil {
		return "", errors.New("api key cannot be decrypted, enter it again")
	}
	return string(plain), nil
}
