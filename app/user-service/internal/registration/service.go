package registration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

const (
	EmailCodeType       int64 = 3
	RegistrationPurpose       = "registration"
)

var (
	ErrInvalidRegistrationRequest = errors.New("invalid registration request")
	ErrEmailAlreadyRegistered     = errors.New("email already registered")
)

type UserLookup interface {
	EmailExists(ctx context.Context, normalizedEmail string) (bool, error)
}

type RegistrationChallengeStore interface {
	Save(ctx context.Context, challenge RegistrationChallenge, ttl time.Duration) error
	Delete(ctx context.Context, tokenDigest string) error
}

type EmailCodeIssuer interface {
	Issue(ctx context.Context, email, purpose, locale string) error
}

type RegistrationChallenge struct {
	TokenDigest string
	Email       string
	Purpose     string
}

type IssueCodeRequest struct {
	Email    string
	CodeType int64
	Locale   string
}

type IssueCodeResult struct {
	VerifyToken string
}

type CodeServiceConfig struct {
	ChallengeTTL  time.Duration
	DefaultLocale string
	GenerateToken func() (string, error)
}

type CodeService struct {
	config     CodeServiceConfig
	users      UserLookup
	challenges RegistrationChallengeStore
	codes      EmailCodeIssuer
}

func NewCodeService(config CodeServiceConfig, users UserLookup, challenges RegistrationChallengeStore, codes EmailCodeIssuer) *CodeService {
	if config.ChallengeTTL <= 0 {
		config.ChallengeTTL = 10 * time.Minute
	}
	if strings.TrimSpace(config.DefaultLocale) == "" {
		config.DefaultLocale = "en"
	}
	if config.GenerateToken == nil {
		config.GenerateToken = secureToken
	}
	return &CodeService{config: config, users: users, challenges: challenges, codes: codes}
}

func (s *CodeService) Issue(ctx context.Context, request IssueCodeRequest) (IssueCodeResult, error) {
	email, err := normalizeEmail(request.Email)
	if err != nil || request.CodeType != EmailCodeType {
		return IssueCodeResult{}, ErrInvalidRegistrationRequest
	}
	exists, err := s.users.EmailExists(ctx, email)
	if err != nil {
		return IssueCodeResult{}, fmt.Errorf("check registration email: %w", err)
	}
	if exists {
		return IssueCodeResult{}, ErrEmailAlreadyRegistered
	}

	token, err := s.config.GenerateToken()
	if err != nil {
		return IssueCodeResult{}, fmt.Errorf("generate registration token: %w", err)
	}
	digest := tokenDigest(token)
	challenge := RegistrationChallenge{TokenDigest: digest, Email: email, Purpose: RegistrationPurpose}
	if err := s.challenges.Save(ctx, challenge, s.config.ChallengeTTL); err != nil {
		return IssueCodeResult{}, fmt.Errorf("save registration challenge: %w", err)
	}

	locale := strings.TrimSpace(request.Locale)
	if locale == "" {
		locale = s.config.DefaultLocale
	}
	if err := s.codes.Issue(ctx, email, RegistrationPurpose, locale); err != nil {
		if deleteErr := s.challenges.Delete(ctx, digest); deleteErr != nil {
			return IssueCodeResult{}, fmt.Errorf("issue registration code: %v; delete challenge: %w", err, deleteErr)
		}
		return IssueCodeResult{}, fmt.Errorf("issue registration code: %w", err)
	}
	return IssueCodeResult{VerifyToken: token}, nil
}

func normalizeEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Address != normalized {
		return "", ErrInvalidRegistrationRequest
	}
	return normalized, nil
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func secureToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
