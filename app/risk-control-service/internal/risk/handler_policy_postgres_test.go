package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPolicyDecisionHTTPRunsPublishedPostgresPolicy(t *testing.T) {
	ctx := context.Background()
	pool := newPolicyPublisherTestPool(t, ctx)
	insertPolicyPublisherFixture(
		t, ctx, pool,
		"withdrawal_risk", "draft",
		"inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90",
	)
	store := NewPostgresStore(pool)
	adapters := NewRPCFactorAdapterRegistry()
	publication, err := NewPolicyPublisher(store, adapters).Publish(ctx, "withdrawal_risk", 1)
	if err != nil {
		t.Fatal(err)
	}
	if publication.ContentFingerprint == "" {
		t.Fatal("published policy must have a content fingerprint")
	}
	runtime, err := LoadPolicySnapshotRuntime(
		ctx,
		NewPolicySnapshotLoader(store, adapters),
		"withdrawal_risk",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithRealtimeDecider(&fakeStore{}, NewRealtimeDecisionService(pool, runtime))

	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/policy-decisions",
		bytes.NewBufferString(`{"evaluation_id":"decision-pg-1","subject_type":"organization","subject_id":"org-risky","params":{"inflow_minor_24h":100000,"outflow_minor_24h":95000}}`),
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var got PolicyEvaluation
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || got.PolicyVersion != 1 || got.Decision != "block" {
		t.Fatalf("status=%d evaluation=%+v", w.Code, got)
	}
	if got.MatchedStrategyKey != "block_rapid_outflow" || len(got.Actions) != 1 || got.Actions[0].ActionCode != "activate_restriction" {
		t.Fatalf("missing published strategy trace or action: %+v", got)
	}
}
