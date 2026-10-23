package notification

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var purposePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type CodeMatch int

const (
	CodeMissing CodeMatch = iota
	CodeMismatch
	CodeMatched
)

const (
	codeTTL       = 10 * time.Minute
	cooldownTTL   = 10 * time.Second
	failureWindow = 5 * time.Minute
	blockTTL      = 2 * time.Hour
	failureLimit  = 10
)

type CodeStore interface {
	IsBlocked(ctx context.Context, recipient string) (bool, error)
	AcquireCooldown(ctx context.Context, key string, ttl time.Duration) (bool, error)
	ReleaseCooldown(ctx context.Context, key string) error
	SaveCode(ctx context.Context, key string, digest []byte, ttl time.Duration) error
	DeleteCode(ctx context.Context, key string) error
	ConsumeCode(ctx context.Context, key string, digest []byte) (CodeMatch, error)
	RegisterFailure(ctx context.Context, recipient string, limit int, window, blockTTL time.Duration) (bool, error)
	ClearFailures(ctx context.Context, recipient string) error
}

type CodeSender interface {
	SendCode(ctx context.Context, recipient, purpose, locale, code string) error
}

type codeGenerator func() (string, error)

type EmailCodeManager struct {
	store    CodeStore
	sender   CodeSender
	pepper   []byte
	generate codeGenerator
}

func NewCodeManager(store CodeStore, sender CodeSender, pepper []byte, generate codeGenerator) *EmailCodeManager {
	if generate == nil {
		generate = secureSixDigitCode
	}
	return &EmailCodeManager{store: store, sender: sender, pepper: append([]byte(nil), pepper...), generate: generate}
}

func (m *EmailCodeManager) Issue(ctx context.Context, recipient, purpose, locale string) (bool, int64, error) {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	purpose = strings.TrimSpace(purpose)
	if recipient == "" || purpose == "" {
		return false, 0, status.Error(codes.InvalidArgument, "recipient and purpose are required")
	}
	if _, err := mail.ParseAddress(recipient); err != nil || !purposePattern.MatchString(purpose) {
		return false, 0, status.Error(codes.InvalidArgument, "recipient or purpose is invalid")
	}
	blocked, err := m.store.IsBlocked(ctx, recipient)
	if err != nil {
		return false, 0, status.Errorf(codes.Unavailable, "check verification block: %v", err)
	}
	if blocked {
		return false, 0, status.Error(codes.ResourceExhausted, "email verification is temporarily blocked")
	}
	key := codeKey(recipient, purpose)
	acquired, err := m.store.AcquireCooldown(ctx, key, cooldownTTL)
	if err != nil {
		return false, 0, status.Errorf(codes.Unavailable, "acquire send cooldown: %v", err)
	}
	if !acquired {
		return false, int64(codeTTL.Seconds()), nil
	}
	code, err := m.generate()
	if err != nil {
		_ = m.store.ReleaseCooldown(ctx, key)
		return false, 0, status.Errorf(codes.Internal, "generate verification code: %v", err)
	}
	digest := m.digest(recipient, purpose, code)
	if err := m.store.SaveCode(ctx, key, digest, codeTTL); err != nil {
		_ = m.store.ReleaseCooldown(ctx, key)
		return false, 0, status.Errorf(codes.Unavailable, "save verification code: %v", err)
	}
	if err := m.sender.SendCode(ctx, recipient, purpose, locale, code); err != nil {
		_ = m.store.DeleteCode(ctx, key)
		_ = m.store.ReleaseCooldown(ctx, key)
		return false, 0, status.Errorf(codes.Unavailable, "send verification code: %v", err)
	}
	return true, int64(codeTTL.Seconds()), nil
}

func (m *EmailCodeManager) Verify(ctx context.Context, recipient, purpose, code string) (bool, error) {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	purpose = strings.TrimSpace(purpose)
	code = strings.TrimSpace(code)
	if recipient == "" || purpose == "" || len(code) != 6 {
		return false, status.Error(codes.InvalidArgument, "recipient, purpose and six-digit code are required")
	}
	if _, err := mail.ParseAddress(recipient); err != nil || !purposePattern.MatchString(purpose) {
		return false, status.Error(codes.InvalidArgument, "recipient or purpose is invalid")
	}
	blocked, err := m.store.IsBlocked(ctx, recipient)
	if err != nil {
		return false, status.Errorf(codes.Unavailable, "check verification block: %v", err)
	}
	if blocked {
		return false, status.Error(codes.ResourceExhausted, "email verification is temporarily blocked")
	}
	result, err := m.store.ConsumeCode(ctx, codeKey(recipient, purpose), m.digest(recipient, purpose, code))
	if err != nil {
		return false, status.Errorf(codes.Unavailable, "verify email code: %v", err)
	}
	if result == CodeMatched {
		if err := m.store.ClearFailures(ctx, recipient); err != nil {
			return false, status.Errorf(codes.Unavailable, "clear verification failures: %v", err)
		}
		return true, nil
	}
	blocked, err = m.store.RegisterFailure(ctx, recipient, failureLimit, failureWindow, blockTTL)
	if err != nil {
		return false, status.Errorf(codes.Unavailable, "record verification failure: %v", err)
	}
	if blocked {
		return false, status.Error(codes.ResourceExhausted, "email verification is temporarily blocked")
	}
	return false, nil
}

func (m *EmailCodeManager) digest(recipient, purpose, code string) []byte {
	mac := hmac.New(sha256.New, m.pepper)
	_, _ = fmt.Fprintf(mac, "%s\x00%s\x00%s", recipient, purpose, code)
	return mac.Sum(nil)
}

func codeKey(recipient, purpose string) string {
	hash := sha256.Sum256([]byte(recipient))
	return fmt.Sprintf("%s:%x", purpose, hash[:12])
}

func secureSixDigitCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}
