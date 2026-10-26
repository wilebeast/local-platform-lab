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
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidPayoutCorrection = errors.New("invalid payout correction")
	ErrPayoutNotCorrectable    = errors.New("payout is not correctable")
)

type CorrectMissingProviderPayoutCommand struct {
	IdempotencyKey    string `json:"-"`
	IssueID           string `json:"issue_id"`
	EvidenceReference string `json:"evidence_reference"`
}

type CorrectMissingProviderPayoutResult struct {
	ResolutionID      string `json:"resolution_id"`
	IssueID           string `json:"issue_id"`
	PayoutID          string `json:"payout_id"`
	PaymentID         string `json:"payment_id"`
	Status            string `json:"status"`
	ReversalJournalID string `json:"reversal_journal_id"`
	EvidenceReference string `json:"evidence_reference"`
	Replayed          bool   `json:"replayed"`
}

type MissingProviderPayoutCorrectionStore interface {
	CorrectMissingProviderPayout(context.Context, CorrectMissingProviderPayoutCommand) (CorrectMissingProviderPayoutResult, error)
}

func (s *PostgresPaymentStore) CorrectMissingProviderPayout(ctx context.Context, command CorrectMissingProviderPayoutCommand) (CorrectMissingProviderPayoutResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.IssueID = strings.TrimSpace(command.IssueID)
	command.EvidenceReference = strings.TrimSpace(command.EvidenceReference)
	if command.IdempotencyKey == "" || command.IssueID == "" || command.EvidenceReference == "" {
		return CorrectMissingProviderPayoutResult{}, ErrInvalidPayoutCorrection
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "payout-correction:"+command.IdempotencyKey); err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	fingerprint := payoutCorrectionFingerprint(command)
	existing, storedFingerprint, err := loadPayoutCorrectionByIdempotency(ctx, tx, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return CorrectMissingProviderPayoutResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return CorrectMissingProviderPayoutResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CorrectMissingProviderPayoutResult{}, err
	}

	var issueStatus, issueType, payoutID, paymentID, payoutStatus, paymentStatus, originalJournalID string
	err = tx.QueryRow(ctx, `SELECT i.status,i.issue_type,p.payout_id,p.payment_id,p.status,o.status,p.payout_journal_id
		FROM acquiring_reconciliation_issues i
		JOIN acquiring_merchant_payouts p ON i.business_type='merchant_payout' AND p.payout_id=i.business_id
		JOIN acquiring_payment_orders o ON o.payment_id=p.payment_id
		WHERE i.issue_id=$1 FOR UPDATE OF i,p,o`, command.IssueID).
		Scan(&issueStatus, &issueType, &payoutID, &paymentID, &payoutStatus, &paymentStatus, &originalJournalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CorrectMissingProviderPayoutResult{}, ErrInvalidPayoutCorrection
	}
	if err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	if issueStatus != "open" || issueType != "missing_provider_payout" || payoutStatus != "paid" || paymentStatus != "paid_out" {
		return CorrectMissingProviderPayoutResult{}, ErrPayoutNotCorrectable
	}

	resolutionID := stablePaymentID("reconciliation-resolution", command.IssueID+":local_payout_not_sent:"+command.EvidenceReference)
	reversalJournalID := stablePaymentID("journal-payout-reversal", resolutionID)
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_reconciliation_resolutions(
		resolution_id,issue_id,idempotency_key,command_fingerprint,resolution_type,evidence_reference,reversal_journal_id)
		VALUES($1,$2,$3,$4,'local_payout_not_sent',$5,$6)`, resolutionID, command.IssueID, command.IdempotencyKey, fingerprint, command.EvidenceReference, reversalJournalID)
	if isUniqueViolation(err) {
		return CorrectMissingProviderPayoutResult{}, ErrPayoutNotCorrectable
	}
	if err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	if err := postPayoutReversalJournal(ctx, tx, reversalJournalID, resolutionID, originalJournalID, fingerprint); err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_merchant_payouts SET status='reversed' WHERE payout_id=$1`, payoutID); err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_payment_orders SET status='succeeded',updated_at=NOW() WHERE payment_id=$1`, paymentID); err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_reconciliation_issues SET status='resolved',resolved_at=NOW() WHERE issue_id=$1`, command.IssueID); err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CorrectMissingProviderPayoutResult{}, err
	}
	return CorrectMissingProviderPayoutResult{
		ResolutionID: resolutionID, IssueID: command.IssueID, PayoutID: payoutID, PaymentID: paymentID,
		Status: "resolved", ReversalJournalID: reversalJournalID, EvidenceReference: command.EvidenceReference,
	}, nil
}

func postPayoutReversalJournal(ctx context.Context, tx pgx.Tx, journalID, resolutionID, originalJournalID, fingerprint string) error {
	var bookID, currency, status string
	if err := tx.QueryRow(ctx, `SELECT book_id,currency,status FROM ledger_journals WHERE id=$1 FOR UPDATE`, originalJournalID).Scan(&bookID, &currency, &status); err != nil {
		return err
	}
	if status != "posted" {
		return ErrPayoutNotCorrectable
	}
	type reversalPosting struct {
		accountID string
		side      Side
		amount    int64
	}
	rows, err := tx.Query(ctx, `SELECT account_id,side,amount_minor FROM ledger_postings WHERE journal_id=$1 ORDER BY sequence_no`, originalJournalID)
	if err != nil {
		return err
	}
	var postings []reversalPosting
	var debits, credits int64
	for rows.Next() {
		var posting reversalPosting
		if err := rows.Scan(&posting.accountID, &posting.side, &posting.amount); err != nil {
			rows.Close()
			return err
		}
		if posting.side == Debit {
			posting.side = Credit
			credits += posting.amount
		} else if posting.side == Credit {
			posting.side = Debit
			debits += posting.amount
		} else {
			rows.Close()
			return ErrPayoutNotCorrectable
		}
		postings = append(postings, posting)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(postings) < 2 || debits != credits {
		return ErrPayoutNotCorrectable
	}
	_, err = tx.Exec(ctx, `INSERT INTO ledger_journals(
		id,book_id,business_type,business_id,event_type,currency,status,reversal_of,command_fingerprint,effective_at)
		VALUES($1,$2,'merchant_payout_correction',$3,'reversal',$4,'draft',$5,$6,$7)`, journalID, bookID, resolutionID, currency, originalJournalID, fingerprint, time.Now().Unix())
	if err != nil {
		return err
	}
	accountIDs := make([]string, 0, len(postings))
	for _, posting := range postings {
		accountIDs = append(accountIDs, posting.accountID)
	}
	sort.Strings(accountIDs)
	for _, accountID := range accountIDs {
		if _, err := tx.Exec(ctx, `SELECT id FROM ledger_accounts WHERE id=$1 FOR UPDATE`, accountID); err != nil {
			return err
		}
	}
	for index, posting := range postings {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_postings(journal_id,sequence_no,account_id,side,amount_minor,currency) VALUES($1,$2,$3,$4,$5,$6)`,
			journalID, index+1, posting.accountID, posting.side, posting.amount, currency); err != nil {
			return err
		}
		column := "posted_debits_minor"
		if posting.side == Credit {
			column = "posted_credits_minor"
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE ledger_balances SET %s=%s+$1,version=version+1,updated_at=NOW() WHERE account_id=$2`, column, column), posting.amount, posting.accountID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ledger_journals SET status='posted',posted_at=$1 WHERE id=$2`, time.Now().Unix(), journalID)
	return err
}

func loadPayoutCorrectionByIdempotency(ctx context.Context, tx pgx.Tx, key string) (CorrectMissingProviderPayoutResult, string, error) {
	var result CorrectMissingProviderPayoutResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT r.resolution_id,r.issue_id,p.payout_id,p.payment_id,i.status,r.reversal_journal_id,r.evidence_reference,r.command_fingerprint
		FROM acquiring_reconciliation_resolutions r
		JOIN acquiring_reconciliation_issues i ON i.issue_id=r.issue_id
		JOIN acquiring_merchant_payouts p ON p.payout_id=i.business_id
		WHERE r.idempotency_key=$1 AND r.resolution_type='local_payout_not_sent'`, key).
		Scan(&result.ResolutionID, &result.IssueID, &result.PayoutID, &result.PaymentID, &result.Status, &result.ReversalJournalID, &result.EvidenceReference, &fingerprint)
	return result, fingerprint, err
}

func payoutCorrectionFingerprint(command CorrectMissingProviderPayoutCommand) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s", command.IssueID, command.EvidenceReference)))
	return hex.EncodeToString(sum[:])
}

func NewMissingProviderPayoutCorrectionHandler(store MissingProviderPayoutCorrectionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command CorrectMissingProviderPayoutCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		result, err := store.CorrectMissingProviderPayout(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidPayoutCorrection):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict), errors.Is(err, ErrPayoutNotCorrectable):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "correct missing provider payout"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}
