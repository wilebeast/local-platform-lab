CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY DEFAULT '',
    normalized_email TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_id_not_empty CHECK (id <> ''),
    CONSTRAINT users_email_not_empty CHECK (normalized_email <> ''),
    CONSTRAINT users_email_normalized CHECK (normalized_email = LOWER(BTRIM(normalized_email))),
    CONSTRAINT users_status_valid CHECK (status IN ('active', 'suspended', 'disabled')),
    CONSTRAINT users_version_positive CHECK (version > 0),
    CONSTRAINT users_normalized_email_unique UNIQUE (normalized_email)
);

CREATE TABLE IF NOT EXISTS user_password_credentials (
    user_id TEXT PRIMARY KEY DEFAULT '' REFERENCES users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    credential_version BIGINT NOT NULL DEFAULT 1,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_password_credentials_user_id_not_empty CHECK (user_id <> ''),
    CONSTRAINT user_password_credentials_hash_not_empty CHECK (password_hash <> ''),
    CONSTRAINT user_password_credentials_status_valid CHECK (status IN ('active', 'disabled')),
    CONSTRAINT user_password_credentials_version_positive CHECK (credential_version > 0)
);

CREATE INDEX IF NOT EXISTS users_status_idx ON users (status);
