package rbac

import (
	"context"
	"errors"
)

var ErrInvalidCheck = errors.New("user, organization and permission are required")

type RoleAccess struct {
	Status      string
	Permissions []string
}

type MemberAccess struct {
	Status string
	Roles  []RoleAccess
}

type Repository interface {
	FindMemberAccess(ctx context.Context, userID, organizationID string) (MemberAccess, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) CheckPermission(ctx context.Context, userID, organizationID, permissionKey string) (bool, error) {
	if userID == "" || organizationID == "" || permissionKey == "" {
		return false, ErrInvalidCheck
	}
	access, err := s.repository.FindMemberAccess(ctx, userID, organizationID)
	if err != nil {
		return false, err
	}
	if access.Status != "active" {
		return false, nil
	}
	for _, role := range access.Roles {
		if role.Status != "active" {
			continue
		}
		for _, permission := range role.Permissions {
			if permission == permissionKey {
				return true, nil
			}
		}
	}
	return false, nil
}
