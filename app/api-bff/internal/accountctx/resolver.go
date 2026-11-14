package accountctx

import (
	"context"
	"errors"
	"time"

	shared "github.com/wilebeast/local-platform-lab/pkg/accountcontext"
)

var (
	ErrInvalidProjection    = errors.New("invalid account access projection")
	ErrProjectionNotFound   = errors.New("account access projection not found")
	ErrSelectionNotFound    = errors.New("session account selection not found")
	ErrAccountNotAccessible = errors.New("account is not accessible")
	ErrSelectionStale       = errors.New("session account selection is stale")
)

type Actor struct {
	UserID    string
	SessionID string
}

type Access struct {
	AccountID      string   `json:"accountId"`
	AccountUID     string   `json:"accountUid"`
	AccountType    string   `json:"accountType"`
	OrganizationID string   `json:"organizationId,omitempty"`
	MemberID       string   `json:"memberId,omitempty"`
	RoleIDs        []string `json:"roleIds,omitempty"`
	Relationship   string   `json:"relationship"`
}

type Projection struct {
	UserID            string   `json:"user_id"`
	PermissionVersion int64    `json:"permission_version"`
	Accesses          []Access `json:"accesses"`
}

type Selection struct {
	AccountUID        string `json:"account_uid"`
	PermissionVersion int64  `json:"permission_version"`
	SelectedAt        int64  `json:"selected_at"`
}

type Store interface {
	PutProjection(context.Context, Projection, time.Duration) error
	GetProjection(context.Context, string) (Projection, error)
	PutSelection(context.Context, string, Selection, time.Duration) error
	GetSelection(context.Context, string) (Selection, error)
}

type Resolver struct {
	store Store
	ttl   time.Duration
	now   func() time.Time
}

func NewResolver(store Store, ttl time.Duration) *Resolver {
	return &Resolver{store: store, ttl: ttl, now: time.Now}
}

func (r *Resolver) Prime(ctx context.Context, projection Projection) error {
	if r == nil || r.store == nil || r.ttl <= 0 || projection.UserID == "" || projection.PermissionVersion <= 0 || len(projection.Accesses) == 0 {
		return ErrInvalidProjection
	}
	for _, access := range projection.Accesses {
		if access.AccountID == "" || access.AccountUID == "" || access.AccountType == "" || access.Relationship == "" {
			return ErrInvalidProjection
		}
	}
	return r.store.PutProjection(ctx, projection, r.ttl)
}

func (r *Resolver) Select(ctx context.Context, actor Actor, accountUID string) (shared.Context, error) {
	projection, err := r.projection(ctx, actor)
	if err != nil {
		return shared.Context{}, err
	}
	access, ok := findAccess(projection, accountUID)
	if !ok {
		return shared.Context{}, ErrAccountNotAccessible
	}
	selection := Selection{AccountUID: accountUID, PermissionVersion: projection.PermissionVersion, SelectedAt: r.now().Unix()}
	if err := r.store.PutSelection(ctx, actor.SessionID, selection, r.ttl); err != nil {
		return shared.Context{}, err
	}
	return buildContext(actor, projection, selection, access), nil
}

func (r *Resolver) Resolve(ctx context.Context, actor Actor) (shared.Context, error) {
	projection, err := r.projection(ctx, actor)
	if err != nil {
		return shared.Context{}, err
	}
	selection, err := r.store.GetSelection(ctx, actor.SessionID)
	if err != nil {
		return shared.Context{}, err
	}
	if selection.AccountUID == "" {
		return shared.Context{}, ErrSelectionNotFound
	}
	if selection.PermissionVersion != projection.PermissionVersion {
		return shared.Context{}, ErrSelectionStale
	}
	access, ok := findAccess(projection, selection.AccountUID)
	if !ok {
		return shared.Context{}, ErrAccountNotAccessible
	}
	return buildContext(actor, projection, selection, access), nil
}

func (r *Resolver) projection(ctx context.Context, actor Actor) (Projection, error) {
	if r == nil || r.store == nil || actor.UserID == "" || actor.SessionID == "" {
		return Projection{}, ErrProjectionNotFound
	}
	projection, err := r.store.GetProjection(ctx, actor.UserID)
	if err != nil {
		return Projection{}, err
	}
	if projection.UserID != actor.UserID || projection.PermissionVersion <= 0 {
		return Projection{}, ErrProjectionNotFound
	}
	return projection, nil
}

func findAccess(projection Projection, accountUID string) (Access, bool) {
	for _, access := range projection.Accesses {
		if access.AccountUID == accountUID {
			return access, true
		}
	}
	return Access{}, false
}

func buildContext(actor Actor, projection Projection, selection Selection, access Access) shared.Context {
	return shared.Context{
		UserID: actor.UserID, SessionID: actor.SessionID,
		AccountID: access.AccountID, AccountUID: access.AccountUID, AccountType: access.AccountType,
		OrganizationID: access.OrganizationID, MemberID: access.MemberID, RoleIDs: append([]string(nil), access.RoleIDs...),
		PermissionVersion: projection.PermissionVersion, SelectedAt: selection.SelectedAt,
	}
}
