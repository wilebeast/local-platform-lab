package main

import (
	"testing"
	"time"
)

func TestLoadHealthMonitorConfigRequiresExplicitAsOfAndBuildsDeterministicWindow(t *testing.T) {
	values := map[string]string{
		"POSTGRES_DSN":                              "postgres://example",
		"FEATURE_SET_KEY":                           "subject_fund_activity",
		"FEATURE_SET_VERSION":                       "2",
		"FEATURE_HEALTH_CHECK_AS_OF":                "2026-11-15T16:00:00Z",
		"FEATURE_HEALTH_EVIDENCE_WINDOW":            "15m",
		"FEATURE_HEALTH_BUCKET_DURATION":            "1m",
		"FEATURE_HEALTH_MIN_COMPARISONS":            "100",
		"FEATURE_HEALTH_MIN_BUCKETS":                "10",
		"FEATURE_HEALTH_MAX_MISMATCH_RATE":          "0.01",
		"FEATURE_HEALTH_MAX_UNAVAILABLE_RATE":       "0.02",
		"FEATURE_HEALTH_MAX_FRESHNESS_LAG":          "1m",
		"FEATURE_HEALTH_MAX_P95_CANDIDATE_DURATION": "20ms",
	}
	config, err := loadHealthMonitorConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if !config.request.EvidenceStart.Equal(time.Date(2026, 11, 15, 15, 45, 0, 0, time.UTC)) ||
		!config.request.EvidenceEnd.Equal(time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("evidence window=%s..%s", config.request.EvidenceStart, config.request.EvidenceEnd)
	}
	if config.request.CheckID != "subject_fund_activity-v2-20261115T160000Z" {
		t.Fatalf("check ID=%q", config.request.CheckID)
	}

	delete(values, "FEATURE_HEALTH_CHECK_AS_OF")
	if _, err := loadHealthMonitorConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected missing explicit as_of to be rejected")
	}
}
