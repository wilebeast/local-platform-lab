package acquiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidReconciliationResolution = errors.New("invalid reconciliation resolution")
	ErrReconciliationIssueNotOpen      = errors.New("reconciliation issue is not open")
)

type ResolveReconciliationIssueCommand struct {
	IdempotencyKey    string `json:"-"`
	IssueID           string `json:"issue_id"`
	ResolutionType    string `json:"resolution_type"`
	EvidenceReference string `json:"evidence_reference"`
}

type ResolveReconciliationIssueResult struct {
	ResolutionID      string `json:"resolution_id"`
	IssueID           string `json:"issue_id"`
	Status            string `json:"status"`
	ResolutionType    string `json:"resolution_type"`
	EvidenceReference string `json:"evidence_reference"`
	Replayed          bool   `json:"replayed"`
}

type ReconciliationIssueResolutionStore interface {
	ResolveReconciliationIssue(context.Context, ResolveReconciliationIssueCommand) (ResolveReconciliationIssueResult, error)
}

func (s *PostgresPaymentStore) ResolveReconciliationIssue(ctx context.Context, command ResolveReconciliationIssueCommand) (ResolveReconciliationIssueResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.IssueID = strings.TrimSpace(command.IssueID)
	command.ResolutionType = strings.TrimSpace(command.ResolutionType)
	command.EvidenceReference = strings.TrimSpace(command.EvidenceReference)
	if command.IdempotencyKey == "" || command.IssueID == "" || command.ResolutionType != "provider_confirmed_late" || command.EvidenceReference == "" {
		return ResolveReconciliationIssueResult{}, ErrInvalidReconciliationResolution
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResolveReconciliationIssueResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "reconciliation-resolution:"+command.IdempotencyKey); err != nil {
		return ResolveReconciliationIssueResult{}, err
	}
	fingerprint := reconciliationResolutionFingerprint(command)
	existing, storedFingerprint, err := loadReconciliationResolutionByIdempotency(ctx, tx, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return ResolveReconciliationIssueResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return ResolveReconciliationIssueResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ResolveReconciliationIssueResult{}, err
	}

	var issueStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM acquiring_reconciliation_issues WHERE issue_id=$1 FOR UPDATE`, command.IssueID).Scan(&issueStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResolveReconciliationIssueResult{}, ErrInvalidReconciliationResolution
		}
		return ResolveReconciliationIssueResult{}, err
	}
	if issueStatus != "open" {
		return ResolveReconciliationIssueResult{}, ErrReconciliationIssueNotOpen
	}

	resolutionID := stablePaymentID("reconciliation-resolution", command.IssueID+":"+command.ResolutionType+":"+command.EvidenceReference)
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_reconciliation_resolutions(
		resolution_id,issue_id,idempotency_key,command_fingerprint,resolution_type,evidence_reference)
		VALUES($1,$2,$3,$4,$5,$6)`, resolutionID, command.IssueID, command.IdempotencyKey, fingerprint, command.ResolutionType, command.EvidenceReference)
	if isUniqueViolation(err) {
		return ResolveReconciliationIssueResult{}, ErrReconciliationIssueNotOpen
	}
	if err != nil {
		return ResolveReconciliationIssueResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_reconciliation_issues SET status='resolved',resolved_at=NOW() WHERE issue_id=$1`, command.IssueID); err != nil {
		return ResolveReconciliationIssueResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ResolveReconciliationIssueResult{}, err
	}
	return ResolveReconciliationIssueResult{
		ResolutionID: resolutionID, IssueID: command.IssueID, Status: "resolved",
		ResolutionType: command.ResolutionType, EvidenceReference: command.EvidenceReference,
	}, nil
}

func loadReconciliationResolutionByIdempotency(ctx context.Context, tx pgx.Tx, key string) (ResolveReconciliationIssueResult, string, error) {
	var result ResolveReconciliationIssueResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT r.resolution_id,r.issue_id,i.status,r.resolution_type,r.evidence_reference,r.command_fingerprint
		FROM acquiring_reconciliation_resolutions r
		JOIN acquiring_reconciliation_issues i ON i.issue_id=r.issue_id
		WHERE r.idempotency_key=$1`, key).
		Scan(&result.ResolutionID, &result.IssueID, &result.Status, &result.ResolutionType, &result.EvidenceReference, &fingerprint)
	return result, fingerprint, err
}

func reconciliationResolutionFingerprint(command ResolveReconciliationIssueCommand) string {
	canonical := fmt.Sprintf("%s\n%s\n%s", command.IssueID, command.ResolutionType, command.EvidenceReference)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func NewReconciliationIssueResolutionHandler(store ReconciliationIssueResolutionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command ResolveReconciliationIssueCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		result, err := store.ResolveReconciliationIssue(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidReconciliationResolution):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict), errors.Is(err, ErrReconciliationIssueNotOpen):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve reconciliation issue"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}
