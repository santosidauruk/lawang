package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"io"
)

const resumeTokenBytes = 32

type CryptoTokens struct {
	random io.Reader
}

func NewCryptoTokens(random io.Reader) *CryptoTokens {
	return &CryptoTokens{random: random}
}

func NewProductionCryptoTokens() *CryptoTokens {
	return NewCryptoTokens(rand.Reader)
}

func (t *CryptoTokens) Issue() (string, []byte, error) {
	value := make([]byte, resumeTokenBytes)
	if _, err := io.ReadFull(t.random, value); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(value)
	return raw, t.Hash(raw), nil
}

func (*CryptoTokens) Hash(raw string) []byte {
	hash := sha256.Sum256([]byte(raw))
	return hash[:]
}

func (*CryptoTokens) Equal(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}
