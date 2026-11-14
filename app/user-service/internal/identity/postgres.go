package identity

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	"github.com/wilebeast/local-platform-lab/app/user-service/internal/auth"
)

//go:embed migrations/001_identity.sql
var migrationSQL string

type PostgresRepository struct {
	db *sql.DB
}

type BootstrapUser struct {
	UserID   string
	Email    string
	Password string
}

func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migrationSQL)
	return err
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) EmailExists(ctx context.Context, normalizedEmail string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM users
			WHERE normalized_email = $1
		)`, auth.NormalizeEmail(normalizedEmail)).Scan(&exists)
	return exists, err
}

func (r *PostgresRepository) FindPasswordIdentityByEmail(ctx context.Context, email string) (auth.PasswordIdentity, error) {
	return r.find(ctx, `
		SELECT u.id, u.normalized_email, u.status, c.status, c.password_hash
		FROM users u
		JOIN user_password_credentials c ON c.user_id = u.id
		WHERE u.normalized_email = $1`, auth.NormalizeEmail(email))
}

func (r *PostgresRepository) FindPasswordIdentityByUserID(ctx context.Context, userID string) (auth.PasswordIdentity, error) {
	return r.find(ctx, `
		SELECT u.id, u.normalized_email, u.status, c.status, c.password_hash
		FROM users u
		JOIN user_password_credentials c ON c.user_id = u.id
		WHERE u.id = $1`, userID)
}

func (r *PostgresRepository) find(ctx context.Context, query, key string) (auth.PasswordIdentity, error) {
	var identity auth.PasswordIdentity
	err := r.db.QueryRowContext(ctx, query, key).Scan(
		&identity.UserID,
		&identity.NormalizedEmail,
		&identity.UserStatus,
		&identity.CredentialStatus,
		&identity.PasswordHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.PasswordIdentity{}, auth.ErrCredentialNotFound
	}
	return identity, err
}

// EnsureBootstrapUser creates the local lab identity once. It never updates an
// existing password, so changing an environment variable cannot silently
// rotate credentials during a restart.
func EnsureBootstrapUser(ctx context.Context, db *sql.DB, user BootstrapUser) error {
	user.Email = auth.NormalizeEmail(user.Email)
	if user.UserID == "" || user.Email == "" || user.Password == "" {
		return errors.New("bootstrap user id, email, and password are required")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, normalized_email, status)
		VALUES ($1, $2, 'active')
		ON CONFLICT (id) DO NOTHING`, user.UserID, user.Email); err != nil {
		return fmt.Errorf("insert bootstrap user: %w", err)
	}

	var storedEmail string
	if err := tx.QueryRowContext(ctx, `SELECT normalized_email FROM users WHERE id = $1 FOR UPDATE`, user.UserID).Scan(&storedEmail); err != nil {
		return fmt.Errorf("load bootstrap user: %w", err)
	}
	if storedEmail != user.Email {
		return fmt.Errorf("bootstrap user %q already belongs to another email", user.UserID)
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_password_credentials WHERE user_id = $1)`, user.UserID).Scan(&exists); err != nil {
		return fmt.Errorf("check bootstrap credential: %w", err)
	}
	if !exists {
		hash, err := auth.HashPassword(user.Password)
		if err != nil {
			return fmt.Errorf("hash bootstrap password: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_password_credentials (user_id, password_hash, status)
			VALUES ($1, $2, 'active')`, user.UserID, hash); err != nil {
			return fmt.Errorf("insert bootstrap credential: %w", err)
		}
	}

	return tx.Commit()
}
