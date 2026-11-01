package warehouse_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestClickHouseReadsFundFactsWithPointInTimeBoundary(t *testing.T) {
	var query string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query, _ = url.QueryUnescape(request.URL.Query().Get("query"))
		return httpResponse(`{"event_id":"evt_001","event_version":1,"occurred_at_millis":1793527200000,"produced_at_millis":1793527201000,"ingested_at_millis":1793527202000,"subject_type":"organization","subject_id":"org_001","direction":"inflow","currency":"USD","amount_minor":100000,"source_object_key":"raw/evt_001.json","source_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}` + "\n"), nil
	})}

	client, err := warehouse.NewClickHouse("http://clickhouse.test", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	facts, err := client.ReadFundMovementFacts(context.Background(), asOf.Add(-24*time.Hour), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].EventID != "evt_001" || facts[0].AmountMinor != 100000 {
		t.Fatalf("unexpected facts: %+v", facts)
	}
	if !strings.Contains(query, "occurred_at_millis > 1793448000000") || !strings.Contains(query, "occurred_at_millis <= 1793534400000") {
		t.Fatalf("query does not enforce point-in-time window: %s", query)
	}
}

func TestClickHouseWritesVersionedFeatureSnapshots(t *testing.T) {
	var queries []string
	var inserted []warehouse.SubjectFundActivitySnapshot
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(request.URL.Query().Get("query"))
		queries = append(queries, query)
		if strings.HasPrefix(query, "INSERT INTO risk_warehouse.subject_fund_activity_24h") {
			scanner := bufio.NewScanner(request.Body)
			for scanner.Scan() {
				var snapshot warehouse.SubjectFundActivitySnapshot
				if err := json.Unmarshal(scanner.Bytes(), &snapshot); err != nil {
					t.Fatal(err)
				}
				inserted = append(inserted, snapshot)
			}
		}
		return httpResponse(""), nil
	})}

	client, err := warehouse.NewClickHouse("http://clickhouse.test", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_001", Currency: "USD",
		AsOfMillis: 1793534400000, InflowMinor: 100000, OutflowMinor: 95000, OutflowRatio: 0.95,
		OutflowRatioDefined: true,
		SourceRunID:         "run_001", ComputedAtMillis: 1793534460000,
	}
	if err := client.SaveFundActivitySnapshots(context.Background(), []warehouse.SubjectFundActivitySnapshot{snapshot}); err != nil {
		t.Fatal(err)
	}
	if len(inserted) != 2 || inserted[0].SourceRunID != "run_001" || inserted[1].SourceRunID != "run_001" || inserted[0].OutflowRatio != 0.95 {
		t.Fatalf("unexpected inserted snapshots: %+v", inserted)
	}
	if len(queries) != 10 || !strings.Contains(queries[0], "_history") || !strings.Contains(queries[1], "CREATE TABLE IF NOT EXISTS") || !strings.Contains(queries[2], "ADD COLUMN IF NOT EXISTS") || !strings.Contains(queries[3], "content_fingerprint") || !strings.Contains(queries[4], "content_fingerprint") || !strings.Contains(queries[5], "SELECT content_fingerprint") || !strings.Contains(queries[6], "_history FORMAT JSONEachRow") || !strings.Contains(queries[7], "subject_fund_activity_24h FORMAT JSONEachRow") || !strings.Contains(queries[8], "OPTIMIZE TABLE") || !strings.Contains(queries[9], "OPTIMIZE TABLE") {
		t.Fatalf("unexpected queries: %+v", queries)
	}
}

func TestClickHousePreservesEverySnapshotRunInImmutableHistory(t *testing.T) {
	var historyRuns []string
	var currentRuns []string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(request.URL.Query().Get("query"))
		if strings.HasPrefix(query, "INSERT INTO risk_warehouse.subject_fund_activity_24h_history") ||
			strings.HasPrefix(query, "INSERT INTO risk_warehouse.subject_fund_activity_24h FORMAT") {
			var snapshot warehouse.SubjectFundActivitySnapshot
			if err := json.NewDecoder(request.Body).Decode(&snapshot); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(query, "_history") {
				historyRuns = append(historyRuns, snapshot.SourceRunID)
			} else {
				currentRuns = append(currentRuns, snapshot.SourceRunID)
			}
		}
		return httpResponse(""), nil
	})}
	client, err := warehouse.NewClickHouse("http://clickhouse.test", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	base := warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_001", Currency: "USD",
		AsOfMillis: 1793534400000, InflowMinor: 100000, OutflowMinor: 95000,
	}
	first := base
	first.SourceRunID, first.ComputedAtMillis = "run-001", 1793534460000
	second := base
	second.SourceRunID, second.ComputedAtMillis = "run-002", 1793534520000

	if err := client.SaveFundActivitySnapshots(context.Background(), []warehouse.SubjectFundActivitySnapshot{first}); err != nil {
		t.Fatal(err)
	}
	if err := client.SaveFundActivitySnapshots(context.Background(), []warehouse.SubjectFundActivitySnapshot{second}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(historyRuns, ",") != "run-001,run-002" {
		t.Fatalf("history runs = %v", historyRuns)
	}
	if strings.Join(currentRuns, ",") != "run-001,run-002" {
		t.Fatalf("current runs = %v", currentRuns)
	}
}

func TestClickHouseRejectsChangedContentForExistingSourceRun(t *testing.T) {
	var storedFingerprint string
	var historyWrites int
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query, _ := url.QueryUnescape(request.URL.Query().Get("query"))
		if strings.HasPrefix(query, "SELECT content_fingerprint") {
			if storedFingerprint == "" {
				return httpResponse(""), nil
			}
			return httpResponse(storedFingerprint + "\n"), nil
		}
		if strings.HasPrefix(query, "INSERT INTO risk_warehouse.subject_fund_activity_24h_history") {
			var snapshot warehouse.SubjectFundActivitySnapshot
			if err := json.NewDecoder(request.Body).Decode(&snapshot); err != nil {
				t.Fatal(err)
			}
			storedFingerprint = snapshot.ContentFingerprint
			historyWrites++
		}
		return httpResponse(""), nil
	})}
	client, err := warehouse.NewClickHouse("http://clickhouse.test", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	original := warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_001", Currency: "USD",
		AsOfMillis: 1793534400000, InflowMinor: 100000, OutflowMinor: 95000,
		SourceRunID: "run-conflict-contract", ComputedAtMillis: 1793534460000,
	}
	if err := client.SaveFundActivitySnapshots(context.Background(), []warehouse.SubjectFundActivitySnapshot{original}); err != nil {
		t.Fatal(err)
	}
	if storedFingerprint == "" {
		t.Fatal("content fingerprint was not written")
	}
	if err := client.SaveFundActivitySnapshots(context.Background(), []warehouse.SubjectFundActivitySnapshot{original}); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	tampered := original
	tampered.InflowMinor = 200000
	err = client.SaveFundActivitySnapshots(context.Background(), []warehouse.SubjectFundActivitySnapshot{tampered})
	if err == nil || !strings.Contains(err.Error(), "source_run_id conflict") {
		t.Fatalf("conflicting replay error = %v", err)
	}
	if historyWrites != 2 {
		t.Fatalf("history writes = %d, want first write plus identical replay", historyWrites)
	}
}

func TestClickHouseReadsLatestFeatureSnapshotNotNewerThanAsOf(t *testing.T) {
	var query string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query, _ = url.QueryUnescape(request.URL.Query().Get("query"))
		return httpResponse(`{"feature_set_key":"subject_fund_activity_24h","feature_set_version":1,"subject_type":"organization","subject_id":"org_001","currency":"USD","window_start_millis":1793448000000,"window_end_millis":1793534400000,"as_of_millis":1793534400000,"inflow_minor":100000,"outflow_minor":95000,"outflow_ratio":0.95,"outflow_ratio_defined":true,"source_event_count":2,"source_run_id":"run_001","computed_at_millis":1793534460000}` + "\n"), nil
	})}
	client, err := warehouse.NewClickHouse("http://clickhouse.test", "", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	snapshot, found, err := client.ReadLatestFundActivitySnapshot(
		context.Background(), 1, "organization", "org_001", "USD", asOf,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found || snapshot.OutflowRatio != 0.95 || !snapshot.OutflowRatioDefined {
		t.Fatalf("unexpected snapshot: found=%t snapshot=%+v", found, snapshot)
	}
	for _, fragment := range []string{
		"feature_set_version = 1", "subject_id = 'org_001'", "as_of_millis <= 1793534400000",
		"ORDER BY as_of_millis DESC", "LIMIT 1",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("query missing %q: %s", fragment, query)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func httpResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}
