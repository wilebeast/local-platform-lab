package rbac

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/001_rbac.sql
var migrationSQL string

type PostgresRepository struct {
	db *sql.DB
}

func OpenPostgres(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migrationSQL)
	return err
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) FindMemberAccess(ctx context.Context, userID, organizationID string) (MemberAccess, error) {
	var access MemberAccess
	err := r.db.QueryRowContext(ctx,
		`SELECT status FROM rbac_members WHERE user_id = $1 AND organization_id = $2`,
		userID, organizationID,
	).Scan(&access.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return access, nil
	}
	if err != nil {
		return access, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.status, COALESCE(rp.permission_key, '')
		FROM rbac_members m
		JOIN rbac_member_roles mr ON mr.member_id = m.id
		JOIN rbac_roles r ON r.id = mr.role_id AND r.organization_id = m.organization_id
		LEFT JOIN rbac_role_permissions rp ON rp.role_id = r.id
		WHERE m.user_id = $1 AND m.organization_id = $2
		ORDER BY r.id, rp.permission_key`, userID, organizationID)
	if err != nil {
		return access, err
	}
	defer rows.Close()

	roles := make(map[string]*RoleAccess)
	order := make([]string, 0)
	for rows.Next() {
		var roleID, roleStatus, permission string
		if err := rows.Scan(&roleID, &roleStatus, &permission); err != nil {
			return access, err
		}
		role, exists := roles[roleID]
		if !exists {
			role = &RoleAccess{Status: roleStatus}
			roles[roleID] = role
			order = append(order, roleID)
		}
		if permission != "" {
			role.Permissions = append(role.Permissions, permission)
		}
	}
	if err := rows.Err(); err != nil {
		return access, err
	}
	for _, roleID := range order {
		access.Roles = append(access.Roles, *roles[roleID])
	}
	return access, nil
}
