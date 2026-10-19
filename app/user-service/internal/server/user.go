package server

import (
	"context"
	"errors"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/user-service/internal/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UserServer struct {
	userv1.UnimplementedUserServiceServer
	auth *auth.Service
}

func NewUserServer(authService *auth.Service) *UserServer {
	return &UserServer{auth: authService}
}

func (s *UserServer) Login(ctx context.Context, request *userv1.LoginRequest) (*userv1.TokenPair, error) {
	pair, err := s.auth.Login(ctx, request.GetEmail(), request.GetPassword())
	if err != nil {
		return nil, authStatus(err, "login failed")
	}
	return toProto(pair), nil
}

func (s *UserServer) PrepareLogin(ctx context.Context, request *userv1.PrepareLoginRequest) (*userv1.PrepareLoginResponse, error) {
	result, err := s.auth.PrepareLogin(ctx, request.GetEmail(), request.GetPassword())
	if err != nil {
		return nil, authStatus(err, "prepare login failed")
	}
	response := &userv1.PrepareLoginResponse{SecondFactorRequired: result.SecondFactorRequired, AuthSessionToken: result.AuthSessionToken}
	if result.TokenPair.AccessToken != "" {
		response.TokenPair = toProto(result.TokenPair)
	}
	return response, nil
}

func (s *UserServer) VerifyLogin2FA(ctx context.Context, request *userv1.VerifyLogin2FARequest) (*userv1.TokenPair, error) {
	pair, err := s.auth.VerifyLogin2FA(ctx, request.GetAuthSessionToken(), request.GetCode())
	if err != nil {
		return nil, authStatus(err, "second-factor verification failed")
	}
	return toProto(pair), nil
}

func (s *UserServer) TOTPOptions(ctx context.Context, request *userv1.TOTPOptionsRequest) (*userv1.TOTPOptionsResponse, error) {
	setup, err := s.auth.TOTPOptions(ctx, request.GetUserId())
	if err != nil {
		return nil, authStatus(err, "create TOTP options failed")
	}
	return &userv1.TOTPOptionsResponse{SetupToken: setup.SetupToken, OtpauthUri: setup.OTPAuthURI}, nil
}

func (s *UserServer) ConfirmTOTP(ctx context.Context, request *userv1.ConfirmTOTPRequest) (*userv1.RecoveryCodesResponse, error) {
	codes, err := s.auth.ConfirmTOTP(ctx, request.GetUserId(), request.GetSetupToken(), request.GetCode())
	if err != nil {
		return nil, authStatus(err, "confirm TOTP failed")
	}
	return &userv1.RecoveryCodesResponse{RecoveryCodes: codes}, nil
}

func (s *UserServer) UnbindTOTP(ctx context.Context, request *userv1.UnbindTOTPRequest) (*userv1.UnbindTOTPResponse, error) {
	if err := s.auth.UnbindTOTP(ctx, request.GetUserId(), request.GetPassword(), request.GetCode()); err != nil {
		return nil, authStatus(err, "unbind TOTP failed")
	}
	return &userv1.UnbindTOTPResponse{Unbound: true}, nil
}

func (s *UserServer) RegenerateRecoveryCodes(ctx context.Context, request *userv1.RegenerateRecoveryCodesRequest) (*userv1.RecoveryCodesResponse, error) {
	codes, err := s.auth.RegenerateRecoveryCodes(ctx, request.GetUserId(), request.GetCode())
	if err != nil {
		return nil, authStatus(err, "regenerate recovery codes failed")
	}
	return &userv1.RecoveryCodesResponse{RecoveryCodes: codes}, nil
}

func authStatus(err error, internalMessage string) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidSecondFactor):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, auth.ErrTOTPAlreadyBound), errors.Is(err, auth.ErrTOTPNotBound):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, auth.ErrSecondFactorRequired):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, internalMessage)
	}
}

func (s *UserServer) Refresh(ctx context.Context, request *userv1.RefreshRequest) (*userv1.TokenPair, error) {
	pair, err := s.auth.Refresh(ctx, request.GetRefreshToken())
	if errors.Is(err, auth.ErrInvalidRefreshToken) {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "refresh failed")
	}
	return toProto(pair), nil
}

func (s *UserServer) Logout(ctx context.Context, request *userv1.LogoutRequest) (*userv1.LogoutResponse, error) {
	revoked, err := s.auth.Logout(ctx, request.GetRefreshToken())
	if err != nil {
		return nil, status.Error(codes.Internal, "logout failed")
	}
	return &userv1.LogoutResponse{Revoked: revoked}, nil
}

func toProto(pair auth.TokenPair) *userv1.TokenPair {
	return &userv1.TokenPair{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken,
		ExpiresIn: pair.ExpiresIn, TokenType: "Bearer",
	}
}

func (s *UserServer) GetProfile(_ context.Context, request *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
	userID := request.GetUserId()
	if userID == "" {
		userID = "local-user"
	}
	return &userv1.GetProfileResponse{
		UserId:             userID,
		VerificationStatus: "not_started",
	}, nil
}
