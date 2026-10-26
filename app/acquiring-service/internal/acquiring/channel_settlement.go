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
	ErrInvalidChannelSettlement = errors.New("invalid channel settlement request")
	ErrPaymentNotNetSettleable  = errors.New("payment is not ready for net settlement")
)

type SettleChannelNetCommand struct {
	IdempotencyKey string `json:"-"`
	MerchantID     string `json:"merchant_id"`
	PaymentID      string `json:"payment_id"`
}

type SettleChannelNetResult struct {
	SettlementID        string `json:"settlement_id"`
	PaymentID           string `json:"payment_id"`
	RefundID            string `json:"refund_id"`
	Provider            string `json:"provider"`
	Status              string `json:"status"`
	Currency            string `json:"currency"`
	AmountMinor         int64  `json:"amount_minor"`
	SettlementJournalID string `json:"settlement_journal_id"`
	Replayed            bool   `json:"replayed"`
}

type ChannelNetSettlementStore interface {
	SettleChannelNet(context.Context, SettleChannelNetCommand) (SettleChannelNetResult, error)
}

func (s *PostgresPaymentStore) SettleChannelNet(ctx context.Context, command SettleChannelNetCommand) (SettleChannelNetResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.MerchantID = strings.TrimSpace(command.MerchantID)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	if command.IdempotencyKey == "" || command.MerchantID == "" || command.PaymentID == "" {
		return SettleChannelNetResult{}, ErrInvalidChannelSettlement
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SettleChannelNetResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, command.MerchantID+":"+command.IdempotencyKey); err != nil {
		return SettleChannelNetResult{}, err
	}
	fingerprint := channelSettlementFingerprint(command)
	existing, storedFingerprint, err := loadChannelSettlementByIdempotency(ctx, tx, command.MerchantID, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return SettleChannelNetResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return SettleChannelNetResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SettleChannelNetResult{}, err
	}

	var refundID, currency, orderStatus, provider string
	var amount int64
	err = tx.QueryRow(ctx, `SELECT r.refund_id,r.currency,r.amount_minor,o.status,a.provider
		FROM acquiring_payment_orders o
		JOIN acquiring_refunds r ON r.payment_id=o.payment_id
		JOIN acquiring_payment_attempts a ON a.payment_id=o.payment_id
		WHERE o.payment_id=$1 AND o.merchant_id=$2
		AND NOT EXISTS(SELECT 1 FROM acquiring_channel_cash_settlements c WHERE c.payment_id=o.payment_id)
		FOR UPDATE OF o,r,a`, command.PaymentID, command.MerchantID).
		Scan(&refundID, &currency, &amount, &orderStatus, &provider)
	if errors.Is(err, pgx.ErrNoRows) || orderStatus != "refunded" {
		return SettleChannelNetResult{}, ErrPaymentNotNetSettleable
	}
	if err != nil {
		return SettleChannelNetResult{}, err
	}

	settlementID := stablePaymentID("channel-settlement", command.MerchantID+":"+command.IdempotencyKey)
	journalID := stablePaymentID("journal-channel-settlement", settlementID)
	journal, err := ChannelNetSettlementJournal(ChannelNetSettlementCommand{SettlementID: settlementID, Currency: currency, AmountMinor: amount})
	if err != nil {
		return SettleChannelNetResult{}, err
	}
	accountIDs, err := ensureAcquiringAccounts(ctx, tx, provider, command.MerchantID, currency)
	if err != nil {
		return SettleChannelNetResult{}, err
	}
	if err = postAcquiringJournal(ctx, tx, journalID, journal, fingerprint, time.Now().Unix(), accountIDs); err != nil {
		return SettleChannelNetResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_channel_settlements(settlement_id,merchant_id,payment_id,refund_id,provider,idempotency_key,command_fingerprint,currency,amount_minor,status,settlement_journal_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'netted',$10)`, settlementID, command.MerchantID, command.PaymentID, refundID, provider, command.IdempotencyKey, fingerprint, currency, amount, journalID)
	if isUniqueViolation(err) {
		return SettleChannelNetResult{}, ErrPaymentIdempotencyConflict
	}
	if err != nil {
		return SettleChannelNetResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SettleChannelNetResult{}, err
	}
	return SettleChannelNetResult{SettlementID: settlementID, PaymentID: command.PaymentID, RefundID: refundID, Provider: provider, Status: "netted", Currency: currency, AmountMinor: amount, SettlementJournalID: journalID}, nil
}

func loadChannelSettlementByIdempotency(ctx context.Context, tx pgx.Tx, merchantID, key string) (SettleChannelNetResult, string, error) {
	var result SettleChannelNetResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT settlement_id,payment_id,refund_id,provider,status,currency,amount_minor,settlement_journal_id,command_fingerprint
		FROM acquiring_channel_settlements WHERE merchant_id=$1 AND idempotency_key=$2`, merchantID, key).
		Scan(&result.SettlementID, &result.PaymentID, &result.RefundID, &result.Provider, &result.Status, &result.Currency, &result.AmountMinor, &result.SettlementJournalID, &fingerprint)
	return result, fingerprint, err
}

func channelSettlementFingerprint(command SettleChannelNetCommand) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s", command.MerchantID, command.PaymentID)))
	return hex.EncodeToString(sum[:])
}

func NewChannelNetSettlementHandler(store ChannelNetSettlementStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command SettleChannelNetCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = r.Header.Get("Idempotency-Key")
		result, err := store.SettleChannelNet(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidChannelSettlement):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentNotNetSettleable):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "settle channel net"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}
