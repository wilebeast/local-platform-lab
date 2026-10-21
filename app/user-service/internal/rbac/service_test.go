package rbac

import (
	"context"
	"testing"
)

type stubRepository struct {
	access MemberAccess
	err    error
}

func (s stubRepository) FindMemberAccess(context.Context, string, string) (MemberAccess, error) {
	return s.access, s.err
}

func TestServiceCheckPermission(t *testing.T) {
	tests := []struct {
		name       string
		access     MemberAccess
		permission string
		want       bool
	}{
		{
			name: "active member inherits permission from active role",
			access: MemberAccess{Status: "active", Roles: []RoleAccess{{
				Status: "active", Permissions: []string{"profile.read"},
			}}},
			permission: "profile.read",
			want:       true,
		},
		{
			name: "missing permission is denied",
			access: MemberAccess{Status: "active", Roles: []RoleAccess{{
				Status: "active", Permissions: []string{"profile.read"},
			}}},
			permission: "profile.write",
			want:       false,
		},
		{
			name: "inactive member is denied",
			access: MemberAccess{Status: "disabled", Roles: []RoleAccess{{
				Status: "active", Permissions: []string{"profile.read"},
			}}},
			permission: "profile.read",
			want:       false,
		},
		{
			name: "permission from inactive role is denied",
			access: MemberAccess{Status: "active", Roles: []RoleAccess{{
				Status: "disabled", Permissions: []string{"profile.read"},
			}}},
			permission: "profile.read",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(stubRepository{access: tt.access})
			got, err := service.CheckPermission(context.Background(), "local-user", "local-org", tt.permission)
			if err != nil {
				t.Fatalf("CheckPermission returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("CheckPermission() = %v, want %v", got, tt.want)
			}
		})
	}
}
