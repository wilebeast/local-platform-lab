package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

var (
	dummyHashOnce sync.Once
	dummyHash     string
)

var ErrCredentialNotFound = errors.New("credential not found")

const (
	argon2Memory      = 64 * 1024
	argon2Iterations  = 3
	argon2Parallelism = 2
	argon2SaltLength  = 16
	argon2KeyLength   = 32
)

type PasswordIdentity struct {
	UserID           string
	NormalizedEmail  string
	UserStatus       string
	CredentialStatus string
	PasswordHash     string
}

type CredentialStore interface {
	FindPasswordIdentityByEmail(context.Context, string) (PasswordIdentity, error)
	FindPasswordIdentityByUserID(context.Context, string) (PasswordIdentity, error)
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	salt := make([]byte, argon2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argon2Iterations, argon2Memory, argon2Parallelism, argon2KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2Memory,
		argon2Iterations,
		argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	var version int
	var memory, iterations uint32
	var parallelism uint8
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false
	}
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	if version != argon2.Version || memory != argon2Memory || iterations != argon2Iterations || parallelism != argon2Parallelism {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argon2SaltLength {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != argon2KeyLength {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func (s *Service) authenticatePassword(ctx context.Context, email, password string) (PasswordIdentity, error) {
	identity, err := s.credentials.FindPasswordIdentityByEmail(ctx, NormalizeEmail(email))
	if errors.Is(err, ErrCredentialNotFound) {
		VerifyPassword(passwordTimingHash(), password)
		return PasswordIdentity{}, ErrInvalidCredentials
	}
	if err != nil {
		return PasswordIdentity{}, err
	}
	passwordMatches := VerifyPassword(identity.PasswordHash, password)
	if identity.UserStatus != "active" || identity.CredentialStatus != "active" || !passwordMatches {
		return PasswordIdentity{}, ErrInvalidCredentials
	}
	return identity, nil
}

func (s *Service) activePasswordIdentity(ctx context.Context, userID string) (PasswordIdentity, error) {
	identity, err := s.credentials.FindPasswordIdentityByUserID(ctx, userID)
	if errors.Is(err, ErrCredentialNotFound) {
		return PasswordIdentity{}, ErrInvalidCredentials
	}
	if err != nil {
		return PasswordIdentity{}, err
	}
	if identity.UserStatus != "active" || identity.CredentialStatus != "active" {
		return PasswordIdentity{}, ErrInvalidCredentials
	}
	return identity, nil
}

func passwordTimingHash() string {
	dummyHashOnce.Do(func() {
		dummyHash, _ = HashPassword("not-a-real-user-password")
	})
	return dummyHash
}
