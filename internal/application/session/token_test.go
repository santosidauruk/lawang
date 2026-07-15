package session_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestCryptoTokensIssuesThirtyTwoRandomBytesAndHashesDeterministically(t *testing.T) {
	random := bytes.Repeat([]byte{0x5a}, 32)
	tokens := session.NewCryptoTokens(bytes.NewReader(random))

	raw, hash, err := tokens.Issue()
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	wantRaw := base64.RawURLEncoding.EncodeToString(random)
	wantHash := sha256.Sum256([]byte(wantRaw))
	if raw != wantRaw {
		t.Errorf("Issue() raw = %q, want base64url of 32 random bytes", raw)
	}
	if !bytes.Equal(hash, wantHash[:]) {
		t.Errorf("Issue() hash = %x, want SHA-256 %x", hash, wantHash)
	}
	if !tokens.Equal(tokens.Hash(raw), hash) {
		t.Fatal("Equal() rejected the deterministic hash of the issued token")
	}
	if tokens.Equal(tokens.Hash(raw+"x"), hash) {
		t.Fatal("Equal() accepted a different credential")
	}
}
