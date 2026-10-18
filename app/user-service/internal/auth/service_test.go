package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type memoryStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryStore() *memoryStore { return &memoryStore{values: map[string]string{}} }
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

func testService(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(Config{
		JWTSigningKey: "01234567890123456789012345678901", Issuer: "local-platform-lab",
		Audience: "api-bff", AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail: "demo@local.test", DemoPassword: "local-demo-password", DemoUserID: "local-user",
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
	token, err := jwt.Parse(pair.AccessToken, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			t.Fatalf("unexpected signing method %v", token.Header["alg"])
		}
		return []byte("01234567890123456789012345678901"), nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("invalid access token: %v", err)
	}
	if pair.ExpiresIn != 1800 || pair.RefreshToken == "" {
		t.Fatalf("unexpected token pair: %+v", pair)
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

func TestLoginRejectsWrongPassword(t *testing.T) {
	service := testService(t)
	if _, err := service.Login(context.Background(), "demo@local.test", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}
