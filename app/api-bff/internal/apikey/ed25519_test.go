package apikey

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

type memoryCredentialStore struct{ credential Credential }

func (s memoryCredentialStore) Lookup(context.Context, string) (Credential, error) {
	return s.credential, nil
}

type memoryNonceStore struct{ claimed map[string]bool }

func (s *memoryNonceStore) Claim(_ context.Context, key string, _ time.Duration) (bool, error) {
	if s.claimed[key] {
		return false, nil
	}
	s.claimed[key] = true
	return true, nil
}

func TestEd25519AuthenticatorAcceptsOnceAndRejectsReplay(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_792_650_000, 0)
	bodyHash := sha256.Sum256(nil)
	request := Ed25519Request{
		KeyID: "local-ed25519-1", Method: "GET", URI: "/api/v3/open/v2/profile",
		Timestamp: "1792650000", Nonce: base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		Digest: "SHA-256=" + base64.StdEncoding.EncodeToString(bodyHash[:]),
	}
	request.ComputedDigest = request.Digest
	request.Authorization = "Ed25519 signature=\"" + base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(request.SigningString()))) + "\""
	authenticator := NewEd25519Authenticator(
		memoryCredentialStore{credential: Credential{KeyID: request.KeyID, MerchantID: "local-org", PublicKey: public, Status: "active", Permissions: []string{"profile.read"}}},
		&memoryNonceStore{claimed: map[string]bool{}},
	)
	authenticator.now = func() time.Time { return now }

	result, err := authenticator.Authenticate(context.Background(), request)
	if err != nil || !result.Valid || result.MerchantID != "local-org" {
		t.Fatalf("expected valid request, result=%+v err=%v", result, err)
	}
	replayed, err := authenticator.Authenticate(context.Background(), request)
	if err != nil || replayed.Valid {
		t.Fatalf("expected replay rejection, result=%+v err=%v", replayed, err)
	}
}
