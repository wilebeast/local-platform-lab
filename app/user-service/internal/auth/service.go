package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
)

type RefreshStore interface {
	Put(context.Context, string, string, time.Duration) error
	Consume(context.Context, string) (string, error)
	Delete(context.Context, string) (bool, error)
}

type FactorStore interface {
	PutTOTPSecret(context.Context, string, string) error
	GetTOTPSecret(context.Context, string) (string, error)
	DeleteTOTP(context.Context, string) error
	PutTOTPSetup(context.Context, string, string, time.Duration) error
	ConsumeTOTPSetup(context.Context, string) (string, error)
	PutLoginSession(context.Context, string, string, time.Duration) error
	ConsumeLoginSession(context.Context, string) (string, error)
	ReplaceRecoveryDigests(context.Context, string, []string) error
	ConsumeRecoveryDigest(context.Context, string, string) (bool, error)
}

type Config struct {
	JWTSigningKey      string
	Issuer             string
	Audience           string
	AccessTTL          time.Duration
	RefreshTTL         time.Duration
	DemoEmail          string
	DemoPassword       string
	DemoUserID         string
	TOTPEncryptionKey  string
	RecoveryCodePepper string
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
}

type Service struct {
	config  Config
	store   RefreshStore
	factors FactorStore
	now     func() time.Time
}

func NewService(config Config, store RefreshStore) (*Service, error) {
	if len(config.JWTSigningKey) < 32 {
		return nil, errors.New("JWT signing key must be at least 32 characters")
	}
	if config.AccessTTL <= 0 || config.RefreshTTL <= 0 || store == nil {
		return nil, errors.New("invalid auth configuration")
	}
	factors, ok := store.(FactorStore)
	if !ok || len(config.TOTPEncryptionKey) < 32 || len(config.RecoveryCodePepper) < 32 {
		return nil, errors.New("invalid second-factor configuration")
	}
	return &Service{config: config, store: store, factors: factors, now: time.Now}, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (TokenPair, error) {
	preparation, err := s.PrepareLogin(ctx, email, password)
	if err != nil {
		return TokenPair{}, err
	}
	if preparation.SecondFactorRequired {
		return TokenPair{}, ErrSecondFactorRequired
	}
	return preparation.TokenPair, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	if refreshToken == "" {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	userID, err := s.store.Consume(ctx, tokenHash(refreshToken))
	if err != nil || userID == "" {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	return s.issue(ctx, userID)
}

func (s *Service) Logout(ctx context.Context, refreshToken string) (bool, error) {
	if refreshToken == "" {
		return false, nil
	}
	return s.store.Delete(ctx, tokenHash(refreshToken))
}

func (s *Service) issue(ctx context.Context, userID string) (TokenPair, error) {
	now := s.now()
	expiresAt := now.Add(s.config.AccessTTL)
	claims := jwt.MapClaims{
		"id": userID, "entity_id": userID, "account_type": "personal",
		"iss": s.config.Issuer, "aud": s.config.Audience,
		"iat": now.Unix(), "exp": expiresAt.Unix(),
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.config.JWTSigningKey))
	if err != nil {
		return TokenPair{}, err
	}
	refreshToken, err := randomToken()
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.store.Put(ctx, tokenHash(refreshToken), userID, s.config.RefreshTTL); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: accessToken, RefreshToken: refreshToken, ExpiresIn: int64(s.config.AccessTTL.Seconds())}, nil
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
