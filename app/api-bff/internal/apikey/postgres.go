package apikey

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migration.sql
var migrationSQL string

type PostgresValidator struct {
	db *sql.DB
}

func OpenPostgres(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migrationSQL)
	return err
}

func NewPostgresValidator(db *sql.DB) *PostgresValidator {
	return &PostgresValidator{db: db}
}

func (v *PostgresValidator) Lookup(ctx context.Context, keyID string) (Credential, error) {
	var credential Credential
	var publicKey []byte
	var permissionsJSON string
	err := v.db.QueryRowContext(ctx, `
		SELECT key_id, merchant_id, status, expires_at, public_key, permissions::text
		FROM developer_api_keys
		WHERE key_id = $1 AND algo = 'ed25519'`, keyID,
	).Scan(&credential.KeyID, &credential.MerchantID, &credential.Status, &credential.ExpiresAt, &publicKey, &permissionsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, nil
	}
	if err != nil {
		return Credential{}, err
	}
	credential.PublicKey = publicKey
	if err := json.Unmarshal([]byte(permissionsJSON), &credential.Permissions); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

func (v *PostgresValidator) Validate(ctx context.Context, request ValidationRequest) (ValidationResult, error) {
	var permissionsJSON, whitelistJSON, status string
	var expiresAt int64
	err := v.db.QueryRowContext(ctx, `
		SELECT permissions::text, ip_whitelist::text, status, expires_at
		FROM developer_api_keys
		WHERE merchant_id = $1 AND key_id = $2`, request.MerchantID, request.KeyID,
	).Scan(&permissionsJSON, &whitelistJSON, &status, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationResult{}, nil
	}
	if err != nil {
		return ValidationResult{}, err
	}
	if status != "active" || (expiresAt > 0 && expiresAt <= time.Now().Unix()) {
		return ValidationResult{}, nil
	}

	var permissions []string
	if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
		return ValidationResult{}, err
	}
	if !contains(permissions, request.RequiredPermission) {
		return ValidationResult{Forbidden: true}, nil
	}

	var whitelist []string
	if err := json.Unmarshal([]byte(whitelistJSON), &whitelist); err != nil {
		return ValidationResult{}, err
	}
	if len(whitelist) > 0 && !ipAllowed(request.ClientIP, whitelist) {
		return ValidationResult{Forbidden: true}, nil
	}
	return ValidationResult{Valid: true}, nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func ipAllowed(raw string, whitelist []string) bool {
	raw = strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	address, err := netip.ParseAddr(raw)
	if err != nil {
		return false
	}
	address = address.Unmap()
	for _, item := range whitelist {
		item = strings.TrimSpace(item)
		if prefix, err := netip.ParsePrefix(item); err == nil && prefix.Contains(address) {
			return true
		}
		if allowed, err := netip.ParseAddr(item); err == nil && allowed.Unmap() == address {
			return true
		}
	}
	return false
}
