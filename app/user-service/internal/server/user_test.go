package server

import (
	"context"
	"errors"
	"testing"
	"time"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/user-service/internal/registration"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type registrationUserLookup struct{ exists bool }

func (s registrationUserLookup) EmailExists(context.Context, string) (bool, error) {
	return s.exists, nil
}

type registrationChallengeMemory struct{}

func (registrationChallengeMemory) Save(context.Context, registration.RegistrationChallenge, time.Duration) error {
	return nil
}
func (registrationChallengeMemory) Delete(context.Context, string) error { return nil }

type registrationCodeRecorder struct{ calls int }

func (r *registrationCodeRecorder) Issue(context.Context, string, string, string) error {
	r.calls++
	return nil
}

func TestGetProfile(t *testing.T) {
	response, err := NewUserServer(nil, nil).GetProfile(context.Background(), &userv1.GetProfileRequest{UserId: "user-123"})
	if err != nil {
		t.Fatalf("GetProfile returned error: %v", err)
	}
	if response.GetUserId() != "user-123" || response.GetVerificationStatus() != "not_started" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestSendRegistrationCodeMapsUseCaseToGRPCContract(t *testing.T) {
	codes := &registrationCodeRecorder{}
	service := registration.NewCodeService(registration.CodeServiceConfig{
		GenerateToken: func() (string, error) { return "registration-token", nil },
	}, registrationUserLookup{}, registrationChallengeMemory{}, codes)
	response, err := NewUserServer(nil, nil, service).SendRegistrationCode(context.Background(), &userv1.SendRegistrationCodeRequest{
		Email: "reader@example.com", CodeType: registration.EmailCodeType, Locale: "zh-CN",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetVerifyToken() != "registration-token" || codes.calls != 1 {
		t.Fatalf("unexpected response=%+v code_calls=%d", response, codes.calls)
	}
}

func TestSendRegistrationCodeMapsRegisteredEmailToAlreadyExists(t *testing.T) {
	service := registration.NewCodeService(registration.CodeServiceConfig{
		GenerateToken: func() (string, error) { return "unused", nil },
	}, registrationUserLookup{exists: true}, registrationChallengeMemory{}, &registrationCodeRecorder{})
	_, err := NewUserServer(nil, nil, service).SendRegistrationCode(context.Background(), &userv1.SendRegistrationCodeRequest{
		Email: "registered@example.com", CodeType: registration.EmailCodeType,
	})
	if !errors.Is(err, status.Error(codes.AlreadyExists, registration.ErrEmailAlreadyRegistered.Error())) && status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}
}
