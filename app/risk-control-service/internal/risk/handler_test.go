package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeStore struct {
	facts      CustomerFacts
	evaluation Evaluation
	replay     bool
	err        error
}

func (s *fakeStore) UpsertFacts(_ context.Context, f CustomerFacts) (CustomerFacts, error) {
	s.facts = f
	s.facts.FactVersion = 1
	return s.facts, s.err
}
func (s *fakeStore) Evaluate(_ context.Context, c EvaluationCommand) (Evaluation, bool, error) {
	return s.evaluation, s.replay, s.err
}
func (s *fakeStore) GetEvaluation(_ context.Context, id string) (Evaluation, error) {
	return s.evaluation, s.err
}
func (s *fakeStore) CompleteAction(_ context.Context, id string, c CompleteActionCommand) (Action, error) {
	return Action{ActionID: id, Status: "completed"}, s.err
}

func TestPublicHTTPContract(t *testing.T) {
	store := &fakeStore{evaluation: Evaluation{EvaluationID: "eval-1", Decision: "review", Actions: []Action{{ActionID: "action-eval-1-provide-poa", ActionType: "provide_poa", Status: "open"}}, EvaluatedAt: time.Unix(1, 0)}}
	h := NewHandler(store)
	t.Run("records trusted customer facts", func(t *testing.T) {
		body := `{"verification_status":"approved","residence_country":"SG","poa_verified":false}`
		r := httptest.NewRequest(http.MethodPut, "/api/v1/customers/customer-1/facts", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || store.facts.CustomerID != "customer-1" {
			t.Fatalf("status=%d facts=%+v", w.Code, store.facts)
		}
	})
	t.Run("returns review with a durable action", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/evaluations", bytes.NewBufferString(`{"evaluation_id":"eval-1","customer_id":"customer-1","product_code":"business_card"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got Evaluation
		_ = json.NewDecoder(w.Body).Decode(&got)
		if w.Code != 200 || got.Decision != "review" || len(got.Actions) != 1 {
			t.Fatalf("status=%d got=%+v", w.Code, got)
		}
	})
	t.Run("marks exact replay", func(t *testing.T) {
		store.replay = true
		r := httptest.NewRequest(http.MethodPost, "/api/v1/evaluations", bytes.NewBufferString(`{"evaluation_id":"eval-1","customer_id":"customer-1","product_code":"business_card"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Idempotent-Replay") != "true" {
			t.Fatalf("missing replay header")
		}
	})
}
