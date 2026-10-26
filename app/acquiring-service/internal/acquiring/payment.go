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
	"sync"
)

type OrderStatus string
type AttemptStatus string
type OperationStatus string

const (
	OrderCreated     OrderStatus     = "created"
	AttemptCreated   AttemptStatus   = "created"
	OperationPending OperationStatus = "pending"
	fixedFeeMinor                    = int64(200)
)

var (
	ErrPaymentIdempotencyConflict = errors.New("idempotency key belongs to another request")
	ErrInvalidPaymentRequest      = errors.New("invalid payment request")
)

type CreatePaymentCommand struct {
	IdempotencyKey string `json:"-"`
	MerchantID     string `json:"merchant_id"`
	MerchantOrder  string `json:"merchant_order_id"`
	Currency       string `json:"currency"`
	AmountMinor    int64  `json:"amount_minor"`
	PaymentMethod  string `json:"payment_method"`
}

type PaymentOrder struct {
	ID               string      `json:"id"`
	MerchantID       string      `json:"merchant_id"`
	MerchantOrderID  string      `json:"merchant_order_id"`
	Currency         string      `json:"currency"`
	AmountMinor      int64       `json:"amount_minor"`
	FeeMinor         int64       `json:"fee_minor"`
	MerchantNetMinor int64       `json:"merchant_net_minor"`
	Status           OrderStatus `json:"status"`
}

type PaymentAttempt struct {
	ID            string        `json:"id"`
	PaymentID     string        `json:"payment_id"`
	PaymentMethod string        `json:"payment_method"`
	Provider      string        `json:"provider"`
	Status        AttemptStatus `json:"status"`
}

type ChannelOperation struct {
	ID            string          `json:"id"`
	AttemptID     string          `json:"attempt_id"`
	Provider      string          `json:"provider"`
	ProviderReqID string          `json:"provider_request_id"`
	Status        OperationStatus `json:"status"`
}

type CreatePaymentResult struct {
	Order       PaymentOrder     `json:"order"`
	Attempt     PaymentAttempt   `json:"attempt"`
	Operation   ChannelOperation `json:"channel_operation"`
	CheckoutURL string           `json:"checkout_url"`
	Replayed    bool             `json:"replayed"`
}

type PaymentStore interface {
	CreatePayment(context.Context, CreatePaymentCommand) (CreatePaymentResult, error)
}

type storedPayment struct {
	Fingerprint string
	Result      CreatePaymentResult
}

type MemoryPaymentStore struct {
	mu               sync.Mutex
	payments         map[string]storedPayment
	financialEffects int
}

func NewMemoryPaymentStore() *MemoryPaymentStore {
	return &MemoryPaymentStore{payments: make(map[string]storedPayment)}
}

func (s *MemoryPaymentStore) CreatePayment(_ context.Context, command CreatePaymentCommand) (CreatePaymentResult, error) {
	if err := validateCreatePayment(command); err != nil {
		return CreatePaymentResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	scope := command.MerchantID + ":" + command.IdempotencyKey
	fingerprint := paymentFingerprint(command)
	if existing, ok := s.payments[scope]; ok {
		if existing.Fingerprint != fingerprint {
			return CreatePaymentResult{}, ErrPaymentIdempotencyConflict
		}
		result := existing.Result
		result.Replayed = true
		return result, nil
	}

	paymentID := stablePaymentID("pay", scope)
	attemptID := stablePaymentID("attempt", paymentID+":"+command.PaymentMethod)
	operationID := stablePaymentID("operation", attemptID)
	provider := routeProvider(command.PaymentMethod)
	result := CreatePaymentResult{
		Order: PaymentOrder{
			ID: paymentID, MerchantID: command.MerchantID, MerchantOrderID: command.MerchantOrder,
			Currency: command.Currency, AmountMinor: command.AmountMinor, FeeMinor: fixedFeeMinor,
			MerchantNetMinor: command.AmountMinor - fixedFeeMinor, Status: OrderCreated,
		},
		Attempt: PaymentAttempt{
			ID: attemptID, PaymentID: paymentID, PaymentMethod: command.PaymentMethod,
			Provider: provider, Status: AttemptCreated,
		},
		Operation: ChannelOperation{
			ID: operationID, AttemptID: attemptID, Provider: provider,
			ProviderReqID: stablePaymentID("provider-request", operationID), Status: OperationPending,
		},
		CheckoutURL: "/checkout/" + paymentID,
	}
	s.payments[scope] = storedPayment{Fingerprint: fingerprint, Result: result}
	return result, nil
}

func (s *MemoryPaymentStore) OrderCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.payments)
}
func (s *MemoryPaymentStore) AttemptCount() int   { return s.OrderCount() }
func (s *MemoryPaymentStore) OperationCount() int { return s.OrderCount() }
func (s *MemoryPaymentStore) FinancialEffectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.financialEffects
}

func NewCreatePaymentHandler(store PaymentStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/payments" {
			http.NotFound(w, r)
			return
		}
		var command CreatePaymentCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		command.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		command.Currency = strings.ToUpper(strings.TrimSpace(command.Currency))
		result, err := store.CreatePayment(r.Context(), command)
		switch {
		case errors.Is(err, ErrInvalidPaymentRequest):
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrPaymentIdempotencyConflict):
			writePaymentJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case err != nil:
			writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": "create payment"})
		default:
			status := http.StatusCreated
			if result.Replayed {
				status = http.StatusOK
			}
			writePaymentJSON(w, status, result)
		}
	})
}

func validateCreatePayment(command CreatePaymentCommand) error {
	if command.IdempotencyKey == "" || command.MerchantID == "" || command.MerchantOrder == "" ||
		command.Currency != "USD" || command.AmountMinor <= fixedFeeMinor ||
		(command.PaymentMethod != "card" && command.PaymentMethod != "bank_transfer") {
		return ErrInvalidPaymentRequest
	}
	return nil
}

func paymentFingerprint(command CreatePaymentCommand) string {
	canonical := fmt.Sprintf("%s\n%s\n%s\n%d\n%s", command.MerchantID, command.MerchantOrder, command.Currency, command.AmountMinor, command.PaymentMethod)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func stablePaymentID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func routeProvider(paymentMethod string) string {
	if paymentMethod == "bank_transfer" {
		return "demo-bank"
	}
	return "demo-card"
}

func writePaymentJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
