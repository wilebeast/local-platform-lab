package auth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"
)

type memoryCredentialStore struct {
	byEmail map[string]PasswordIdentity
	byID    map[string]PasswordIdentity
	err     error
}

func newMemoryCredentialStore(t *testing.T) *memoryCredentialStore {
	t.Helper()
	hash, err := HashPassword("local-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	identity := PasswordIdentity{
		UserID:           "local-user",
		NormalizedEmail:  "demo@local.test",
		UserStatus:       "active",
		CredentialStatus: "active",
		PasswordHash:     hash,
	}
	return &memoryCredentialStore{
		byEmail: map[string]PasswordIdentity{identity.NormalizedEmail: identity},
		byID:    map[string]PasswordIdentity{identity.UserID: identity},
	}
}

func (s *memoryCredentialStore) FindPasswordIdentityByEmail(_ context.Context, email string) (PasswordIdentity, error) {
	if s.err != nil {
		return PasswordIdentity{}, s.err
	}
	identity, ok := s.byEmail[email]
	if !ok {
		return PasswordIdentity{}, ErrCredentialNotFound
	}
	return identity, nil
}

func (s *memoryCredentialStore) FindPasswordIdentityByUserID(_ context.Context, userID string) (PasswordIdentity, error) {
	if s.err != nil {
		return PasswordIdentity{}, s.err
	}
	identity, ok := s.byID[userID]
	if !ok {
		return PasswordIdentity{}, ErrCredentialNotFound
	}
	return identity, nil
}

func newCredentialBackedService(t *testing.T, credentials CredentialStore) *Service {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab",
		Audience: "api-bff", AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		TOTPEncryptionKey:  "01234567890123456789012345678901",
		RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
	}, newMemoryStore(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestPasswordLoginLoadsNormalizedEmailFromCredentialStore(t *testing.T) {
	service := newCredentialBackedService(t, newMemoryCredentialStore(t))

	pair, err := service.Login(context.Background(), "  DEMO@LOCAL.TEST ", "local-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("expected token pair, got %+v", pair)
	}
}

func TestPasswordLoginRejectsInactiveUserWithoutLeakingReason(t *testing.T) {
	credentials := newMemoryCredentialStore(t)
	identity := credentials.byEmail["demo@local.test"]
	identity.UserStatus = "suspended"
	credentials.byEmail[identity.NormalizedEmail] = identity
	credentials.byID[identity.UserID] = identity
	service := newCredentialBackedService(t, credentials)

	_, err := service.Login(context.Background(), "demo@local.test", "local-demo-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected generic invalid credentials, got %v", err)
	}
}

func TestPasswordHashNeverContainsPlaintextPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("password hash must not equal plaintext")
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("password hash did not verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password verified")
	}
	oversized := strings.Replace(hash, "m=65536", "m=1048576", 1)
	if VerifyPassword(oversized, "correct horse battery staple") {
		t.Fatal("unapproved Argon2 parameters must be rejected")
	}
}

func TestPasswordLoginDoesNotHideCredentialStoreFailure(t *testing.T) {
	want := errors.New("database unavailable")
	service := newCredentialBackedService(t, &memoryCredentialStore{err: want})

	_, err := service.Login(context.Background(), "demo@local.test", "local-demo-password")
	if !errors.Is(err, want) {
		t.Fatalf("expected credential store failure, got %v", err)
	}
}
