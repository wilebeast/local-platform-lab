package notification

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type memoryCodeStore struct {
	digest       []byte
	cooldownHeld bool
	blocked      bool
}

func TestCodeManagerRejectsInvalidRecipientAndPurpose(t *testing.T) {
	manager := NewCodeManager(&memoryCodeStore{}, &recordingCodeSender{}, []byte("unit-test-pepper"), nil)
	tests := []struct {
		name      string
		recipient string
		purpose   string
	}{
		{name: "invalid recipient", recipient: "not-an-email", purpose: "sign_in"},
		{name: "unsafe purpose", recipient: "reader@example.test", purpose: "sign-in:../../"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := manager.Issue(context.Background(), test.recipient, test.purpose, "en")
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("expected InvalidArgument, got %v", err)
			}
		})
	}
}

func (s *memoryCodeStore) IsBlocked(context.Context, string) (bool, error) {
	return s.blocked, nil
}

func (s *memoryCodeStore) AcquireCooldown(context.Context, string, time.Duration) (bool, error) {
	if s.cooldownHeld {
		return false, nil
	}
	s.cooldownHeld = true
	return true, nil
}

func (s *memoryCodeStore) ReleaseCooldown(context.Context, string) error {
	s.cooldownHeld = false
	return nil
}

func (s *memoryCodeStore) SaveCode(_ context.Context, _ string, digest []byte, _ time.Duration) error {
	s.digest = append([]byte(nil), digest...)
	return nil
}

func (s *memoryCodeStore) DeleteCode(context.Context, string) error {
	s.digest = nil
	return nil
}

func (s *memoryCodeStore) ConsumeCode(_ context.Context, _ string, digest []byte) (CodeMatch, error) {
	if len(s.digest) == 0 {
		return CodeMissing, nil
	}
	if string(s.digest) != string(digest) {
		return CodeMismatch, nil
	}
	s.digest = nil
	return CodeMatched, nil
}

func (s *memoryCodeStore) RegisterFailure(context.Context, string, int, time.Duration, time.Duration) (bool, error) {
	return false, nil
}

func (s *memoryCodeStore) ClearFailures(context.Context, string) error { return nil }

type recordingCodeSender struct {
	code  string
	calls int
}

func (s *recordingCodeSender) SendCode(_ context.Context, _, _, _, code string) error {
	s.calls++
	s.code = code
	return nil
}

func TestCodeManagerCooldownAndOneTimeVerification(t *testing.T) {
	store := &memoryCodeStore{}
	sender := &recordingCodeSender{}
	manager := NewCodeManager(store, sender, []byte("unit-test-pepper"), func() (string, error) {
		return "482913", nil
	})

	sent, expiresIn, err := manager.Issue(context.Background(), "reader@example.test", "sign_in", "en")
	if err != nil {
		t.Fatal(err)
	}
	if !sent || expiresIn != 600 || sender.calls != 1 || sender.code != "482913" {
		t.Fatalf("unexpected first issue: sent=%t expires=%d calls=%d code=%q", sent, expiresIn, sender.calls, sender.code)
	}
	if string(store.digest) == "482913" {
		t.Fatal("store received the plaintext code")
	}

	sent, _, err = manager.Issue(context.Background(), "reader@example.test", "sign_in", "en")
	if err != nil {
		t.Fatal(err)
	}
	if sent || sender.calls != 1 {
		t.Fatalf("cooldown must suppress duplicate send: sent=%t calls=%d", sent, sender.calls)
	}

	valid, err := manager.Verify(context.Background(), "reader@example.test", "sign_in", "482913")
	if err != nil || !valid {
		t.Fatalf("expected valid code, valid=%t err=%v", valid, err)
	}
	valid, err = manager.Verify(context.Background(), "reader@example.test", "sign_in", "482913")
	if err != nil || valid {
		t.Fatalf("consumed code must be invalid, valid=%t err=%v", valid, err)
	}
}
