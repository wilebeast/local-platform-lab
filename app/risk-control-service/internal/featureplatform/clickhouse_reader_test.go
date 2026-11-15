package featureplatform

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClickHouseFeatureReaderUsesCurrentThenHistoryForMissingSubjects(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 12, 10, 0, 0, time.UTC)
	var mu sync.Mutex
	queries := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query, _ := url.QueryUnescape(request.URL.Query().Get("query"))
		mu.Lock()
		queries = append(queries, query)
		mu.Unlock()
		response.Header().Set("Content-Type", "application/x-ndjson")
		switch {
		case strings.Contains(query, "dws_subject_fund_activity_current_v2"):
			fmt.Fprintln(response, fundActivityRowJSON("org-current", cutoff.Add(-time.Minute), 100_000, 95_000, "current-run"))
		case strings.Contains(query, "dws_subject_fund_activity_history_v2"):
			fmt.Fprintln(response, fundActivityRowJSON("org-history", cutoff.Add(-2*time.Minute), 80_000, 20_000, "history-run"))
		default:
			http.Error(response, "unexpected query", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	reader, err := NewClickHouseFeatureReader(server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := reader.ReadLatestBefore(context.Background(), SnapshotReadRequest{
		FeatureSetKey:     "subject_fund_activity",
		FeatureSetVersion: 1,
		FeatureKeys:       []string{"inflow_minor", "outflow_minor", "outflow_ratio"},
		Subjects: []Subject{
			{Type: "organization", Keys: map[string]string{"subject_id": "org-current", "currency": "USD"}},
			{Type: "organization", Keys: map[string]string{"subject_id": "org-history", "currency": "USD"}},
		},
		Cutoff: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshots=%d, want 2: %+v", len(snapshots), snapshots)
	}
	if snapshots[0].SourceRunID != "current-run" || snapshots[1].SourceRunID != "history-run" {
		t.Fatalf("unexpected snapshot sources: %+v", snapshots)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 2 || !strings.Contains(queries[0], "as_of_millis <=") || !strings.Contains(queries[1], "org-history") {
		t.Fatalf("unexpected ClickHouse queries: %#v", queries)
	}
	if strings.Contains(queries[1], "org-current") {
		t.Fatalf("history fallback should only query missing subjects: %s", queries[1])
	}
}

func fundActivityRowJSON(subjectID string, asOf time.Time, inflow int64, outflow int64, runID string) string {
	ratio := float64(outflow) / float64(inflow)
	return fmt.Sprintf(`{"feature_set_key":"subject_fund_activity","feature_set_version":1,"window_type":"24h","subject_type":"organization","subject_id":%q,"currency":"USD","window_start_millis":%d,"window_end_millis":%d,"as_of_millis":%d,"input_cutoff_millis":%d,"inflow_minor":%d,"outflow_minor":%d,"outflow_ratio":%f,"outflow_ratio_defined":1,"source_event_count":2,"source_run_id":%q,"content_fingerprint":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","quality_status":"passed","computed_at_millis":%d,"ingested_at_millis":%d}`,
		subjectID,
		asOf.Add(-24*time.Hour).UnixMilli(),
		asOf.UnixMilli(),
		asOf.UnixMilli(),
		asOf.UnixMilli(),
		inflow,
		outflow,
		ratio,
		runID,
		asOf.Add(time.Second).UnixMilli(),
		asOf.Add(2*time.Second).UnixMilli(),
	)
}
