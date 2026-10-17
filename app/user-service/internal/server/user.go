package server

import (
	"context"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
)

type UserServer struct {
	userv1.UnimplementedUserServiceServer
}

func NewUserServer() *UserServer {
	return &UserServer{}
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
