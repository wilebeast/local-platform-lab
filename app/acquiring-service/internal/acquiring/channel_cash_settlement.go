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
	ErrInvalidChannelCashSettlement = errors.New("invalid channel cash settlement request")
	ErrPaymentNotCashSettleable     = errors.New("payment is not ready for cash settlement")
)

type SettleChannelCashCommand struct {
	IdempotencyKey string `json:"-"`
	MerchantID     string `json:"merchant_id"`
	PaymentID      string `json:"payment_id"`
}

type SettleChannelCashResult struct {
	SettlementID        string `json:"settlement_id"`
	PaymentID           string `json:"payment_id"`
	Provider            string `json:"provider"`
	Status              string `json:"status"`
	Currency            string `json:"currency"`
	AmountMinor         int64  `json:"amount_minor"`
	SettlementJournalID string `json:"settlement_journal_id"`
	Replayed            bool   `json:"replayed"`
}

type ChannelCashSettlementStore interface {
	SettleChannelCash(context.Context, SettleChannelCashCommand) (SettleChannelCashResult, error)
}

func (s *PostgresPaymentStore) SettleChannelCash(ctx context.Context, command SettleChannelCashCommand) (SettleChannelCashResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.MerchantID = strings.TrimSpace(command.MerchantID)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	if command.IdempotencyKey == "" || command.MerchantID == "" || command.PaymentID == "" {
		return SettleChannelCashResult{}, ErrInvalidChannelCashSettlement
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SettleChannelCashResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, command.MerchantID+":"+command.IdempotencyKey); err != nil {
		return SettleChannelCashResult{}, err
	}
	fingerprint := channelCashSettlementFingerprint(command)
	existing, storedFingerprint, err := loadChannelCashSettlementByIdempotency(ctx, tx, command.MerchantID, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return SettleChannelCashResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return SettleChannelCashResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SettleChannelCashResult{}, err
	}

	var currency, status, provider string
	var amount int64
	err = tx.QueryRow(ctx, `SELECT o.currency,o.amount_minor,o.status,a.provider
		FROM acquiring_payment_orders o JOIN acquiring_payment_attempts a ON a.payment_id=o.payment_id
		WHERE o.payment_id=$1 AND o.merchant_id=$2
		AND NOT EXISTS(SELECT 1 FROM acquiring_channel_settlements n WHERE n.payment_id=o.payment_id)
		FOR UPDATE OF o,a`, command.PaymentID, command.MerchantID).Scan(&currency, &amount, &status, &provider)
	if errors.Is(err, pgx.ErrNoRows) || status != "succeeded" {
		return SettleChannelCashResult{}, ErrPaymentNotCashSettleable
	}
	if err != nil {
		return SettleChannelCashResult{}, err
	}

	settlementID := stablePaymentID("channel-cash-settlement", command.MerchantID+":"+command.IdempotencyKey)
	journalID := stablePaymentID("journal-channel-cash", settlementID)
	journal, err := ChannelCashSettlementJournal(ChannelCashSettlementCommand{SettlementID: settlementID, Currency: currency, AmountMinor: amount})
	if err != nil {
		return SettleChannelCashResult{}, err
	}
	accountIDs, err := ensureAcquiringAccounts(ctx, tx, provider, command.MerchantID, currency)
	if err != nil {
		return SettleChannelCashResult{}, err
	}
	if err = postAcquiringJournal(ctx, tx, journalID, journal, fingerprint, time.Now().Unix(), accountIDs); err != nil {
		return SettleChannelCashResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_channel_cash_settlements(settlement_id,merchant_id,payment_id,provider,idempotency_key,command_fingerprint,currency,amount_minor,status,settlement_journal_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'received',$9)`, settlementID, command.MerchantID, command.PaymentID, provider, command.IdempotencyKey, fingerprint, currency, amount, journalID)
	if isUniqueViolation(err) {
		return SettleChannelCashResult{}, ErrPaymentIdempotencyConflict
	}
	if err != nil {
		return SettleChannelCashResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SettleChannelCashResult{}, err
	}
	return SettleChannelCashResult{SettlementID: settlementID, PaymentID: command.PaymentID, Provider: provider, Status: "received", Currency: currency, AmountMinor: amount, SettlementJournalID: journalID}, nil
}

func loadChannelCashSettlementByIdempotency(ctx context.Context, tx pgx.Tx, merchantID, key string) (SettleChannelCashResult, string, error) {
	var result SettleChannelCashResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT settlement_id,payment_id,provider,status,currency,amount_minor,settlement_journal_id,command_fingerprint
		FROM acquiring_channel_cash_settlements WHERE merchant_id=$1 AND idempotency_key=$2`, merchantID, key).
		Scan(&result.SettlementID, &result.PaymentID, &result.Provider, &result.Status, &result.Currency, &result.AmountMinor, &result.SettlementJournalID, &fingerprint)
	return result, fingerprint, err
}

func channelCashSettlementFingerprint(command SettleChannelCashCommand) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s", command.MerchantID, command.PaymentID)))
	return hex.EncodeToString(sum[:])
}

func NewChannelCashSettlementHandler(store ChannelCashSettlementStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command SettleChannelCashCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		result, err := store.SettleChannelCash(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidChannelCashSettlement):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentNotCashSettleable):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "settle channel cash"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}
