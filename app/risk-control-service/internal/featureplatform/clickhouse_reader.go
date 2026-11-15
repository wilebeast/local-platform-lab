package featureplatform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	fundActivityFeatureSetKey = "subject_fund_activity"
	fundActivityWindowType    = "24h"
	fundActivityCurrentTable  = "risk_warehouse.dws_subject_fund_activity_current_v2"
	fundActivityHistoryTable  = "risk_warehouse.dws_subject_fund_activity_history_v2"
)

type ClickHouseFeatureReader struct {
	endpoint string
	user     string
	password string
	client   *http.Client
}

func NewClickHouseFeatureReader(
	endpoint string,
	user string,
	password string,
	client *http.Client,
) (*ClickHouseFeatureReader, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("ClickHouse endpoint is required")
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("valid ClickHouse endpoint is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &ClickHouseFeatureReader{
		endpoint: strings.TrimRight(endpoint, "/"),
		user:     user,
		password: password,
		client:   client,
	}, nil
}

func (r *ClickHouseFeatureReader) ReadLatestBefore(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	if err := validateFundActivityRead(request, false); err != nil {
		return nil, err
	}
	current, err := r.queryFundActivity(ctx, fundActivityCurrentTable, request.Subjects, request, false)
	if err != nil {
		return nil, fmt.Errorf("query current feature projection: %w", err)
	}
	bySubject := indexSnapshots(current)
	missing := make([]Subject, 0)
	for _, subject := range request.Subjects {
		if _, exists := bySubject[subjectIdentity(subject)]; !exists {
			missing = append(missing, subject)
		}
	}
	if len(missing) > 0 {
		history, err := r.queryFundActivity(ctx, fundActivityHistoryTable, missing, request, false)
		if err != nil {
			return nil, fmt.Errorf("query historical feature fallback: %w", err)
		}
		for _, snapshot := range history {
			bySubject[subjectIdentity(snapshot.Subject)] = snapshot
		}
	}
	return orderSnapshots(request.Subjects, bySubject), nil
}

func (r *ClickHouseFeatureReader) ReadAt(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	if err := validateFundActivityRead(request, true); err != nil {
		return nil, err
	}
	snapshots, err := r.queryFundActivity(ctx, fundActivityHistoryTable, request.Subjects, request, true)
	if err != nil {
		return nil, fmt.Errorf("query exact historical features: %w", err)
	}
	return orderSnapshots(request.Subjects, indexSnapshots(snapshots)), nil
}

type fundActivityRow struct {
	FeatureSetKey       string  `json:"feature_set_key"`
	FeatureSetVersion   int     `json:"feature_set_version"`
	WindowType          string  `json:"window_type"`
	SubjectType         string  `json:"subject_type"`
	SubjectID           string  `json:"subject_id"`
	Currency            string  `json:"currency"`
	WindowStartMillis   int64   `json:"window_start_millis"`
	WindowEndMillis     int64   `json:"window_end_millis"`
	AsOfMillis          int64   `json:"as_of_millis"`
	InputCutoffMillis   int64   `json:"input_cutoff_millis"`
	InflowMinor         int64   `json:"inflow_minor"`
	OutflowMinor        int64   `json:"outflow_minor"`
	OutflowRatio        float64 `json:"outflow_ratio"`
	OutflowRatioDefined uint8   `json:"outflow_ratio_defined"`
	SourceEventCount    uint64  `json:"source_event_count"`
	SourceRunID         string  `json:"source_run_id"`
	ContentFingerprint  string  `json:"content_fingerprint"`
	QualityStatus       string  `json:"quality_status"`
	ComputedAtMillis    int64   `json:"computed_at_millis"`
	IngestedAtMillis    int64   `json:"ingested_at_millis"`
}

func (r *ClickHouseFeatureReader) queryFundActivity(
	ctx context.Context,
	table string,
	subjects []Subject,
	request SnapshotReadRequest,
	exact bool,
) ([]FeatureSnapshot, error) {
	timeOperator := "<="
	timeMillis := request.Cutoff.UTC().UnixMilli()
	if exact {
		timeOperator = "="
		timeMillis = request.AsOf.UTC().UnixMilli()
	}
	query := fmt.Sprintf(`SELECT
feature_set_key, feature_set_version, window_type, subject_type, subject_id, currency,
window_start_millis, window_end_millis, as_of_millis, input_cutoff_millis,
inflow_minor, outflow_minor, outflow_ratio, outflow_ratio_defined,
source_event_count, source_run_id, content_fingerprint, quality_status,
computed_at_millis, ingested_at_millis
FROM %s FINAL
WHERE feature_set_key = '%s'
  AND feature_set_version = %d
  AND window_type = '%s'
  AND as_of_millis %s %d
  AND (subject_type, subject_id, currency) IN (%s)
ORDER BY subject_type, subject_id, currency, as_of_millis DESC, computed_at_millis DESC
LIMIT 1 BY subject_type, subject_id, currency
FORMAT JSONEachRow`,
		table,
		clickHouseLiteral(request.FeatureSetKey),
		request.FeatureSetVersion,
		fundActivityWindowType,
		timeOperator,
		timeMillis,
		clickHouseSubjectTuples(subjects),
	)
	body, err := r.query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	decoder := json.NewDecoder(body)
	snapshots := make([]FeatureSnapshot, 0, len(subjects))
	for {
		var row fundActivityRow
		if err := decoder.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse feature snapshot: %w", err)
		}
		snapshot := FeatureSnapshot{
			FeatureSetKey:     row.FeatureSetKey,
			FeatureSetVersion: row.FeatureSetVersion,
			Subject: Subject{Type: row.SubjectType, Keys: map[string]string{
				"subject_id": row.SubjectID,
				"currency":   row.Currency,
			}},
			AsOf:               time.UnixMilli(row.AsOfMillis).UTC(),
			ComputedAt:         time.UnixMilli(row.ComputedAtMillis).UTC(),
			SourceRunID:        row.SourceRunID,
			ContentFingerprint: row.ContentFingerprint,
			QualityStatus:      row.QualityStatus,
			Values: map[string]any{
				"inflow_minor":  row.InflowMinor,
				"outflow_minor": row.OutflowMinor,
			},
			Undefined: make(map[string]bool),
		}
		if row.OutflowRatioDefined == 1 {
			snapshot.Values["outflow_ratio"] = row.OutflowRatio
		} else {
			snapshot.Undefined["outflow_ratio"] = true
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (r *ClickHouseFeatureReader) query(ctx context.Context, query string) (io.ReadCloser, error) {
	requestURL := r.endpoint + "/?query=" + url.QueryEscape(query)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return nil, err
	}
	if r.user != "" {
		request.SetBasicAuth(r.user, r.password)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.Body, nil
	}
	defer response.Body.Close()
	message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	return nil, fmt.Errorf("ClickHouse query failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
}

func validateFundActivityRead(request SnapshotReadRequest, exact bool) error {
	if request.FeatureSetKey != fundActivityFeatureSetKey || request.FeatureSetVersion <= 0 || len(request.FeatureKeys) == 0 || len(request.Subjects) == 0 {
		return fmt.Errorf("supported feature set version, feature keys and subjects are required")
	}
	if exact && request.AsOf.IsZero() {
		return fmt.Errorf("as_of is required")
	}
	if !exact && request.Cutoff.IsZero() {
		return fmt.Errorf("cutoff is required")
	}
	supported := map[string]struct{}{"inflow_minor": {}, "outflow_minor": {}, "outflow_ratio": {}}
	for _, key := range request.FeatureKeys {
		if _, exists := supported[key]; !exists {
			return fmt.Errorf("feature %q is not supported by the fund activity adapter", key)
		}
	}
	for _, subject := range request.Subjects {
		if subject.Type == "" || subject.Keys["subject_id"] == "" || subject.Keys["currency"] == "" {
			return fmt.Errorf("fund activity subject_type, subject_id and currency are required")
		}
	}
	return nil
}

func clickHouseSubjectTuples(subjects []Subject) string {
	tuples := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		tuples = append(tuples, fmt.Sprintf("('%s','%s','%s')",
			clickHouseLiteral(subject.Type),
			clickHouseLiteral(subject.Keys["subject_id"]),
			clickHouseLiteral(subject.Keys["currency"]),
		))
	}
	return strings.Join(tuples, ",")
}

func clickHouseLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

func indexSnapshots(snapshots []FeatureSnapshot) map[string]FeatureSnapshot {
	index := make(map[string]FeatureSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		index[subjectIdentity(snapshot.Subject)] = snapshot
	}
	return index
}

func orderSnapshots(subjects []Subject, snapshots map[string]FeatureSnapshot) []FeatureSnapshot {
	ordered := make([]FeatureSnapshot, 0, len(snapshots))
	for _, subject := range subjects {
		if snapshot, exists := snapshots[subjectIdentity(subject)]; exists {
			ordered = append(ordered, snapshot)
		}
	}
	return ordered
}
