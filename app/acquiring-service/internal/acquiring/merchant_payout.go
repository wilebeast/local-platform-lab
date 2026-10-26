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
	ErrInvalidMerchantPayout = errors.New("invalid merchant payout request")
	ErrPaymentNotPayable     = errors.New("payment is not ready for merchant payout")
)

type PayMerchantCommand struct {
	IdempotencyKey string `json:"-"`
	MerchantID     string `json:"merchant_id"`
	PaymentID      string `json:"payment_id"`
}

type PayMerchantResult struct {
	PayoutID        string `json:"payout_id"`
	PaymentID       string `json:"payment_id"`
	Status          string `json:"status"`
	Currency        string `json:"currency"`
	AmountMinor     int64  `json:"amount_minor"`
	PayoutJournalID string `json:"payout_journal_id"`
	Replayed        bool   `json:"replayed"`
}

type MerchantPayoutStore interface {
	PayMerchant(context.Context, PayMerchantCommand) (PayMerchantResult, error)
}

func (s *PostgresPaymentStore) PayMerchant(ctx context.Context, command PayMerchantCommand) (PayMerchantResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.MerchantID = strings.TrimSpace(command.MerchantID)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	if command.IdempotencyKey == "" || command.MerchantID == "" || command.PaymentID == "" {
		return PayMerchantResult{}, ErrInvalidMerchantPayout
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PayMerchantResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, command.MerchantID+":"+command.IdempotencyKey); err != nil {
		return PayMerchantResult{}, err
	}
	fingerprint := merchantPayoutFingerprint(command)
	existing, storedFingerprint, err := loadMerchantPayoutByIdempotency(ctx, tx, command.MerchantID, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return PayMerchantResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return PayMerchantResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PayMerchantResult{}, err
	}

	var cashSettlementID, currency, orderStatus, provider string
	var merchantNet int64
	err = tx.QueryRow(ctx, `SELECT c.settlement_id,o.currency,o.merchant_net_minor,o.status,a.provider
		FROM acquiring_payment_orders o
		JOIN acquiring_channel_cash_settlements c ON c.payment_id=o.payment_id
		JOIN acquiring_payment_attempts a ON a.payment_id=o.payment_id
		WHERE o.payment_id=$1 AND o.merchant_id=$2 FOR UPDATE OF o,c,a`, command.PaymentID, command.MerchantID).
		Scan(&cashSettlementID, &currency, &merchantNet, &orderStatus, &provider)
	if errors.Is(err, pgx.ErrNoRows) || orderStatus != "succeeded" {
		return PayMerchantResult{}, ErrPaymentNotPayable
	}
	if err != nil {
		return PayMerchantResult{}, err
	}

	payoutID := stablePaymentID("merchant-payout", command.MerchantID+":"+command.IdempotencyKey)
	journalID := stablePaymentID("journal-merchant-payout", payoutID)
	journal, err := MerchantPayoutJournal(MerchantPayoutCommand{PayoutID: payoutID, Currency: currency, AmountMinor: merchantNet})
	if err != nil {
		return PayMerchantResult{}, err
	}
	accountIDs, err := ensureAcquiringAccounts(ctx, tx, provider, command.MerchantID, currency)
	if err != nil {
		return PayMerchantResult{}, err
	}
	if err = postAcquiringJournal(ctx, tx, journalID, journal, fingerprint, time.Now().Unix(), accountIDs); err != nil {
		return PayMerchantResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_merchant_payouts(payout_id,merchant_id,payment_id,cash_settlement_id,idempotency_key,command_fingerprint,currency,amount_minor,status,payout_journal_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'paid',$9)`, payoutID, command.MerchantID, command.PaymentID, cashSettlementID, command.IdempotencyKey, fingerprint, currency, merchantNet, journalID)
	if isUniqueViolation(err) {
		return PayMerchantResult{}, ErrPaymentIdempotencyConflict
	}
	if err != nil {
		return PayMerchantResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE acquiring_payment_orders SET status='paid_out',updated_at=NOW() WHERE payment_id=$1`, command.PaymentID); err != nil {
		return PayMerchantResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PayMerchantResult{}, err
	}
	return PayMerchantResult{PayoutID: payoutID, PaymentID: command.PaymentID, Status: "paid", Currency: currency, AmountMinor: merchantNet, PayoutJournalID: journalID}, nil
}

func loadMerchantPayoutByIdempotency(ctx context.Context, tx pgx.Tx, merchantID, key string) (PayMerchantResult, string, error) {
	var result PayMerchantResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT payout_id,payment_id,status,currency,amount_minor,payout_journal_id,command_fingerprint
		FROM acquiring_merchant_payouts WHERE merchant_id=$1 AND idempotency_key=$2`, merchantID, key).
		Scan(&result.PayoutID, &result.PaymentID, &result.Status, &result.Currency, &result.AmountMinor, &result.PayoutJournalID, &fingerprint)
	return result, fingerprint, err
}

func merchantPayoutFingerprint(command PayMerchantCommand) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s", command.MerchantID, command.PaymentID)))
	return hex.EncodeToString(sum[:])
}

func NewMerchantPayoutHandler(store MerchantPayoutStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command PayMerchantCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		result, err := store.PayMerchant(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidMerchantPayout):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentNotPayable):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "pay merchant"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}
