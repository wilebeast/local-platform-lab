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
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidRefundRequest = errors.New("invalid refund request")
	ErrPaymentNotRefundable = errors.New("payment is not refundable")
)

type RefundPaymentCommand struct {
	IdempotencyKey string `json:"-"`
	MerchantID     string `json:"merchant_id"`
	PaymentID      string `json:"payment_id"`
}

type RefundPaymentResult struct {
	RefundID        string `json:"refund_id"`
	PaymentID       string `json:"payment_id"`
	Status          string `json:"status"`
	Currency        string `json:"currency"`
	AmountMinor     int64  `json:"amount_minor"`
	FeeMinor        int64  `json:"fee_minor"`
	RefundJournalID string `json:"refund_journal_id"`
	Replayed        bool   `json:"replayed"`
}

type RefundPaymentStore interface {
	RefundPayment(context.Context, RefundPaymentCommand) (RefundPaymentResult, error)
}

func (s *PostgresPaymentStore) RefundPayment(ctx context.Context, command RefundPaymentCommand) (RefundPaymentResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.MerchantID = strings.TrimSpace(command.MerchantID)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	if command.IdempotencyKey == "" || command.MerchantID == "" || command.PaymentID == "" {
		return RefundPaymentResult{}, ErrInvalidRefundRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RefundPaymentResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, command.MerchantID+":"+command.IdempotencyKey); err != nil {
		return RefundPaymentResult{}, err
	}
	fingerprint := refundFingerprint(command)
	existing, storedFingerprint, err := loadRefundByIdempotency(ctx, tx, command.MerchantID, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return RefundPaymentResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return RefundPaymentResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RefundPaymentResult{}, err
	}

	var currency, status, provider string
	var amount, fee int64
	err = tx.QueryRow(ctx, `SELECT o.currency,o.amount_minor,o.fee_minor,o.status,a.provider
		FROM acquiring_payment_orders o JOIN acquiring_payment_attempts a ON a.payment_id=o.payment_id
		WHERE o.payment_id=$1 AND o.merchant_id=$2 FOR UPDATE OF o,a`, command.PaymentID, command.MerchantID).
		Scan(&currency, &amount, &fee, &status, &provider)
	if errors.Is(err, pgx.ErrNoRows) || status != "succeeded" {
		return RefundPaymentResult{}, ErrPaymentNotRefundable
	}
	if err != nil {
		return RefundPaymentResult{}, err
	}

	refundID := stablePaymentID("refund", command.MerchantID+":"+command.IdempotencyKey)
	journalID := stablePaymentID("journal-refund", refundID)
	journal, err := RefundJournal(RefundCommand{RefundID: refundID, PaymentID: command.PaymentID, Currency: currency, AmountMinor: amount, FeeMinor: fee})
	if err != nil {
		return RefundPaymentResult{}, err
	}
	accountIDs, err := ensureAcquiringAccounts(ctx, tx, provider, command.MerchantID, currency)
	if err != nil {
		return RefundPaymentResult{}, err
	}
	if err = postAcquiringJournal(ctx, tx, journalID, journal, fingerprint, time.Now().Unix(), accountIDs); err != nil {
		return RefundPaymentResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_refunds(refund_id,merchant_id,payment_id,idempotency_key,command_fingerprint,currency,amount_minor,fee_minor,status,refund_journal_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'refunded',$9)`, refundID, command.MerchantID, command.PaymentID, command.IdempotencyKey, fingerprint, currency, amount, fee, journalID)
	if isUniqueViolation(err) {
		return RefundPaymentResult{}, ErrPaymentIdempotencyConflict
	}
	if err != nil {
		return RefundPaymentResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE acquiring_payment_orders SET status='refunded',updated_at=NOW() WHERE payment_id=$1`, command.PaymentID); err != nil {
		return RefundPaymentResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RefundPaymentResult{}, err
	}
	return RefundPaymentResult{RefundID: refundID, PaymentID: command.PaymentID, Status: "refunded", Currency: currency, AmountMinor: amount, FeeMinor: fee, RefundJournalID: journalID}, nil
}

func loadRefundByIdempotency(ctx context.Context, tx pgx.Tx, merchantID, key string) (RefundPaymentResult, string, error) {
	var result RefundPaymentResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT refund_id,payment_id,status,currency,amount_minor,fee_minor,refund_journal_id,command_fingerprint
		FROM acquiring_refunds WHERE merchant_id=$1 AND idempotency_key=$2`, merchantID, key).
		Scan(&result.RefundID, &result.PaymentID, &result.Status, &result.Currency, &result.AmountMinor, &result.FeeMinor, &result.RefundJournalID, &fingerprint)
	return result, fingerprint, err
}

func refundFingerprint(command RefundPaymentCommand) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s", command.MerchantID, command.PaymentID)))
	return hex.EncodeToString(sum[:])
}

func NewRefundPaymentHandler(store RefundPaymentStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command RefundPaymentCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		result, err := store.RefundPayment(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidRefundRequest):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentNotRefundable):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "refund payment"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}
