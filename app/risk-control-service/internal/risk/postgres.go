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

func commandFingerprint(cmd EvaluationCommand) string {
	b, _ := json.Marshal(cmd)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
