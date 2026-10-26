package acquiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrInvalidReconciliation = errors.New("invalid reconciliation request")

type ReconcilePayoutsCommand struct {
	IdempotencyKey    string   `json:"-"`
	Provider          string   `json:"provider"`
	StatementID       string   `json:"statement_id"`
	PeriodStart       int64    `json:"period_start"`
	PeriodEnd         int64    `json:"period_end"`
	ProviderPayoutIDs []string `json:"provider_payout_ids"`
}

type ReconcilePayoutsResult struct {
	RunID        string `json:"run_id"`
	Provider     string `json:"provider"`
	StatementID  string `json:"statement_id"`
	Status       string `json:"status"`
	MatchedCount int    `json:"matched_count"`
	IssueCount   int    `json:"issue_count"`
	Replayed     bool   `json:"replayed"`
}

type PayoutReconciliationStore interface {
	ReconcilePayouts(context.Context, ReconcilePayoutsCommand) (ReconcilePayoutsResult, error)
}

func (s *PostgresPaymentStore) ReconcilePayouts(ctx context.Context, command ReconcilePayoutsCommand) (ReconcilePayoutsResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.Provider = strings.TrimSpace(command.Provider)
	command.StatementID = strings.TrimSpace(command.StatementID)
	if command.IdempotencyKey == "" || command.StatementID == "" || (command.Provider != "demo-card" && command.Provider != "demo-bank") || command.PeriodStart <= 0 || command.PeriodEnd <= command.PeriodStart {
		return ReconcilePayoutsResult{}, ErrInvalidReconciliation
	}
	ids := append([]string(nil), command.ProviderPayoutIDs...)
	for i := range ids {
		ids[i] = strings.TrimSpace(ids[i])
		if ids[i] == "" {
			return ReconcilePayoutsResult{}, ErrInvalidReconciliation
		}
	}
	sort.Strings(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return ReconcilePayoutsResult{}, ErrInvalidReconciliation
		}
	}
	command.ProviderPayoutIDs = ids
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReconcilePayoutsResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, command.Provider+":"+command.IdempotencyKey); err != nil {
		return ReconcilePayoutsResult{}, err
	}
	fingerprint := reconciliationFingerprint(command)
	existing, storedFingerprint, err := loadReconciliationByIdempotency(ctx, tx, command.Provider, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return ReconcilePayoutsResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return ReconcilePayoutsResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReconcilePayoutsResult{}, err
	}

	runID := stablePaymentID("reconciliation", command.Provider+":"+command.StatementID)
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_reconciliation_runs(run_id,provider,statement_id,idempotency_key,command_fingerprint,period_start,period_end,status)
		VALUES($1,$2,$3,$4,$5,$6,$7,'completed')`, runID, command.Provider, command.StatementID, command.IdempotencyKey, fingerprint, command.PeriodStart, command.PeriodEnd)
	if isUniqueViolation(err) {
		return ReconcilePayoutsResult{}, ErrPaymentIdempotencyConflict
	}
	if err != nil {
		return ReconcilePayoutsResult{}, err
	}
	providerSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		providerSet[id] = struct{}{}
		if _, err := tx.Exec(ctx, `INSERT INTO acquiring_reconciliation_lines(run_id,provider_payout_id) VALUES($1,$2)`, runID, id); err != nil {
			return ReconcilePayoutsResult{}, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT p.payout_id,p.currency,p.amount_minor
		FROM acquiring_merchant_payouts p
		JOIN acquiring_payment_attempts a ON a.payment_id=p.payment_id
		WHERE a.provider=$1 AND p.status='paid' AND p.created_at >= to_timestamp($2) AND p.created_at < to_timestamp($3)
		ORDER BY p.payout_id`, command.Provider, command.PeriodStart, command.PeriodEnd)
	if err != nil {
		return ReconcilePayoutsResult{}, err
	}
	type expectedPayout struct {
		id, currency string
		amount       int64
	}
	var expected []expectedPayout
	for rows.Next() {
		var payout expectedPayout
		if err := rows.Scan(&payout.id, &payout.currency, &payout.amount); err != nil {
			return ReconcilePayoutsResult{}, err
		}
		expected = append(expected, payout)
	}
	if err := rows.Err(); err != nil {
		return ReconcilePayoutsResult{}, err
	}
	rows.Close()
	matched, issues := 0, 0
	for _, payout := range expected {
		if _, ok := providerSet[payout.id]; ok {
			matched++
			delete(providerSet, payout.id)
			continue
		}
		issueID := stablePaymentID("reconciliation-issue", runID+":missing_provider_payout:"+payout.id)
		if _, err := tx.Exec(ctx, `INSERT INTO acquiring_reconciliation_issues(issue_id,run_id,issue_type,business_type,business_id,currency,expected_amount_minor,status)
			VALUES($1,$2,'missing_provider_payout','merchant_payout',$3,$4,$5,'open')`, issueID, runID, payout.id, payout.currency, payout.amount); err != nil {
			return ReconcilePayoutsResult{}, err
		}
		issues++
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_reconciliation_runs SET matched_count=$1,issue_count=$2 WHERE run_id=$3`, matched, issues, runID); err != nil {
		return ReconcilePayoutsResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReconcilePayoutsResult{}, err
	}
	return ReconcilePayoutsResult{RunID: runID, Provider: command.Provider, StatementID: command.StatementID, Status: "completed", MatchedCount: matched, IssueCount: issues}, nil
}

func loadReconciliationByIdempotency(ctx context.Context, tx pgx.Tx, provider, key string) (ReconcilePayoutsResult, string, error) {
	var result ReconcilePayoutsResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT run_id,provider,statement_id,status,matched_count,issue_count,command_fingerprint
		FROM acquiring_reconciliation_runs WHERE provider=$1 AND idempotency_key=$2`, provider, key).
		Scan(&result.RunID, &result.Provider, &result.StatementID, &result.Status, &result.MatchedCount, &result.IssueCount, &fingerprint)
	return result, fingerprint, err
}

func reconciliationFingerprint(command ReconcilePayoutsCommand) string {
	canonical := fmt.Sprintf("%s\n%s\n%d\n%d\n%s", command.Provider, command.StatementID, command.PeriodStart, command.PeriodEnd, strings.Join(command.ProviderPayoutIDs, ","))
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func NewPayoutReconciliationHandler(store PayoutReconciliationStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command ReconcilePayoutsCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		result, err := store.ReconcilePayouts(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidReconciliation):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "reconcile payouts"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}
