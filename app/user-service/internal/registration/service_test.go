package registration

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubUserLookup struct {
	exists bool
	email  string
}

func (s *stubUserLookup) EmailExists(_ context.Context, email string) (bool, error) {
	s.email = email
	return s.exists, nil
}

type recordingChallengeStore struct {
	saved   RegistrationChallenge
	deleted string
}

func (s *recordingChallengeStore) Save(_ context.Context, challenge RegistrationChallenge, _ time.Duration) error {
	s.saved = challenge
	return nil
}

func (s *recordingChallengeStore) Delete(_ context.Context, tokenDigest string) error {
	s.deleted = tokenDigest
	return nil
}

type recordingCodeIssuer struct {
	email   string
	purpose string
	locale  string
	calls   int
	err     error
}

func (i *recordingCodeIssuer) Issue(_ context.Context, email, purpose, locale string) error {
	i.calls++
	i.email, i.purpose, i.locale = email, purpose, locale
	return i.err
}

func TestIssueRegistrationCodeBindsNormalizedEmailToOpaqueToken(t *testing.T) {
	users := &stubUserLookup{}
	challenges := &recordingChallengeStore{}
	codes := &recordingCodeIssuer{}
	service := NewCodeService(CodeServiceConfig{
		ChallengeTTL:  10 * time.Minute,
		DefaultLocale: "zh-CN",
		GenerateToken: func() (string, error) { return "opaque-registration-token", nil },
	}, users, challenges, codes)

	result, err := service.Issue(context.Background(), IssueCodeRequest{
		Email: "  Reader@Example.COM ", CodeType: EmailCodeType,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.VerifyToken != "opaque-registration-token" {
		t.Fatalf("unexpected verify token %q", result.VerifyToken)
	}
	if users.email != "reader@example.com" {
		t.Fatalf("lookup used non-normalized email %q", users.email)
	}
	if codes.calls != 1 || codes.email != "reader@example.com" || codes.purpose != RegistrationPurpose || codes.locale != "zh-CN" {
		t.Fatalf("unexpected notification request: %+v", codes)
	}
	if challenges.saved.TokenDigest == "" || challenges.saved.TokenDigest == result.VerifyToken {
		t.Fatalf("challenge must store a digest, got %q", challenges.saved.TokenDigest)
	}
	if challenges.saved.Email != "reader@example.com" || challenges.saved.Purpose != RegistrationPurpose {
		t.Fatalf("unexpected saved challenge: %+v", challenges.saved)
	}
}

func TestIssueRegistrationCodeRejectsRegisteredEmailBeforeSending(t *testing.T) {
	users := &stubUserLookup{exists: true}
	challenges := &recordingChallengeStore{}
	codes := &recordingCodeIssuer{}
	service := NewCodeService(CodeServiceConfig{
		ChallengeTTL:  10 * time.Minute,
		GenerateToken: func() (string, error) { return "unused", nil },
	}, users, challenges, codes)

	_, err := service.Issue(context.Background(), IssueCodeRequest{
		Email: "registered@example.com", CodeType: EmailCodeType,
	})
	if !errors.Is(err, ErrEmailAlreadyRegistered) {
		t.Fatalf("expected registered-email conflict, got %v", err)
	}
	if codes.calls != 0 {
		t.Fatalf("registered email must not send a code, got %d calls", codes.calls)
	}
	if challenges.saved.TokenDigest != "" {
		t.Fatalf("registered email must not create a challenge: %+v", challenges.saved)
	}
}

func TestIssueRegistrationCodeRejectsUnsupportedCodeType(t *testing.T) {
	service := NewCodeService(CodeServiceConfig{
		ChallengeTTL:  10 * time.Minute,
		GenerateToken: func() (string, error) { return "unused", nil },
	}, &stubUserLookup{}, &recordingChallengeStore{}, &recordingCodeIssuer{})

	_, err := service.Issue(context.Background(), IssueCodeRequest{
		Email: "reader@example.com", CodeType: 6,
	})
	if !errors.Is(err, ErrInvalidRegistrationRequest) {
		t.Fatalf("expected invalid request, got %v", err)
	}
}

func TestIssueRegistrationCodeDeletesChallengeWhenDeliveryFails(t *testing.T) {
	challenges := &recordingChallengeStore{}
	codes := &recordingCodeIssuer{err: errors.New("mail unavailable")}
	service := NewCodeService(CodeServiceConfig{
		ChallengeTTL:  10 * time.Minute,
		GenerateToken: func() (string, error) { return "opaque-registration-token", nil },
	}, &stubUserLookup{}, challenges, codes)

	_, err := service.Issue(context.Background(), IssueCodeRequest{
		Email: "reader@example.com", CodeType: EmailCodeType,
	})
	if err == nil {
		t.Fatal("expected delivery failure")
	}
	if challenges.deleted == "" || challenges.deleted != challenges.saved.TokenDigest {
		t.Fatalf("failed delivery must delete saved challenge: saved=%q deleted=%q", challenges.saved.TokenDigest, challenges.deleted)
	}
}
