package apikey

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"
)

const ed25519Protocol = "LOCAL-OPENAPI-V2"

type Credential struct {
	KeyID, MerchantID, Status string
	PublicKey                 ed25519.PublicKey
	Permissions               []string
	ExpiresAt                 int64
}

type CredentialStore interface {
	Lookup(context.Context, string) (Credential, error)
}

type NonceStore interface {
	Claim(context.Context, string, time.Duration) (bool, error)
}

type Ed25519Request struct {
	KeyID, Authorization, Method, URI, Timestamp, Nonce, Digest, ComputedDigest string
}

func (r Ed25519Request) SigningString() string {
	return strings.Join([]string{ed25519Protocol, r.KeyID, r.Method, r.URI, r.Timestamp, r.Nonce, r.Digest}, "\n") + "\n"
}

type AuthenticationResult struct {
	Valid      bool
	MerchantID string
}

type Ed25519Authenticator struct {
	credentials CredentialStore
	nonces      NonceStore
	now         func() time.Time
}

func NewEd25519Authenticator(credentials CredentialStore, nonces NonceStore) *Ed25519Authenticator {
	return &Ed25519Authenticator{credentials: credentials, nonces: nonces, now: time.Now}
}

func (a *Ed25519Authenticator) Authenticate(ctx context.Context, request Ed25519Request) (AuthenticationResult, error) {
	now := a.now()
	unix, err := parseCanonicalUnix(request.Timestamp)
	if err != nil {
		return AuthenticationResult{}, nil
	}
	timestamp := time.Unix(unix, 0)
	if delta := now.Sub(timestamp); delta > 5*time.Minute || delta < -5*time.Minute {
		return AuthenticationResult{}, nil
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(request.Nonce)
	if err != nil || len(nonce) != 16 {
		return AuthenticationResult{}, nil
	}
	if request.Method == "" || request.Method != strings.ToUpper(request.Method) || request.URI == "" {
		return AuthenticationResult{}, nil
	}
	if !validDigest(request.Digest) || request.Digest != request.ComputedDigest {
		return AuthenticationResult{}, nil
	}
	credential, err := a.credentials.Lookup(ctx, request.KeyID)
	if err != nil {
		return AuthenticationResult{}, err
	}
	if credential.KeyID == "" || credential.Status != "active" || len(credential.PublicKey) != ed25519.PublicKeySize || (credential.ExpiresAt > 0 && credential.ExpiresAt <= now.Unix()) {
		return AuthenticationResult{}, nil
	}
	signature, ok := parseAuthorization(request.Authorization)
	if !ok || !ed25519.Verify(credential.PublicKey, []byte(request.SigningString()), signature) {
		return AuthenticationResult{}, nil
	}
	claimed, err := a.nonces.Claim(ctx, "openapi:nonce:"+request.KeyID+":"+request.Nonce, 11*time.Minute)
	if err != nil {
		return AuthenticationResult{}, err
	}
	if !claimed {
		return AuthenticationResult{}, nil
	}
	return AuthenticationResult{Valid: true, MerchantID: credential.MerchantID}, nil
}

type Ed25519RequestAuthenticator interface {
	Authenticate(context.Context, Ed25519Request) (AuthenticationResult, error)
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "SHA-256=") {
		return false
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(value, "SHA-256="))
	return err == nil && len(raw) == sha256.Size
}

func parseAuthorization(value string) ([]byte, bool) {
	const prefix = "Ed25519 signature=\""
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "\"") {
		return nil, false
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSuffix(strings.TrimPrefix(value, prefix), "\""))
	return raw, err == nil && len(raw) == ed25519.SignatureSize
}
