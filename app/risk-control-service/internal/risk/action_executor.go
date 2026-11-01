package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PolicyActionExecution struct {
	PolicyActionJob
	SubjectType string
	SubjectID   string
}

type PolicyActionEffect interface {
	ActionCode() string
	Execute(context.Context, PolicyActionExecution) error
}

type PolicyActionExecutor struct {
	pool        *pgxpool.Pool
	effects     map[string]PolicyActionEffect
	maxAttempts int
}

func NewPolicyActionExecutor(
	pool *pgxpool.Pool,
	effects []PolicyActionEffect,
	maxAttempts int,
) (*PolicyActionExecutor, error) {
	if pool == nil || maxAttempts <= 0 {
		return nil, fmt.Errorf("action executor requires pool and positive max attempts")
	}
	registered := make(map[string]PolicyActionEffect, len(effects))
	for _, effect := range effects {
		if effect == nil || effect.ActionCode() == "" {
			return nil, fmt.Errorf("action effect and code are required")
		}
		if _, exists := registered[effect.ActionCode()]; exists {
			return nil, fmt.Errorf("action effect %q is already registered", effect.ActionCode())
		}
		registered[effect.ActionCode()] = effect
	}
	return &PolicyActionExecutor{pool: pool, effects: registered, maxAttempts: maxAttempts}, nil
}

func (e *PolicyActionExecutor) RunOnce(
	ctx context.Context,
) (bool, PolicyActionJob, error) {
	execution, err := e.claimNext(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, PolicyActionJob{}, nil
	}
	if err != nil {
		return false, PolicyActionJob{}, err
	}
	effect, found := e.effects[execution.ActionCode]
	if !found {
		err = fmt.Errorf("action effect %q is not registered", execution.ActionCode)
	} else {
		err = effect.Execute(ctx, execution)
	}
	if err != nil {
		job, updateErr := e.recordFailure(ctx, execution.PolicyActionJob, err)
		if updateErr != nil {
			return true, job, fmt.Errorf("execute action: %v; record failure: %w", err, updateErr)
		}
		return true, job, err
	}
	job, err := e.markCompleted(ctx, execution.PolicyActionJob)
	return true, job, err
}

func (e *PolicyActionExecutor) claimNext(ctx context.Context) (PolicyActionExecution, error) {
	tx, err := e.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return PolicyActionExecution{}, err
	}
	defer tx.Rollback(ctx)
	var execution PolicyActionExecution
	var params []byte
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT action_id
			FROM risk_policy_action_jobs
			WHERE (status IN ('pending','retryable') AND next_attempt_at <= NOW())
			   OR (status='processing' AND claimed_at <= NOW() - INTERVAL '1 minute')
			ORDER BY COALESCE(claimed_at, next_attempt_at), created_at, action_id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE risk_policy_action_jobs AS job
		SET status='processing', attempt_count=attempt_count+1, claimed_at=NOW(), last_error=''
		FROM candidate
		WHERE job.action_id=candidate.action_id
		RETURNING job.action_id,job.evaluation_id,job.sequence_no,job.action_code,
			job.params,job.status,job.attempt_count,job.last_error`).Scan(
		&execution.ActionID, &execution.EvaluationID, &execution.SequenceNo,
		&execution.ActionCode, &params, &execution.Status,
		&execution.AttemptCount, &execution.LastError,
	)
	if err != nil {
		return PolicyActionExecution{}, err
	}
	if err := json.Unmarshal(params, &execution.Params); err != nil {
		return PolicyActionExecution{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT subject_type,subject_id
		FROM risk_realtime_evaluations
		WHERE evaluation_id=$1`, execution.EvaluationID).
		Scan(&execution.SubjectType, &execution.SubjectID); err != nil {
		return PolicyActionExecution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PolicyActionExecution{}, err
	}
	return execution, nil
}

func (e *PolicyActionExecutor) markCompleted(
	ctx context.Context,
	job PolicyActionJob,
) (PolicyActionJob, error) {
	err := e.pool.QueryRow(ctx, `
		UPDATE risk_policy_action_jobs
		SET status='completed',completed_at=NOW(),last_error=''
		WHERE action_id=$1 AND status='processing'
		RETURNING status,attempt_count,last_error`, job.ActionID).
		Scan(&job.Status, &job.AttemptCount, &job.LastError)
	return job, err
}

func (e *PolicyActionExecutor) recordFailure(
	ctx context.Context,
	job PolicyActionJob,
	cause error,
) (PolicyActionJob, error) {
	status := "retryable"
	if job.AttemptCount >= e.maxAttempts {
		status = "dead_letter"
	}
	nextAttempt := time.Now().UTC().Add(time.Duration(job.AttemptCount) * time.Second)
	err := e.pool.QueryRow(ctx, `
		UPDATE risk_policy_action_jobs
		SET status=$2,next_attempt_at=$3,last_error=$4
		WHERE action_id=$1 AND status='processing'
		RETURNING status,attempt_count,last_error`,
		job.ActionID, status, nextAttempt, cause.Error()).
		Scan(&job.Status, &job.AttemptCount, &job.LastError)
	return job, err
}

type ActivateRestrictionEffect struct{ pool *pgxpool.Pool }

func NewActivateRestrictionEffect(pool *pgxpool.Pool) *ActivateRestrictionEffect {
	return &ActivateRestrictionEffect{pool: pool}
}

func (*ActivateRestrictionEffect) ActionCode() string { return "activate_restriction" }

func (e *ActivateRestrictionEffect) Execute(
	ctx context.Context,
	action PolicyActionExecution,
) error {
	capability, ok := action.Params["capability"].(string)
	if !ok || capability == "" {
		return fmt.Errorf("activate_restriction requires capability")
	}
	tx, err := e.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	lockKey := action.SubjectType + "\n" + action.SubjectID + "\n" + capability
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return err
	}
	var existingID string
	err = tx.QueryRow(ctx, `
		SELECT restriction_id
		FROM risk_restrictions
		WHERE subject_type=$1 AND subject_id=$2 AND capability=$3 AND status='active'
		LIMIT 1
		FOR UPDATE`, action.SubjectType, action.SubjectID, capability).Scan(&existingID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		restrictionID := "restriction-" + action.ActionID
		if _, err := tx.Exec(ctx, `
			INSERT INTO risk_restrictions(
				restriction_id,finding_id,subject_type,subject_id,capability,status)
			VALUES($1,NULL,$2,$3,$4,'active')`,
			restrictionID, action.SubjectType, action.SubjectID, capability); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
