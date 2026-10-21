package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
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

type Session struct {
	ID              string
	UserID          string
	UserVersion     int64
	AuthMethods     []string
	AuthenticatedAt int64
	Status          string
}

type SessionStore interface {
	PutSession(context.Context, Session, time.Duration) error
	GetSession(context.Context, string) (Session, error)
	RevokeSession(context.Context, string) error
	GetUserVersion(context.Context, string) (int64, error)
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
	JWTPrivateKey      ed25519.PrivateKey
	JWTKeyID           string
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
	config   Config
	store    RefreshStore
	sessions SessionStore
	factors  FactorStore
	now      func() time.Time
}

func NewService(config Config, store RefreshStore) (*Service, error) {
	if len(config.JWTPrivateKey) != ed25519.PrivateKeySize || config.JWTKeyID == "" {
		return nil, errors.New("valid Ed25519 private key and key id are required")
	}
	if config.AccessTTL <= 0 || config.RefreshTTL <= 0 || store == nil {
		return nil, errors.New("invalid auth configuration")
	}
	factors, ok := store.(FactorStore)
	if !ok || len(config.TOTPEncryptionKey) < 32 || len(config.RecoveryCodePepper) < 32 {
		return nil, errors.New("invalid second-factor configuration")
	}
	sessions, ok := store.(SessionStore)
	if !ok {
		return nil, errors.New("session store is required")
	}
	return &Service{config: config, store: store, sessions: sessions, factors: factors, now: time.Now}, nil
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
	record, err := s.store.Consume(ctx, tokenHash(refreshToken))
	if err != nil || record == "" {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	parts := strings.SplitN(record, "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	session, err := s.sessions.GetSession(ctx, parts[1])
	if err != nil || session.Status != "active" || session.UserID != parts[0] {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	version, err := s.sessions.GetUserVersion(ctx, session.UserID)
	if err != nil || version != session.UserVersion {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	return s.issueForSession(ctx, session)
}

func (s *Service) Logout(ctx context.Context, refreshToken string) (bool, error) {
	if refreshToken == "" {
		return false, nil
	}
	record, err := s.store.Consume(ctx, tokenHash(refreshToken))
	if errors.Is(err, ErrInvalidRefreshToken) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	parts := strings.SplitN(record, "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return false, ErrInvalidRefreshToken
	}
	if err := s.sessions.RevokeSession(ctx, parts[1]); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) issue(ctx context.Context, userID string, authMethods ...string) (TokenPair, error) {
	now := s.now()
	if len(authMethods) == 0 {
		authMethods = []string{"pwd"}
	}
	sid, err := randomToken()
	if err != nil {
		return TokenPair{}, err
	}
	version, err := s.sessions.GetUserVersion(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
	session := Session{ID: sid, UserID: userID, UserVersion: version, AuthMethods: authMethods, AuthenticatedAt: now.Unix(), Status: "active"}
	if err := s.sessions.PutSession(ctx, session, s.config.RefreshTTL); err != nil {
		return TokenPair{}, err
	}
	return s.issueForSession(ctx, session)
}

func (s *Service) issueForSession(ctx context.Context, session Session) (TokenPair, error) {
	now := s.now()
	expiresAt := now.Add(s.config.AccessTTL)
	jti, err := randomToken()
	if err != nil {
		return TokenPair{}, err
	}
	claims := jwt.MapClaims{
		"sub": session.UserID, "jti": jti, "sid": session.ID, "ver": session.UserVersion,
		"amr": session.AuthMethods, "auth_time": session.AuthenticatedAt,
		"iss": s.config.Issuer, "aud": s.config.Audience,
		"iat": now.Unix(), "exp": expiresAt.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.config.JWTKeyID
	accessToken, err := token.SignedString(s.config.JWTPrivateKey)
	if err != nil {
		return TokenPair{}, err
	}
	refreshToken, err := randomToken()
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.store.Put(ctx, tokenHash(refreshToken), session.UserID+"|"+session.ID, s.config.RefreshTTL); err != nil {
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
