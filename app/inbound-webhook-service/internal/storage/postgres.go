package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/receiver"
	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/worker"
)

type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (p *Postgres) Accept(ctx context.Context, event receiver.InboxEvent) (bool, error) {
	headers, err := json.Marshal(event.RawHeaders)
	if err != nil {
		return false, err
	}
	command, err := p.pool.Exec(ctx, `
		INSERT INTO provider_webhook_inbox (
			integration_code, domain, external_event_id, event_type, event_version,
			occurred_at, credential_key_id, raw_headers, raw_body, body_sha256
		) VALUES ($1, $2, $3, $4, $5, to_timestamp($6), $7, $8, $9, $10)
		ON CONFLICT (integration_code, external_event_id) DO NOTHING`,
		event.IntegrationCode, event.Domain, event.ExternalEventID, event.EventType, event.EventVersion,
		event.OccurredAt, event.CredentialKeyID, headers, event.RawBody, event.BodySHA256)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (p *Postgres) Claim(ctx context.Context, now time.Time) (*worker.Event, error) {
	row := p.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM provider_webhook_inbox
			WHERE (
				processing_status IN ('pending', 'retry') AND next_attempt_at <= $1
			) OR (
				processing_status = 'processing' AND locked_at < $1 - INTERVAL '1 minute'
			)
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE provider_webhook_inbox inbox
		SET processing_status = 'processing', attempt_count = attempt_count + 1, locked_at = $1
		FROM candidate
		WHERE inbox.id = candidate.id
		RETURNING inbox.id, inbox.integration_code, inbox.domain, inbox.external_event_id,
			inbox.event_type, inbox.event_version, extract(epoch from inbox.occurred_at)::bigint,
			inbox.raw_body, inbox.attempt_count`, now)
	var event worker.Event
	err := row.Scan(&event.ID, &event.IntegrationCode, &event.Domain, &event.ExternalEventID,
		&event.EventType, &event.EventVersion, &event.OccurredAt, &event.RawBody, &event.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &event, err
}

func (p *Postgres) MarkProcessed(ctx context.Context, id int64, processedAt time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE provider_webhook_inbox
		SET processing_status = 'processed', processed_at = $2, locked_at = NULL, last_error = NULL
		WHERE id = $1`, id, processedAt)
	return err
}

func (p *Postgres) MarkRetry(ctx context.Context, id int64, nextAttemptAt time.Time, reason string) error {
	_, err := p.pool.Exec(ctx, `UPDATE provider_webhook_inbox
		SET processing_status = 'retry', next_attempt_at = $2, locked_at = NULL, last_error = $3
		WHERE id = $1`, id, nextAttemptAt, truncate(reason, 1000))
	return err
}

func (p *Postgres) MarkDeadLetter(ctx context.Context, id int64, reason string) error {
	_, err := p.pool.Exec(ctx, `UPDATE provider_webhook_inbox
		SET processing_status = 'dead_letter', locked_at = NULL, last_error = $2
		WHERE id = $1`, id, truncate(reason, 1000))
	return err
}

type projectionEnvelope struct {
	Data projectionData `json:"data"`
}

type projectionData struct {
	ResourceID string `json:"resource_id"`
	Status     string `json:"status"`
}

func (p *Postgres) Apply(ctx context.Context, event worker.Event) error {
	if event.EventVersion != 1 {
		return fmt.Errorf("unsupported event version %d", event.EventVersion)
	}
	table, exists := projectionTable(event.Domain)
	if !exists {
		return fmt.Errorf("unsupported domain %q", event.Domain)
	}
	var envelope projectionEnvelope
	if err := json.Unmarshal(event.RawBody, &envelope); err != nil || envelope.Data.ResourceID == "" || envelope.Data.Status == "" {
		return errors.New("resource_id and status are required")
	}
	query := fmt.Sprintf(`INSERT INTO %s (
		resource_id, status, source_integration, source_event_id, source_occurred_at, payload
	) VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (resource_id) DO UPDATE SET
		status = EXCLUDED.status,
		source_integration = EXCLUDED.source_integration,
		source_event_id = EXCLUDED.source_event_id,
		source_occurred_at = EXCLUDED.source_occurred_at,
		payload = EXCLUDED.payload,
		updated_at = NOW()
	WHERE EXCLUDED.source_occurred_at >= %s.source_occurred_at`, table, table)
	_, err := p.pool.Exec(ctx, query, envelope.Data.ResourceID, envelope.Data.Status,
		event.IntegrationCode, event.ExternalEventID, event.OccurredAt, event.RawBody)
	return err
}

func projectionTable(domain string) (string, bool) {
	switch domain {
	case "payment":
		return "demo_payment_projection", true
	case "card":
		return "demo_card_projection", true
	case "verification":
		return "demo_verification_projection", true
	default:
		return "", false
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
