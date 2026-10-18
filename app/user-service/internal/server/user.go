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
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "login failed")
	}
	return toProto(pair), nil
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
