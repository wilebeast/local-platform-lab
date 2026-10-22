CREATE TABLE IF NOT EXISTS developer_api_keys (
    merchant_id TEXT NOT NULL,
    key_id TEXT NOT NULL,
    permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    ip_whitelist JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'active',
    expires_at BIGINT NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT EXTRACT(epoch FROM CURRENT_TIMESTAMP)::bigint,
    updated_at BIGINT NOT NULL DEFAULT EXTRACT(epoch FROM CURRENT_TIMESTAMP)::bigint,
    PRIMARY KEY (merchant_id, key_id)
);

ALTER TABLE developer_api_keys ADD COLUMN IF NOT EXISTS algo TEXT NOT NULL DEFAULT 'hmac-sha256';
ALTER TABLE developer_api_keys ADD COLUMN IF NOT EXISTS public_key BYTEA;
ALTER TABLE developer_api_keys ADD COLUMN IF NOT EXISTS public_key_fingerprint TEXT NOT NULL DEFAULT '';

INSERT INTO developer_api_keys (merchant_id, key_id, permissions, ip_whitelist, status)
VALUES ('local-org', 'local-key-1', '["profile.read"]'::jsonb, '[]'::jsonb, 'active')
ON CONFLICT (merchant_id, key_id) DO NOTHING;
