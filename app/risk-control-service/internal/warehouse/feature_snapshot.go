package warehouse

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	fundActivityFeatureSetKey     = "subject_fund_activity_24h"
	fundActivityFeatureSetVersion = int32(1)
)

type SubjectFundActivitySnapshot struct {
	FeatureSetKey       string  `json:"feature_set_key"`
	FeatureSetVersion   int32   `json:"feature_set_version"`
	SubjectType         string  `json:"subject_type"`
	SubjectID           string  `json:"subject_id"`
	Currency            string  `json:"currency"`
	WindowStartMillis   int64   `json:"window_start_millis"`
	WindowEndMillis     int64   `json:"window_end_millis"`
	AsOfMillis          int64   `json:"as_of_millis"`
	InflowMinor         int64   `json:"inflow_minor"`
	OutflowMinor        int64   `json:"outflow_minor"`
	OutflowRatio        float64 `json:"outflow_ratio"`
	OutflowRatioDefined bool    `json:"outflow_ratio_defined"`
	SourceEventCount    int64   `json:"source_event_count"`
	SourceRunID         string  `json:"source_run_id"`
	ContentFingerprint  string  `json:"content_fingerprint"`
	ComputedAtMillis    int64   `json:"computed_at_millis"`
}

type FundActivityAggregator struct {
	window time.Duration
}

func NewFundActivityAggregator(window time.Duration) *FundActivityAggregator {
	return &FundActivityAggregator{window: window}
}

func (a *FundActivityAggregator) Aggregate(
	_ context.Context,
	asOf time.Time,
	facts []FundMovementFact,
) ([]SubjectFundActivitySnapshot, error) {
	if a.window <= 0 {
		return nil, fmt.Errorf("aggregation window must be positive")
	}
	if asOf.IsZero() {
		return nil, fmt.Errorf("as_of is required")
	}
	asOf = asOf.UTC()
	windowStart := asOf.Add(-a.window)

	type identity struct {
		subjectType string
		subjectID   string
		currency    string
	}
	aggregates := make(map[identity]*SubjectFundActivitySnapshot)
	for _, fact := range facts {
		occurredAt := time.UnixMilli(fact.OccurredAtMillis).UTC()
		if !occurredAt.After(windowStart) || occurredAt.After(asOf) {
			continue
		}
		key := identity{fact.SubjectType, fact.SubjectID, fact.Currency}
		snapshot := aggregates[key]
		if snapshot == nil {
			snapshot = &SubjectFundActivitySnapshot{
				FeatureSetKey:     fundActivityFeatureSetKey,
				FeatureSetVersion: fundActivityFeatureSetVersion,
				SubjectType:       fact.SubjectType,
				SubjectID:         fact.SubjectID,
				Currency:          fact.Currency,
				WindowStartMillis: windowStart.UnixMilli(),
				WindowEndMillis:   asOf.UnixMilli(),
				AsOfMillis:        asOf.UnixMilli(),
			}
			aggregates[key] = snapshot
		}
		switch fact.Direction {
		case "inflow":
			snapshot.InflowMinor += fact.AmountMinor
		case "outflow":
			snapshot.OutflowMinor += fact.AmountMinor
		default:
			return nil, fmt.Errorf("fact %q has unsupported direction %q", fact.EventID, fact.Direction)
		}
		snapshot.SourceEventCount++
	}

	result := make([]SubjectFundActivitySnapshot, 0, len(aggregates))
	for _, snapshot := range aggregates {
		if snapshot.InflowMinor > 0 {
			snapshot.OutflowRatio = float64(snapshot.OutflowMinor) / float64(snapshot.InflowMinor)
			snapshot.OutflowRatioDefined = true
		}
		result = append(result, *snapshot)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SubjectType != result[j].SubjectType {
			return result[i].SubjectType < result[j].SubjectType
		}
		if result[i].SubjectID != result[j].SubjectID {
			return result[i].SubjectID < result[j].SubjectID
		}
		return result[i].Currency < result[j].Currency
	})
	return result, nil
}
