package card

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type AccountBalance struct {
	CardID                string `json:"card_id"`
	Currency              string `json:"currency"`
	PostedBalanceMinor    int64  `json:"posted_balance_minor"`
	HeldAmountMinor       int64  `json:"held_amount_minor"`
	AvailableBalanceMinor int64  `json:"available_balance_minor"`
}

type FundParams struct {
	EntryID, CardID, Currency string
	AmountMinor               int64
}
type FinanceStore interface {
	Fund(context.Context, FundParams) (AccountBalance, error)
}

type AuthorizationParams struct {
	AuthorizationID, ProviderCardID, Currency string
	AmountMinor                               int64
}
type AuthorizationResult struct {
	AuthorizationID       string `json:"authorization_id"`
	Decision              string `json:"decision"`
	Reason                string `json:"reason,omitempty"`
	PostedBalanceMinor    int64  `json:"posted_balance_minor"`
	HeldAmountMinor       int64  `json:"held_amount_minor"`
	AvailableBalanceMinor int64  `json:"available_balance_minor"`
}
type AuthorizationStore interface {
	Authorize(context.Context, AuthorizationParams) (AuthorizationResult, error)
}

type AuthorizationReversalParams struct {
	ReversalID, AuthorizationID string
}
type AuthorizationReversalResult struct {
	ReversalID            string `json:"reversal_id"`
	AuthorizationID       string `json:"authorization_id"`
	Status                string `json:"status"`
	ReleasedAmountMinor   int64  `json:"released_amount_minor"`
	PostedBalanceMinor    int64  `json:"posted_balance_minor"`
	HeldAmountMinor       int64  `json:"held_amount_minor"`
	AvailableBalanceMinor int64  `json:"available_balance_minor"`
}
type AuthorizationReversalStore interface {
	ReverseAuthorization(context.Context, AuthorizationReversalParams) (AuthorizationReversalResult, error)
}

type AuthorizationSettlementParams struct {
	SettlementID, AuthorizationID string
}
type AuthorizationSettlementResult struct {
	SettlementID          string `json:"settlement_id"`
	AuthorizationID       string `json:"authorization_id"`
	Status                string `json:"status"`
	SettledAmountMinor    int64  `json:"settled_amount_minor"`
	PostedBalanceMinor    int64  `json:"posted_balance_minor"`
	HeldAmountMinor       int64  `json:"held_amount_minor"`
	AvailableBalanceMinor int64  `json:"available_balance_minor"`
}
type AuthorizationSettlementStore interface {
	SettleAuthorization(context.Context, AuthorizationSettlementParams) (AuthorizationSettlementResult, error)
}

type SettlementRefundParams struct {
	RefundID, SettlementID string
}
type SettlementRefundResult struct {
	RefundID              string `json:"refund_id"`
	SettlementID          string `json:"settlement_id"`
	Status                string `json:"status"`
	RefundedAmountMinor   int64  `json:"refunded_amount_minor"`
	PostedBalanceMinor    int64  `json:"posted_balance_minor"`
	HeldAmountMinor       int64  `json:"held_amount_minor"`
	AvailableBalanceMinor int64  `json:"available_balance_minor"`
}
type SettlementRefundStore interface {
	RefundSettlement(context.Context, SettlementRefundParams) (SettlementRefundResult, error)
}

type OperationalLedgerReconciliationStore interface {
	ReconcileOperationalLedger(context.Context) (OperationalLedgerReconciliation, error)
}

func NewOperationalLedgerReconciliationHandler(store OperationalLedgerReconciliationStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/card-lab/ledger/reconciliation" {
			http.NotFound(w, r)
			return
		}
		report, err := store.ReconcileOperationalLedger(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "ledger reconciliation unavailable")
			return
		}
		writeJSON(w, http.StatusOK, report)
	})
}

type memoryFunding struct {
	cardID, currency string
	amount           int64
}
type memoryProviderCard struct{ cardID, status string }
type memoryAuthorization struct {
	params AuthorizationParams
	result AuthorizationResult
}
type memoryAuthorizationReversal struct {
	params AuthorizationReversalParams
	result AuthorizationReversalResult
}
type memoryAuthorizationSettlement struct {
	params AuthorizationSettlementParams
	result AuthorizationSettlementResult
}
type memorySettlementRefund struct {
	params SettlementRefundParams
	result SettlementRefundResult
}
type MemoryFinanceStore struct {
	mu             sync.Mutex
	accounts       map[string]AccountBalance
	funding        map[string]memoryFunding
	providerCards  map[string]memoryProviderCard
	authorizations map[string]memoryAuthorization
	reversals      map[string]memoryAuthorizationReversal
	settlements    map[string]memoryAuthorizationSettlement
	refunds        map[string]memorySettlementRefund
}

func NewMemoryFinanceStore() *MemoryFinanceStore {
	return &MemoryFinanceStore{accounts: map[string]AccountBalance{}, funding: map[string]memoryFunding{}, providerCards: map[string]memoryProviderCard{}, authorizations: map[string]memoryAuthorization{}, reversals: map[string]memoryAuthorizationReversal{}, settlements: map[string]memoryAuthorizationSettlement{}, refunds: map[string]memorySettlementRefund{}}
}

func (s *MemoryFinanceStore) RefundSettlement(_ context.Context, p SettlementRefundParams) (SettlementRefundResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.refunds[p.RefundID]; ok {
		if existing.params != p {
			return SettlementRefundResult{}, ErrIdempotencyConflict
		}
		return existing.result, nil
	}
	settlement, ok := s.settlements[p.SettlementID]
	if !ok {
		return SettlementRefundResult{}, ErrSettlementNotRefundable
	}
	for _, refund := range s.refunds {
		if refund.params.SettlementID == p.SettlementID {
			return SettlementRefundResult{}, ErrSettlementNotRefundable
		}
	}
	authorization := s.authorizations[settlement.params.AuthorizationID]
	card := s.providerCards[authorization.params.ProviderCardID]
	account := s.accounts[card.cardID]
	account.PostedBalanceMinor += settlement.result.SettledAmountMinor
	account.AvailableBalanceMinor = account.PostedBalanceMinor - account.HeldAmountMinor
	s.accounts[card.cardID] = account
	result := SettlementRefundResult{p.RefundID, p.SettlementID, "refunded", settlement.result.SettledAmountMinor, account.PostedBalanceMinor, account.HeldAmountMinor, account.AvailableBalanceMinor}
	s.refunds[p.RefundID] = memorySettlementRefund{p, result}
	return result, nil
}

func (s *MemoryFinanceStore) SettleAuthorization(_ context.Context, p AuthorizationSettlementParams) (AuthorizationSettlementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.settlements[p.SettlementID]; ok {
		if existing.params != p {
			return AuthorizationSettlementResult{}, ErrIdempotencyConflict
		}
		return existing.result, nil
	}
	authorization, ok := s.authorizations[p.AuthorizationID]
	if !ok || authorization.result.Decision != "approved" {
		return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
	}
	for _, reversal := range s.reversals {
		if reversal.params.AuthorizationID == p.AuthorizationID {
			return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
		}
	}
	for _, settlement := range s.settlements {
		if settlement.params.AuthorizationID == p.AuthorizationID {
			return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
		}
	}
	card := s.providerCards[authorization.params.ProviderCardID]
	account := s.accounts[card.cardID]
	if account.HeldAmountMinor < authorization.params.AmountMinor || account.PostedBalanceMinor < authorization.params.AmountMinor {
		return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
	}
	account.PostedBalanceMinor -= authorization.params.AmountMinor
	account.HeldAmountMinor -= authorization.params.AmountMinor
	account.AvailableBalanceMinor = account.PostedBalanceMinor - account.HeldAmountMinor
	s.accounts[card.cardID] = account
	result := AuthorizationSettlementResult{p.SettlementID, p.AuthorizationID, "settled", authorization.params.AmountMinor, account.PostedBalanceMinor, account.HeldAmountMinor, account.AvailableBalanceMinor}
	s.settlements[p.SettlementID] = memoryAuthorizationSettlement{p, result}
	return result, nil
}

func (s *MemoryFinanceStore) ReverseAuthorization(_ context.Context, p AuthorizationReversalParams) (AuthorizationReversalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.reversals[p.ReversalID]; ok {
		if existing.params != p {
			return AuthorizationReversalResult{}, ErrIdempotencyConflict
		}
		return existing.result, nil
	}
	authorization, ok := s.authorizations[p.AuthorizationID]
	if !ok || authorization.result.Decision != "approved" {
		return AuthorizationReversalResult{}, ErrAuthorizationNotReversible
	}
	for _, reversal := range s.reversals {
		if reversal.params.AuthorizationID == p.AuthorizationID {
			return AuthorizationReversalResult{}, ErrAuthorizationNotReversible
		}
	}
	card := s.providerCards[authorization.params.ProviderCardID]
	account := s.accounts[card.cardID]
	if account.HeldAmountMinor < authorization.params.AmountMinor {
		return AuthorizationReversalResult{}, ErrAuthorizationNotReversible
	}
	account.HeldAmountMinor -= authorization.params.AmountMinor
	account.AvailableBalanceMinor = account.PostedBalanceMinor - account.HeldAmountMinor
	s.accounts[card.cardID] = account
	result := AuthorizationReversalResult{p.ReversalID, p.AuthorizationID, "reversed", authorization.params.AmountMinor, account.PostedBalanceMinor, account.HeldAmountMinor, account.AvailableBalanceMinor}
	s.reversals[p.ReversalID] = memoryAuthorizationReversal{p, result}
	return result, nil
}

func (s *MemoryFinanceStore) SetProviderCard(providerCardID, cardID, status string) {
	s.providerCards[providerCardID] = memoryProviderCard{cardID, status}
}

func (s *MemoryFinanceStore) Fund(_ context.Context, p FundParams) (AccountBalance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.funding[p.EntryID]; ok {
		if existing.cardID != p.CardID || existing.currency != p.Currency || existing.amount != p.AmountMinor {
			return AccountBalance{}, ErrIdempotencyConflict
		}
		return s.accounts[p.CardID], nil
	}
	account := s.accounts[p.CardID]
	account.CardID, account.Currency = p.CardID, p.Currency
	account.PostedBalanceMinor += p.AmountMinor
	account.AvailableBalanceMinor = account.PostedBalanceMinor - account.HeldAmountMinor
	s.accounts[p.CardID] = account
	s.funding[p.EntryID] = memoryFunding{p.CardID, p.Currency, p.AmountMinor}
	return account, nil
}

func (s *MemoryFinanceStore) Authorize(_ context.Context, p AuthorizationParams) (AuthorizationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.authorizations[p.AuthorizationID]; ok {
		if existing.params != p {
			return AuthorizationResult{}, ErrIdempotencyConflict
		}
		return existing.result, nil
	}
	card, exists := s.providerCards[p.ProviderCardID]
	account := s.accounts[card.cardID]
	result := AuthorizationResult{AuthorizationID: p.AuthorizationID, Decision: "declined", PostedBalanceMinor: account.PostedBalanceMinor, HeldAmountMinor: account.HeldAmountMinor, AvailableBalanceMinor: account.AvailableBalanceMinor}
	switch {
	case !exists || card.status != "active":
		result.Reason = "card_not_active"
	case account.Currency != p.Currency:
		result.Reason = "currency_mismatch"
	case account.AvailableBalanceMinor < p.AmountMinor:
		result.Reason = "insufficient_funds"
	default:
		account.HeldAmountMinor += p.AmountMinor
		account.AvailableBalanceMinor = account.PostedBalanceMinor - account.HeldAmountMinor
		s.accounts[card.cardID] = account
		result.Decision, result.HeldAmountMinor, result.AvailableBalanceMinor = "approved", account.HeldAmountMinor, account.AvailableBalanceMinor
	}
	s.authorizations[p.AuthorizationID] = memoryAuthorization{p, result}
	return result, nil
}

type FinanceHandler struct{ store FinanceStore }

func NewFinanceHandler(store FinanceStore) http.Handler { return &FinanceHandler{store: store} }

func (h *FinanceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v3/card-lab/accounts/fund" {
		http.NotFound(w, r)
		return
	}
	var input struct {
		EntryID     string `json:"entry_id"`
		CardID      string `json:"card_id"`
		Currency    string `json:"currency"`
		AmountMinor int64  `json:"amount_minor"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.EntryID == "" || input.CardID == "" || input.Currency != "USD" || input.AmountMinor <= 0 {
		writeError(w, http.StatusBadRequest, "entry_id, card_id, USD currency and positive amount_minor are required")
		return
	}
	result, err := h.store.Fund(r.Context(), FundParams{input.EntryID, input.CardID, input.Currency, input.AmountMinor})
	if err == ErrIdempotencyConflict {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "finance store unavailable")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type AuthorizationHandler struct {
	store  AuthorizationStore
	secret []byte
	now    func() time.Time
}

func NewAuthorizationHandler(store AuthorizationStore, secret []byte, now func() time.Time) http.Handler {
	return &AuthorizationHandler{store, secret, now}
}
func (h *AuthorizationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v3/card-lab/provider-authorizations" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	id, timestampText := r.Header.Get("X-Provider-Event-Id"), r.Header.Get("X-Provider-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || id == "" || h.now().Sub(time.Unix(timestamp, 0)).Abs() > 5*time.Minute {
		writeError(w, 401, "invalid signature metadata")
		return
	}
	provided, err := hex.DecodeString(r.Header.Get("X-Provider-Signature"))
	mac := hmac.New(sha256.New, h.secret)
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, id, body)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		writeError(w, 401, "invalid signature")
		return
	}
	var input struct {
		AuthorizationID string `json:"authorization_id"`
		ProviderCardID  string `json:"provider_card_id"`
		Currency        string `json:"currency"`
		AmountMinor     int64  `json:"amount_minor"`
	}
	if json.Unmarshal(body, &input) != nil || input.AuthorizationID != id || input.ProviderCardID == "" || input.Currency != "USD" || input.AmountMinor <= 0 {
		writeError(w, 400, "invalid authorization")
		return
	}
	result, err := h.store.Authorize(r.Context(), AuthorizationParams{input.AuthorizationID, input.ProviderCardID, input.Currency, input.AmountMinor})
	if err == ErrIdempotencyConflict {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeError(w, 503, "authorization unavailable")
		return
	}
	writeJSON(w, 200, result)
}

type AuthorizationReversalHandler struct {
	store  AuthorizationReversalStore
	secret []byte
	now    func() time.Time
}

type AuthorizationSettlementHandler struct {
	store  AuthorizationSettlementStore
	secret []byte
	now    func() time.Time
}

type SettlementRefundHandler struct {
	store  SettlementRefundStore
	secret []byte
	now    func() time.Time
}

func NewSettlementRefundHandler(store SettlementRefundStore, secret []byte, now func() time.Time) http.Handler {
	return &SettlementRefundHandler{store, secret, now}
}

func (h *SettlementRefundHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v3/card-lab/provider-settlement-refunds" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	id, timestampText := r.Header.Get("X-Provider-Event-Id"), r.Header.Get("X-Provider-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || id == "" || h.now().Sub(time.Unix(timestamp, 0)).Abs() > 5*time.Minute {
		writeError(w, http.StatusUnauthorized, "invalid signature metadata")
		return
	}
	provided, err := hex.DecodeString(r.Header.Get("X-Provider-Signature"))
	mac := hmac.New(sha256.New, h.secret)
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, id, body)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	var input struct {
		RefundID     string `json:"refund_id"`
		SettlementID string `json:"settlement_id"`
	}
	if json.Unmarshal(body, &input) != nil || input.RefundID != id || input.SettlementID == "" {
		writeError(w, http.StatusBadRequest, "invalid settlement refund")
		return
	}
	result, err := h.store.RefundSettlement(r.Context(), SettlementRefundParams{input.RefundID, input.SettlementID})
	switch err {
	case nil:
		writeJSON(w, http.StatusOK, result)
	case ErrIdempotencyConflict, ErrSettlementNotRefundable:
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "settlement refund unavailable")
	}
}

func NewAuthorizationSettlementHandler(store AuthorizationSettlementStore, secret []byte, now func() time.Time) http.Handler {
	return &AuthorizationSettlementHandler{store, secret, now}
}

func (h *AuthorizationSettlementHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v3/card-lab/provider-authorization-settlements" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	id, timestampText := r.Header.Get("X-Provider-Event-Id"), r.Header.Get("X-Provider-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || id == "" || h.now().Sub(time.Unix(timestamp, 0)).Abs() > 5*time.Minute {
		writeError(w, http.StatusUnauthorized, "invalid signature metadata")
		return
	}
	provided, err := hex.DecodeString(r.Header.Get("X-Provider-Signature"))
	mac := hmac.New(sha256.New, h.secret)
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, id, body)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	var input struct {
		SettlementID    string `json:"settlement_id"`
		AuthorizationID string `json:"authorization_id"`
	}
	if json.Unmarshal(body, &input) != nil || input.SettlementID != id || input.AuthorizationID == "" {
		writeError(w, http.StatusBadRequest, "invalid authorization settlement")
		return
	}
	result, err := h.store.SettleAuthorization(r.Context(), AuthorizationSettlementParams{input.SettlementID, input.AuthorizationID})
	switch err {
	case nil:
		writeJSON(w, http.StatusOK, result)
	case ErrIdempotencyConflict, ErrAuthorizationNotSettleable:
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "authorization settlement unavailable")
	}
}

func NewAuthorizationReversalHandler(store AuthorizationReversalStore, secret []byte, now func() time.Time) http.Handler {
	return &AuthorizationReversalHandler{store, secret, now}
}

func (h *AuthorizationReversalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v3/card-lab/provider-authorization-reversals" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	id, timestampText := r.Header.Get("X-Provider-Event-Id"), r.Header.Get("X-Provider-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || id == "" || h.now().Sub(time.Unix(timestamp, 0)).Abs() > 5*time.Minute {
		writeError(w, http.StatusUnauthorized, "invalid signature metadata")
		return
	}
	provided, err := hex.DecodeString(r.Header.Get("X-Provider-Signature"))
	mac := hmac.New(sha256.New, h.secret)
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, id, body)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	var input struct {
		ReversalID      string `json:"reversal_id"`
		AuthorizationID string `json:"authorization_id"`
	}
	if json.Unmarshal(body, &input) != nil || input.ReversalID != id || input.AuthorizationID == "" {
		writeError(w, http.StatusBadRequest, "invalid authorization reversal")
		return
	}
	result, err := h.store.ReverseAuthorization(r.Context(), AuthorizationReversalParams{input.ReversalID, input.AuthorizationID})
	switch err {
	case nil:
		writeJSON(w, http.StatusOK, result)
	case ErrIdempotencyConflict:
		writeError(w, http.StatusConflict, err.Error())
	case ErrAuthorizationNotReversible:
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "authorization reversal unavailable")
	}
}
