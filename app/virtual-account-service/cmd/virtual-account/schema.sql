CREATE TABLE IF NOT EXISTS va_openings (
  opening_id text PRIMARY KEY, idempotency_key text UNIQUE NOT NULL,
  owner_id text NOT NULL, currency text NOT NULL, status text NOT NULL,
  command_fingerprint text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS va_channel_operations (
  operation_id text PRIMARY KEY, opening_id text UNIQUE NOT NULL REFERENCES va_openings(opening_id),
  status text NOT NULL, attempt_count integer NOT NULL DEFAULT 0, last_error text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS va_accounts (
  account_id text PRIMARY KEY, opening_id text UNIQUE NOT NULL REFERENCES va_openings(opening_id),
  owner_id text NOT NULL, currency text NOT NULL, provider_resource_id text UNIQUE NOT NULL,
  status text NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS va_account_rails (
  rail_id bigserial PRIMARY KEY, account_id text NOT NULL REFERENCES va_accounts(account_id),
  rail_type text NOT NULL, routing_code text NOT NULL, masked_account text NOT NULL,
  UNIQUE(account_id, rail_type));
CREATE TABLE IF NOT EXISTS va_provider_event_inbox (
  event_id text PRIMARY KEY, provider_resource_id text NOT NULL, event_type text NOT NULL,
  occurred_at bigint NOT NULL, raw_body jsonb NOT NULL, processing_status text NOT NULL DEFAULT 'pending',
  attempt_count integer NOT NULL DEFAULT 0, last_error text NOT NULL DEFAULT '', received_at timestamptz NOT NULL DEFAULT now(), processed_at timestamptz);
CREATE TABLE IF NOT EXISTS va_payins (
  payin_id text PRIMARY KEY, event_id text UNIQUE NOT NULL REFERENCES va_provider_event_inbox(event_id),
  account_id text NOT NULL REFERENCES va_accounts(account_id), amount_minor bigint NOT NULL CHECK(amount_minor>0),
  currency text NOT NULL, status text NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS va_credit_entries (
  credit_id text PRIMARY KEY, payin_id text UNIQUE NOT NULL REFERENCES va_payins(payin_id),
  account_id text NOT NULL, amount_minor bigint NOT NULL, currency text NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS va_balances (
  account_id text PRIMARY KEY, currency text NOT NULL, credited_minor bigint NOT NULL DEFAULT 0,
  version bigint NOT NULL DEFAULT 0, updated_at timestamptz NOT NULL DEFAULT now());
