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

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) UpsertFacts(ctx context.Context, facts CustomerFacts) (CustomerFacts, error) {
	if facts.CustomerID == "" || facts.VerificationStatus == "" || facts.ResidenceCountry == "" {
		return CustomerFacts{}, fmt.Errorf("customer_id, verification_status and residence_country are required")
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO risk_customers(customer_id, verification_status, residence_country, poa_verified, questionnaire_completed)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(customer_id) DO UPDATE SET
			verification_status=EXCLUDED.verification_status,
			residence_country=EXCLUDED.residence_country,
			poa_verified=EXCLUDED.poa_verified,
			questionnaire_completed=EXCLUDED.questionnaire_completed,
			fact_version=risk_customers.fact_version+1,
			updated_at=NOW()
		RETURNING fact_version`, facts.CustomerID, facts.VerificationStatus, facts.ResidenceCountry, facts.POAVerified, facts.QuestionnaireCompleted).Scan(&facts.FactVersion)
	return facts, err
}

func (s *PostgresStore) Evaluate(ctx context.Context, cmd EvaluationCommand) (Evaluation, bool, error) {
	if cmd.EvaluationID == "" || cmd.CustomerID == "" || cmd.ProductCode == "" {
		return Evaluation{}, false, fmt.Errorf("evaluation_id, customer_id and product_code are required")
	}
	fingerprint := commandFingerprint(cmd)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Evaluation{}, false, err
	}
	defer tx.Rollback(ctx)
	// Serialize all attempts for the same business id. The unique primary key is
	// still the final guard; this lock lets a concurrent exact retry observe and
	// return the original result instead of surfacing a database constraint error.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, cmd.EvaluationID); err != nil {
		return Evaluation{}, false, err
	}

	if existing, existingFingerprint, err := loadEvaluation(ctx, tx, cmd.EvaluationID); err == nil {
		if existingFingerprint != fingerprint {
			return Evaluation{}, false, ConflictError{"evaluation_id belongs to another request"}
		}
		if err := tx.Commit(ctx); err != nil {
			return Evaluation{}, false, err
		}
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Evaluation{}, false, err
	}

	var facts CustomerFacts
	err = tx.QueryRow(ctx, `SELECT customer_id, verification_status, residence_country, poa_verified, questionnaire_completed, fact_version FROM risk_customers WHERE customer_id=$1 FOR SHARE`, cmd.CustomerID).
		Scan(&facts.CustomerID, &facts.VerificationStatus, &facts.ResidenceCountry, &facts.POAVerified, &facts.QuestionnaireCompleted, &facts.FactVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Evaluation{}, false, NotFoundError{"customer facts not found"}
	}
	if err != nil {
		return Evaluation{}, false, err
	}

	var policy Policy
	var config []byte
	err = tx.QueryRow(ctx, `SELECT policy_version, config FROM risk_policy_versions WHERE product_code=$1 AND status='published' ORDER BY policy_version DESC LIMIT 1`, cmd.ProductCode).Scan(&policy.Version, &config)
	if errors.Is(err, pgx.ErrNoRows) {
		return Evaluation{}, false, NotFoundError{"published policy not found"}
	}
	if err != nil {
		return Evaluation{}, false, err
	}
	policy.ProductCode = cmd.ProductCode
	if err := json.Unmarshal(config, &policy); err != nil {
		return Evaluation{}, false, err
	}
	decision, reasons, actionTypes := Evaluate(facts, policy)
	reasonsJSON, _ := json.Marshal(reasons)
	snapshotJSON, _ := json.Marshal(facts)
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO risk_evaluations(evaluation_id,customer_id,product_code,policy_version,fact_version,decision,reason_codes,fact_snapshot,command_fingerprint,evaluated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, cmd.EvaluationID, cmd.CustomerID, cmd.ProductCode, policy.Version, facts.FactVersion, decision, reasonsJSON, snapshotJSON, fingerprint, now)
	if err != nil {
		return Evaluation{}, false, err
	}
	actions := make([]Action, 0, len(actionTypes))
	for _, typ := range actionTypes {
		a := Action{ActionID: "action-" + cmd.EvaluationID + "-" + typ, EvaluationID: cmd.EvaluationID, CustomerID: cmd.CustomerID, ActionType: typ, Status: "open"}
		if _, err = tx.Exec(ctx, `INSERT INTO risk_actions(action_id,evaluation_id,customer_id,action_type,status) VALUES($1,$2,$3,$4,'open')`, a.ActionID, a.EvaluationID, a.CustomerID, a.ActionType); err != nil {
			return Evaluation{}, false, err
		}
		actions = append(actions, a)
	}
	if err = tx.Commit(ctx); err != nil {
		return Evaluation{}, false, err
	}
	return Evaluation{EvaluationID: cmd.EvaluationID, CustomerID: cmd.CustomerID, ProductCode: cmd.ProductCode, PolicyVersion: policy.Version, FactVersion: facts.FactVersion, Decision: decision, ReasonCodes: reasons, FactSnapshot: facts, Actions: actions, EvaluatedAt: now}, false, nil
}

func (s *PostgresStore) GetEvaluation(ctx context.Context, id string) (Evaluation, error) {
	e, _, err := loadEvaluation(ctx, s.pool, id)
	return e, err
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadEvaluation(ctx context.Context, q rowQuerier, id string) (Evaluation, string, error) {
	var e Evaluation
	var reasonsJSON, snapshotJSON []byte
	var fingerprint string
	err := q.QueryRow(ctx, `SELECT evaluation_id,customer_id,product_code,policy_version,fact_version,decision,reason_codes,fact_snapshot,command_fingerprint,evaluated_at FROM risk_evaluations WHERE evaluation_id=$1`, id).Scan(&e.EvaluationID, &e.CustomerID, &e.ProductCode, &e.PolicyVersion, &e.FactVersion, &e.Decision, &reasonsJSON, &snapshotJSON, &fingerprint, &e.EvaluatedAt)
	if err != nil {
		return e, "", err
	}
	_ = json.Unmarshal(reasonsJSON, &e.ReasonCodes)
	_ = json.Unmarshal(snapshotJSON, &e.FactSnapshot)
	rows, err := q.Query(ctx, `SELECT action_id,evaluation_id,customer_id,action_type,status,completed_at FROM risk_actions WHERE evaluation_id=$1 ORDER BY action_id`, id)
	if err != nil {
		return e, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var a Action
		if err := rows.Scan(&a.ActionID, &a.EvaluationID, &a.CustomerID, &a.ActionType, &a.Status, &a.CompletedAt); err != nil {
			return e, "", err
		}
		e.Actions = append(e.Actions, a)
	}
	return e, fingerprint, rows.Err()
}

func (s *PostgresStore) CompleteAction(ctx context.Context, id string, cmd CompleteActionCommand) (Action, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Action{}, err
	}
	defer tx.Rollback(ctx)
	var a Action
	err = tx.QueryRow(ctx, `SELECT action_id,evaluation_id,customer_id,action_type,status,completed_at FROM risk_actions WHERE action_id=$1 FOR UPDATE`, id).Scan(&a.ActionID, &a.EvaluationID, &a.CustomerID, &a.ActionType, &a.Status, &a.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, NotFoundError{"action not found"}
	}
	if err != nil {
		return Action{}, err
	}
	if a.Status == "completed" {
		if err := tx.Commit(ctx); err != nil {
			return Action{}, err
		}
		return a, nil
	}
	switch a.ActionType {
	case "provide_poa":
		if cmd.POAVerified == nil || !*cmd.POAVerified {
			return Action{}, fmt.Errorf("poa_verified=true is required")
		}
		_, err = tx.Exec(ctx, `UPDATE risk_customers SET poa_verified=true,fact_version=fact_version+1,updated_at=NOW() WHERE customer_id=$1`, a.CustomerID)
	case "complete_questionnaire":
		if cmd.QuestionnaireCompleted == nil || !*cmd.QuestionnaireCompleted {
			return Action{}, fmt.Errorf("questionnaire_completed=true is required")
		}
		_, err = tx.Exec(ctx, `UPDATE risk_customers SET questionnaire_completed=true,fact_version=fact_version+1,updated_at=NOW() WHERE customer_id=$1`, a.CustomerID)
	default:
		return Action{}, fmt.Errorf("action requires an external verification update")
	}
	if err != nil {
		return Action{}, err
	}
	now := time.Now().UTC()
	a.Status = "completed"
	a.CompletedAt = &now
	_, err = tx.Exec(ctx, `UPDATE risk_actions SET status='completed',completed_at=$2 WHERE action_id=$1`, id, now)
	if err != nil {
		return Action{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Action{}, err
	}
	return a, nil
}

func (s *PostgresStore) CheckRestriction(ctx context.Context, cmd RestrictionCheckCommand) (RestrictionCheck, error) {
	if cmd.SubjectType == "" || cmd.SubjectID == "" || cmd.Capability == "" {
		return RestrictionCheck{}, fmt.Errorf("subject_type, subject_id and capability are required")
	}
	check := RestrictionCheck{
		SubjectType: cmd.SubjectType, SubjectID: cmd.SubjectID, Capability: cmd.Capability,
		Decision: "allow", ReasonCodes: []string{"no_active_restriction"},
	}
	err := s.pool.QueryRow(ctx, `
		SELECT restriction_id, COALESCE(finding_id, '')
		FROM risk_restrictions
		WHERE subject_type=$1 AND subject_id=$2 AND capability=$3 AND status='active'
		ORDER BY activated_at DESC
		LIMIT 1`, cmd.SubjectType, cmd.SubjectID, cmd.Capability).
		Scan(&check.RestrictionID, &check.FindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return check, nil
	}
	if err != nil {
		return RestrictionCheck{}, err
	}
	check.Decision = "deny"
	check.ReasonCodes = []string{"offline_restriction_active"}
	return check, nil
}

func commandFingerprint(cmd EvaluationCommand) string {
	b, _ := json.Marshal(cmd)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *PostgresStore) LoadOfflinePolicy(ctx context.Context, key string, version int) (OfflinePolicy, error) {
	var policy OfflinePolicy
	var config []byte
	err := s.pool.QueryRow(ctx, `
		SELECT policy_key, policy_version, status, config
		FROM risk_offline_policy_versions
		WHERE policy_key=$1 AND policy_version=$2`, key, version).
		Scan(&policy.PolicyKey, &policy.Version, &policy.Status, &config)
	if errors.Is(err, pgx.ErrNoRows) {
		return OfflinePolicy{}, NotFoundError{"offline policy version not found"}
	}
	if err != nil {
		return OfflinePolicy{}, err
	}
	var stored struct {
		WindowHours  int     `json:"window_hours"`
		MinInflow    int64   `json:"min_inflow_minor"`
		OutflowRatio float64 `json:"outflow_ratio"`
		Capability   string  `json:"capability"`
	}
	if err := json.Unmarshal(config, &stored); err != nil {
		return OfflinePolicy{}, err
	}
	policy.Window = time.Duration(stored.WindowHours) * time.Hour
	policy.MinInflow = stored.MinInflow
	policy.OutflowRatio = stored.OutflowRatio
	policy.Capability = stored.Capability
	return policy, nil
}

func (s *PostgresStore) LoadActivityEvents(ctx context.Context, start, end time.Time) ([]ActivityEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id,subject_type,subject_id,event_type,amount_minor,currency,occurred_at
		FROM risk_activity_events
		WHERE occurred_at >= $1 AND occurred_at < $2
		ORDER BY occurred_at,event_id`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]ActivityEvent, 0)
	for rows.Next() {
		var event ActivityEvent
		if err := rows.Scan(&event.EventID, &event.SubjectType, &event.SubjectID, &event.EventType, &event.AmountMinor, &event.Currency, &event.OccurredAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresStore) SaveOfflineRun(ctx context.Context, result OfflineRunResult) (OfflineRunResult, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return OfflineRunResult{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, result.RunID); err != nil {
		return OfflineRunResult{}, false, err
	}
	var existingFingerprint string
	var existingJSON []byte
	err = tx.QueryRow(ctx, `SELECT command_fingerprint,result_json FROM risk_offline_runs WHERE run_id=$1`, result.RunID).Scan(&existingFingerprint, &existingJSON)
	if err == nil {
		if existingFingerprint != result.CommandFingerprint {
			return OfflineRunResult{}, false, ConflictError{"run_id belongs to another request"}
		}
		var existing OfflineRunResult
		if err := json.Unmarshal(existingJSON, &existing); err != nil {
			return OfflineRunResult{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return OfflineRunResult{}, false, err
		}
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OfflineRunResult{}, false, err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return OfflineRunResult{}, false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO risk_offline_runs(
			run_id,status,as_of,window_start,window_end,policy_key,policy_version,
			subjects_evaluated,findings_created,restrictions_activated,command_fingerprint,result_json,completed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NOW())`,
		result.RunID, result.Status, result.AsOf, result.WindowStart, result.WindowEnd, result.PolicyKey, result.PolicyVersion,
		result.SubjectsEvaluated, result.FindingsCreated, result.RestrictionsActivated, result.CommandFingerprint, resultJSON)
	if err != nil {
		return OfflineRunResult{}, false, err
	}
	actualRestrictionsActivated := 0
	for i := range result.Evaluations {
		evaluation := &result.Evaluations[i]
		featuresJSON, _ := json.Marshal(evaluation.Features)
		reasonsJSON, _ := json.Marshal(evaluation.ReasonCodes)
		_, err = tx.Exec(ctx, `
			INSERT INTO risk_feature_snapshots(run_id,subject_type,subject_id,currency,window_start,window_end,features)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, result.RunID, evaluation.SubjectType, evaluation.SubjectID,
			evaluation.Features.Currency, result.WindowStart, result.WindowEnd, featuresJSON)
		if err != nil {
			return OfflineRunResult{}, false, err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO risk_offline_evaluations(run_id,subject_type,subject_id,currency,policy_key,policy_version,decision,matched_strategy_key,matched_rule_key,reason_codes,features)
			VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11)`, result.RunID, evaluation.SubjectType, evaluation.SubjectID,
			evaluation.Features.Currency, result.PolicyKey, result.PolicyVersion, evaluation.Decision, evaluation.MatchedStrategyKey, evaluation.MatchedRuleKey, reasonsJSON, featuresJSON)
		if err != nil {
			return OfflineRunResult{}, false, err
		}
		if evaluation.Finding == nil || evaluation.Restriction == nil {
			continue
		}
		finding := evaluation.Finding
		_, err = tx.Exec(ctx, `
			INSERT INTO risk_findings(finding_id,run_id,subject_type,subject_id,risk_type,severity,policy_key,policy_version,window_start,window_end,status)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, finding.FindingID, result.RunID, finding.SubjectType,
			finding.SubjectID, finding.RiskType, finding.Severity, finding.PolicyKey, finding.PolicyVersion,
			finding.WindowStart, finding.WindowEnd, finding.Status)
		if err != nil {
			return OfflineRunResult{}, false, err
		}
		restriction := evaluation.Restriction
		restrictionKey := restriction.SubjectType + "\n" + restriction.SubjectID + "\n" + restriction.Capability
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, restrictionKey); err != nil {
			return OfflineRunResult{}, false, err
		}
		var existingID string
		var existingStatus string
		err = tx.QueryRow(ctx, `
			SELECT restriction_id,status
			FROM risk_restrictions
			WHERE subject_type=$1 AND subject_id=$2 AND capability=$3
			ORDER BY CASE WHEN status='active' THEN 0 ELSE 1 END, activated_at DESC
			LIMIT 1
			FOR UPDATE`, restriction.SubjectType, restriction.SubjectID, restriction.Capability).Scan(&existingID, &existingStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			actualRestrictionsActivated++
			_, err = tx.Exec(ctx, `
				INSERT INTO risk_restrictions(restriction_id,finding_id,subject_type,subject_id,capability,status)
				VALUES($1,$2,$3,$4,$5,$6)`, restriction.RestrictionID, restriction.FindingID, restriction.SubjectType,
				restriction.SubjectID, restriction.Capability, restriction.Status)
		} else if err != nil {
			return OfflineRunResult{}, false, err
		} else {
			restriction.RestrictionID = existingID
			if existingStatus == "lifted" {
				actualRestrictionsActivated++
			}
			_, err = tx.Exec(ctx, `
				UPDATE risk_restrictions
				SET finding_id=$2,
					status='active',
					activated_at=CASE WHEN status='lifted' THEN NOW() ELSE activated_at END,
					lifted_at=NULL
				WHERE restriction_id=$1`, existingID, restriction.FindingID)
		}
		if err != nil {
			return OfflineRunResult{}, false, err
		}
	}
	result.RestrictionsActivated = actualRestrictionsActivated
	resultJSON, err = json.Marshal(result)
	if err != nil {
		return OfflineRunResult{}, false, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE risk_offline_runs
		SET restrictions_activated=$2,result_json=$3
		WHERE run_id=$1`, result.RunID, result.RestrictionsActivated, resultJSON)
	if err != nil {
		return OfflineRunResult{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OfflineRunResult{}, false, err
	}
	return result, false, nil
}

func (s *PostgresStore) GetOfflineRun(ctx context.Context, id string) (OfflineRunResult, error) {
	var resultJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT result_json FROM risk_offline_runs WHERE run_id=$1`, id).Scan(&resultJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return OfflineRunResult{}, NotFoundError{"offline run not found"}
	}
	if err != nil {
		return OfflineRunResult{}, err
	}
	var result OfflineRunResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return OfflineRunResult{}, err
	}
	return result, nil
}
