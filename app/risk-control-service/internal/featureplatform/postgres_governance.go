package featureplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresFeatureServingGovernanceRepository struct {
	pool   *pgxpool.Pool
	schema string
}

func NewPostgresFeatureServingGovernanceRepository(
	pool *pgxpool.Pool,
	schema string,
) *PostgresFeatureServingGovernanceRepository {
	return &PostgresFeatureServingGovernanceRepository{pool: pool, schema: schema}
}

func (r *PostgresFeatureServingGovernanceRepository) CreatePromotionRequest(
	ctx context.Context,
	request FeatureServingChangeRequest,
) (FeatureServingChangeRequest, error) {
	if err := r.ready(); err != nil {
		return FeatureServingChangeRequest{}, err
	}
	payload, err := json.Marshal(request.Promotion)
	if err != nil {
		return FeatureServingChangeRequest{}, fmt.Errorf("encode promotion request: %w", err)
	}
	contentFingerprint, err := fingerprint(struct {
		Promotion   PromotionRequest
		RequestedBy string
	}{request.Promotion, request.RequestedBy})
	if err != nil {
		return FeatureServingChangeRequest{}, err
	}
	table := pgx.Identifier{r.schema, "feature_serving_change_requests"}.Sanitize()
	_, err = r.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			request_id,change_type,feature_set_key,feature_set_version,request_payload,
			request_status,requested_by,content_fingerprint,created_at)
		VALUES($1,'promotion',$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT(request_id) DO NOTHING`, table),
		request.RequestID, request.Promotion.FeatureSetKey, request.Promotion.FeatureSetVersion,
		payload, request.Status, request.RequestedBy, contentFingerprint, request.CreatedAt)
	if err != nil {
		return FeatureServingChangeRequest{}, fmt.Errorf("create feature serving change request: %w", err)
	}
	stored, storedFingerprint, err := r.get(ctx, request.RequestID)
	if err != nil {
		return FeatureServingChangeRequest{}, err
	}
	if storedFingerprint != contentFingerprint {
		return FeatureServingChangeRequest{}, ConflictError{Message: "feature serving request ID belongs to different content"}
	}
	return stored, nil
}

func (r *PostgresFeatureServingGovernanceRepository) GetChangeRequest(
	ctx context.Context,
	requestID string,
) (FeatureServingChangeRequest, error) {
	if err := r.ready(); err != nil {
		return FeatureServingChangeRequest{}, err
	}
	request, _, err := r.get(ctx, requestID)
	return request, err
}

func (r *PostgresFeatureServingGovernanceRepository) CompleteReview(
	ctx context.Context,
	requestID string,
	review ChangeRequestReview,
) (FeatureServingChangeRequest, error) {
	if err := r.ready(); err != nil {
		return FeatureServingChangeRequest{}, err
	}
	if requestID == "" || review.ReviewedBy == "" || review.Reason == "" || review.ReviewedAt.IsZero() ||
		(review.Status != ChangeRequestApproved && review.Status != ChangeRequestRejected && review.Status != ChangeRequestBlocked) {
		return FeatureServingChangeRequest{}, InvalidRequestError{Message: "complete feature serving review is required"}
	}
	table := pgx.Identifier{r.schema, "feature_serving_change_requests"}.Sanitize()
	tag, err := r.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET request_status=$2,reviewed_by=$3,review_reason=$4,
			assessment_id=$5,reviewed_at=$6
		WHERE request_id=$1 AND request_status='pending'`, table),
		requestID, review.Status, review.ReviewedBy, review.Reason,
		review.AssessmentID, review.ReviewedAt)
	if err != nil {
		return FeatureServingChangeRequest{}, fmt.Errorf("complete feature serving review: %w", err)
	}
	stored, _, readErr := r.get(ctx, requestID)
	if readErr != nil {
		return FeatureServingChangeRequest{}, readErr
	}
	if tag.RowsAffected() == 0 && (stored.Status != review.Status || stored.ReviewedBy != review.ReviewedBy ||
		stored.ReviewReason != review.Reason || stored.AssessmentID != review.AssessmentID) {
		return FeatureServingChangeRequest{}, ConflictError{Message: "feature serving change request was already reviewed"}
	}
	return stored, nil
}

func (r *PostgresFeatureServingGovernanceRepository) get(
	ctx context.Context,
	requestID string,
) (FeatureServingChangeRequest, string, error) {
	if requestID == "" {
		return FeatureServingChangeRequest{}, "", InvalidRequestError{Message: "change request ID is required"}
	}
	table := pgx.Identifier{r.schema, "feature_serving_change_requests"}.Sanitize()
	var request FeatureServingChangeRequest
	var payload []byte
	var contentFingerprint string
	var reviewedAt *time.Time
	err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT request_id,request_payload,request_status,requested_by,reviewed_by,
			review_reason,assessment_id,content_fingerprint,created_at,reviewed_at
		FROM %s WHERE request_id=$1`, table), requestID).Scan(
		&request.RequestID, &payload, &request.Status, &request.RequestedBy,
		&request.ReviewedBy, &request.ReviewReason, &request.AssessmentID,
		&contentFingerprint, &request.CreatedAt, &reviewedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return FeatureServingChangeRequest{}, "", NotFoundError{Message: "feature serving change request not found"}
	}
	if err != nil {
		return FeatureServingChangeRequest{}, "", err
	}
	if err := json.Unmarshal(payload, &request.Promotion); err != nil {
		return FeatureServingChangeRequest{}, "", fmt.Errorf("decode promotion request: %w", err)
	}
	if reviewedAt != nil {
		request.ReviewedAt = reviewedAt.UTC()
	}
	return request, contentFingerprint, nil
}

func (r *PostgresFeatureServingGovernanceRepository) ready() error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("postgres pool is required")
	}
	if !schemaNamePattern.MatchString(r.schema) {
		return fmt.Errorf("invalid postgres schema name %q", r.schema)
	}
	return nil
}
