package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RealtimeDecisionCommand struct {
	EvaluationID string         `json:"evaluation_id"`
	SubjectType  string         `json:"subject_type"`
	SubjectID    string         `json:"subject_id"`
	Params       map[string]any `json:"params"`
}

type PolicyActionJob struct {
	ActionID     string         `json:"action_id"`
	EvaluationID string         `json:"evaluation_id"`
	SequenceNo   int            `json:"sequence_no"`
	ActionCode   string         `json:"action_code"`
	Params       map[string]any `json:"params"`
	Status       string         `json:"status"`
	AttemptCount int            `json:"attempt_count"`
	LastError    string         `json:"last_error,omitempty"`
}

type DurableRealtimeDecision struct {
	EvaluationID string `json:"evaluation_id"`
	SubjectType  string `json:"subject_type"`
	SubjectID    string `json:"subject_id"`
	PolicyEvaluation
	ActionJobs  []PolicyActionJob `json:"action_jobs"`
	EvaluatedAt time.Time         `json:"evaluated_at"`
}

type RealtimeDecisionService struct {
	pool    *pgxpool.Pool
	runtime *PolicySnapshotRuntime
}

func NewRealtimeDecisionService(pool *pgxpool.Pool, runtime *PolicySnapshotRuntime) *RealtimeDecisionService {
	return &RealtimeDecisionService{pool: pool, runtime: runtime}
}

func (s *RealtimeDecisionService) Decide(
	ctx context.Context,
	command RealtimeDecisionCommand,
) (DurableRealtimeDecision, bool, error) {
	if command.EvaluationID == "" || command.SubjectType == "" || command.SubjectID == "" {
		return DurableRealtimeDecision{}, false, fmt.Errorf("evaluation_id, subject_type and subject_id are required")
	}
	if s.pool == nil || s.runtime == nil {
		return DurableRealtimeDecision{}, false, fmt.Errorf("realtime decision service is not configured")
	}
	fingerprint, err := realtimeDecisionFingerprint(command)
	if err != nil {
		return DurableRealtimeDecision{}, false, err
	}
	if existing, existingFingerprint, loadErr := loadDurableRealtimeDecision(ctx, s.pool, command.EvaluationID); loadErr == nil {
		if existingFingerprint != fingerprint {
			return DurableRealtimeDecision{}, false, ConflictError{"evaluation_id belongs to another request"}
		}
		return existing, true, nil
	} else if !errors.Is(loadErr, pgx.ErrNoRows) {
		return DurableRealtimeDecision{}, false, loadErr
	}

	evaluation, err := s.runtime.Evaluate(ctx, command.Params)
	if err != nil {
		return DurableRealtimeDecision{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DurableRealtimeDecision{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, command.EvaluationID); err != nil {
		return DurableRealtimeDecision{}, false, err
	}
	if existing, existingFingerprint, loadErr := loadDurableRealtimeDecision(ctx, tx, command.EvaluationID); loadErr == nil {
		if existingFingerprint != fingerprint {
			return DurableRealtimeDecision{}, false, ConflictError{"evaluation_id belongs to another request"}
		}
		if err := tx.Commit(ctx); err != nil {
			return DurableRealtimeDecision{}, false, err
		}
		return existing, true, nil
	} else if !errors.Is(loadErr, pgx.ErrNoRows) {
		return DurableRealtimeDecision{}, false, loadErr
	}

	reasonCodes, _ := json.Marshal(evaluation.ReasonCodes)
	factors, _ := json.Marshal(evaluation.Factors.Values())
	strategyResults, _ := json.Marshal(evaluation.StrategyResults)
	evaluatedAt := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		INSERT INTO risk_realtime_evaluations(
			evaluation_id,subject_type,subject_id,policy_key,policy_version,decision,
			reason_codes,matched_strategy_key,matched_rule_key,factor_snapshot,
			strategy_results,command_fingerprint,evaluated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		command.EvaluationID, command.SubjectType, command.SubjectID,
		evaluation.PolicyKey, evaluation.PolicyVersion, evaluation.Decision,
		reasonCodes, nullableText(evaluation.MatchedStrategyKey), nullableText(evaluation.MatchedRuleKey),
		factors, strategyResults, fingerprint, evaluatedAt)
	if err != nil {
		return DurableRealtimeDecision{}, false, err
	}
	actionJobs := make([]PolicyActionJob, 0, len(evaluation.Actions))
	for index, action := range evaluation.Actions {
		sequence := index + 1
		job := PolicyActionJob{
			ActionID:     stablePolicyActionID(command.EvaluationID, sequence, action.ActionCode),
			EvaluationID: command.EvaluationID, SequenceNo: sequence,
			ActionCode: action.ActionCode, Params: clonePolicyActionParams(action.Params), Status: "pending",
		}
		params, _ := json.Marshal(job.Params)
		if _, err := tx.Exec(ctx, `
			INSERT INTO risk_policy_action_jobs(
				action_id,evaluation_id,sequence_no,action_code,params,status)
			VALUES($1,$2,$3,$4,$5,'pending')`,
			job.ActionID, job.EvaluationID, job.SequenceNo, job.ActionCode, params); err != nil {
			return DurableRealtimeDecision{}, false, err
		}
		actionJobs = append(actionJobs, job)
	}
	if err := tx.Commit(ctx); err != nil {
		return DurableRealtimeDecision{}, false, err
	}
	return DurableRealtimeDecision{
		EvaluationID: command.EvaluationID, SubjectType: command.SubjectType, SubjectID: command.SubjectID,
		PolicyEvaluation: evaluation, ActionJobs: actionJobs, EvaluatedAt: evaluatedAt,
	}, false, nil
}

func realtimeDecisionFingerprint(command RealtimeDecisionCommand) (string, error) {
	encoded, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func stablePolicyActionID(evaluationID string, sequence int, actionCode string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%d\n%s", evaluationID, sequence, actionCode)))
	return "policy-action-" + hex.EncodeToString(sum[:16])
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func loadDurableRealtimeDecision(
	ctx context.Context,
	query rowQuerier,
	evaluationID string,
) (DurableRealtimeDecision, string, error) {
	var result DurableRealtimeDecision
	var reasonCodesJSON, factorSnapshotJSON, strategyResultsJSON []byte
	var matchedStrategy, matchedRule *string
	var fingerprint string
	err := query.QueryRow(ctx, `
		SELECT evaluation_id,subject_type,subject_id,policy_key,policy_version,decision,
			reason_codes,matched_strategy_key,matched_rule_key,factor_snapshot,
			strategy_results,command_fingerprint,evaluated_at
		FROM risk_realtime_evaluations
		WHERE evaluation_id=$1`, evaluationID).Scan(
		&result.EvaluationID, &result.SubjectType, &result.SubjectID,
		&result.PolicyKey, &result.PolicyVersion, &result.Decision,
		&reasonCodesJSON, &matchedStrategy, &matchedRule, &factorSnapshotJSON,
		&strategyResultsJSON, &fingerprint, &result.EvaluatedAt)
	if err != nil {
		return result, "", err
	}
	_ = json.Unmarshal(reasonCodesJSON, &result.ReasonCodes)
	if matchedStrategy != nil {
		result.MatchedStrategyKey = *matchedStrategy
	}
	if matchedRule != nil {
		result.MatchedRuleKey = *matchedRule
	}
	_ = json.Unmarshal(strategyResultsJSON, &result.StrategyResults)
	var values map[string]any
	_ = json.Unmarshal(factorSnapshotJSON, &values)
	results := make(map[string]FactorResult, len(values))
	for key, value := range values {
		results[key] = FactorResult{Key: key, Status: FactorStatusSuccess, Value: value}
	}
	result.Factors = FactorSnapshot{results: results}

	rows, err := query.Query(ctx, `
		SELECT action_id,evaluation_id,sequence_no,action_code,params,status,attempt_count,last_error
		FROM risk_policy_action_jobs
		WHERE evaluation_id=$1
		ORDER BY sequence_no`, evaluationID)
	if err != nil {
		return result, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var job PolicyActionJob
		var params []byte
		if err := rows.Scan(
			&job.ActionID, &job.EvaluationID, &job.SequenceNo, &job.ActionCode,
			&params, &job.Status, &job.AttemptCount, &job.LastError,
		); err != nil {
			return result, "", err
		}
		_ = json.Unmarshal(params, &job.Params)
		result.ActionJobs = append(result.ActionJobs, job)
		result.Actions = append(result.Actions, PolicyAction{ActionCode: job.ActionCode, Params: clonePolicyActionParams(job.Params)})
	}
	return result, fingerprint, rows.Err()
}
