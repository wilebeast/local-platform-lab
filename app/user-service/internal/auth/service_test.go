package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type memoryStore struct {
	mu                 sync.Mutex
	values             map[string]string
	totpSecrets        map[string]string
	totpSetups         map[string]string
	loginSessions      map[string]string
	recovery           map[string]map[string]struct{}
	sessions           map[string]Session
	versions           map[string]int64
	passkeyCeremonies  map[string]PasskeyCeremony
	passkeyCredentials map[string]PasskeyCredential
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		values: map[string]string{}, totpSecrets: map[string]string{}, totpSetups: map[string]string{},
		loginSessions: map[string]string{}, recovery: map[string]map[string]struct{}{}, sessions: map[string]Session{}, versions: map[string]int64{},
		passkeyCeremonies:  map[string]PasskeyCeremony{},
		passkeyCredentials: map[string]PasskeyCredential{},
	}
}
func (s *memoryStore) PutPasskeyCeremony(_ context.Context, ceremonyID string, ceremony PasskeyCeremony, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passkeyCeremonies[ceremonyID] = ceremony
	return nil
}
func (s *memoryStore) ConsumePasskeyCeremony(_ context.Context, ceremonyID string) (PasskeyCeremony, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ceremony, ok := s.passkeyCeremonies[ceremonyID]
	if !ok {
		return PasskeyCeremony{}, ErrInvalidPasskeyCeremony
	}
	delete(s.passkeyCeremonies, ceremonyID)
	return ceremony, nil
}
func (s *memoryStore) PutPasskeyCredential(_ context.Context, credential PasskeyCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passkeyCredentials[base64.RawURLEncoding.EncodeToString(credential.Credential.ID)] = credential
	return nil
}
func (s *memoryStore) GetPasskeyCredential(_ context.Context, credentialID []byte) (PasskeyCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.passkeyCredentials[base64.RawURLEncoding.EncodeToString(credentialID)]
	if !ok {
		return PasskeyCredential{}, ErrInvalidPasskeyCeremony
	}
	return credential, nil
}
func (s *memoryStore) Put(_ context.Context, key, value string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}
func (s *memoryStore) Consume(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return "", ErrInvalidRefreshToken
	}
	delete(s.values, key)
	return value, nil
}
func (s *memoryStore) Delete(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.values[key]
	delete(s.values, key)
	return ok, nil
}
func (s *memoryStore) PutSession(_ context.Context, session Session, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
	return nil
}
func (s *memoryStore) GetSession(_ context.Context, sid string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sid]
	if !ok {
		return Session{}, errors.New("session not found")
	}
	return session, nil
}
func (s *memoryStore) RevokeSession(_ context.Context, sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sid]
	if !ok {
		return errors.New("session not found")
	}
	session.Status = "revoked"
	s.sessions[sid] = session
	return nil
}
func (s *memoryStore) GetUserVersion(_ context.Context, userID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versions[userID] == 0 {
		s.versions[userID] = 1
	}
	return s.versions[userID], nil
}
func (s *memoryStore) PutTOTPSecret(_ context.Context, userID, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totpSecrets[userID] = secret
	return nil
}
func (s *memoryStore) GetTOTPSecret(_ context.Context, userID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totpSecrets[userID], nil
}
func (s *memoryStore) DeleteTOTP(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.totpSecrets, userID)
	delete(s.recovery, userID)
	return nil
}
func (s *memoryStore) PutTOTPSetup(_ context.Context, token, secret string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totpSetups[token] = secret
	return nil
}
func (s *memoryStore) ConsumeTOTPSetup(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.totpSetups[token]
	delete(s.totpSetups, token)
	return value, nil
}
func (s *memoryStore) PutLoginSession(_ context.Context, token, userID string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loginSessions[token] = userID
	return nil
}
func (s *memoryStore) ConsumeLoginSession(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.loginSessions[token]
	delete(s.loginSessions, token)
	return value, nil
}
func (s *memoryStore) ReplaceRecoveryDigests(_ context.Context, userID string, digests []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := map[string]struct{}{}
	for _, digest := range digests {
		values[digest] = struct{}{}
	}
	s.recovery[userID] = values
	return nil
}
func (s *memoryStore) ConsumeRecoveryDigest(_ context.Context, userID, digest string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.recovery[userID][digest]
	delete(s.recovery[userID], digest)
	return ok, nil
}

func testService(t *testing.T) *Service {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab",
		Audience: "api-bff", AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
		TOTPEncryptionKey:  "01234567890123456789012345678901",
		RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
	}, newMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLoginIssuesValidAccessToken(t *testing.T) {
	service := testService(t)
	pair, err := service.Login(context.Background(), "demo@local.test", "local-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	publicKey := service.config.JWTPrivateKey.Public().(ed25519.PublicKey)
	token, err := jwt.Parse(pair.AccessToken, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA || token.Header["kid"] != "test-key-1" {
			t.Fatalf("unexpected signing method %v", token.Header["alg"])
		}
		return publicKey, nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("invalid access token: %v", err)
	}
	if pair.ExpiresIn != 1800 || pair.RefreshToken == "" {
		t.Fatalf("unexpected token pair: %+v", pair)
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["sub"] != "local-user" || claims["sid"] == "" || claims["jti"] == "" || claims["ver"] != float64(1) {
		t.Fatalf("unexpected access token claims: %+v", claims)
	}
}

func TestRefreshKeepsSIDAndChangesJTI(t *testing.T) {
	service := testService(t)
	first, err := service.Login(context.Background(), "demo@local.test", "local-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	parse := func(raw string) jwt.MapClaims {
		token, parseErr := jwt.Parse(raw, func(*jwt.Token) (any, error) { return service.config.JWTPrivateKey.Public(), nil })
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return token.Claims.(jwt.MapClaims)
	}
	firstClaims, secondClaims := parse(first.AccessToken), parse(second.AccessToken)
	if firstClaims["sid"] != secondClaims["sid"] || firstClaims["jti"] == secondClaims["jti"] {
		t.Fatalf("refresh must keep sid and rotate jti: first=%+v second=%+v", firstClaims, secondClaims)
	}
}

func TestLogoutRevokesDeviceSession(t *testing.T) {
	store := newMemoryStore()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
		TOTPEncryptionKey: "01234567890123456789012345678901", RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := service.Login(context.Background(), "demo@local.test", "local-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := jwt.Parse(pair.AccessToken, func(*jwt.Token) (any, error) { return privateKey.Public(), nil })
	sid := token.Claims.(jwt.MapClaims)["sid"].(string)
	revoked, err := service.Logout(context.Background(), pair.RefreshToken)
	if err != nil || !revoked || store.sessions[sid].Status != "revoked" {
		t.Fatalf("logout did not revoke session: revoked=%v err=%v session=%+v", revoked, err, store.sessions[sid])
	}
}

func TestRefreshTokenIsSingleUse(t *testing.T) {
	service := testService(t)
	pair, _ := service.Login(context.Background(), "demo@local.test", "local-demo-password")
	if _, err := service.Refresh(context.Background(), pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("expected old token rejection, got %v", err)
	}
}

func TestRefreshRejectsOutdatedUserVersion(t *testing.T) {
	store := newMemoryStore()
	privateKey := ed25519.NewKeyFromSeed([]byte("01234567890123456789012345678901"))
	service, err := NewService(Config{
		JWTPrivateKey: privateKey, JWTKeyID: "test-key-1", Issuer: "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
		TOTPEncryptionKey: "01234567890123456789012345678901", RecoveryCodePepper: "abcdefghijklmnopqrstuvwxyz123456",
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := service.Login(context.Background(), "demo@local.test", "local-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	store.versions["local-user"] = 2
	if _, err := service.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("expected outdated session refresh rejection, got %v", err)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	service := testService(t)
	if _, err := service.Login(context.Background(), "demo@local.test", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}
