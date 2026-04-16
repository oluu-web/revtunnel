package apikeys

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
PrefixLive = "lnt_live"
secretBytes = 32
prefixHexSize = 8
)

var (
	ErrInvalidKeyFormat = errors.New("Invalid API key format")
	ErrInvalidPrefix = errors.New("Invalid API key prefix")
)

type Key struct {
	Plaintext string
	Prefix string
	Hash string
}

func NewLiveKey () (*Key, error) {
	return newKey(PrefixLive)
}

func ParsePrefix(apiKey string) (string, error) {
	parts := strings.Split(apiKey, ".")
	if len(parts) != 2 {
		return "", ErrInvalidKeyFormat
	}
	if parts[0] == "" || parts[1] == "" {
		return "", ErrInvalidKeyFormat
	}
	if !strings.HasPrefix(parts[0], PrefixLive+"_") {
		return "", ErrInvalidPrefix
	}

	return  parts[0], nil
}

func Hash(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}

func Verify(apiKey, wantHash string) bool {
	got := Hash(apiKey)
	return subtle.ConstantTimeCompare([]byte(got), []byte(wantHash)) == 1
}

func newKey(kind string) (*Key, error) {
	tag, err := randomHex(prefixHexSize/2)
	if err != nil {
		return nil, fmt.Errorf("generate prefix tag: %w", err)
	}

	secret, err := randomURLSafe(secretBytes)
	if err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}

	prefix := fmt.Sprintf("%s_%s", kind, tag)
	plaintext := prefix + "." + secret

	return &Key{
		Plaintext: plaintext,
		Prefix: prefix,
		Hash: Hash(plaintext),
	}, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}