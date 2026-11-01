package warehouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const clickHouseDatabaseDDL = `CREATE DATABASE IF NOT EXISTS risk_warehouse`

const clickHouseFactTableDDL = `CREATE TABLE IF NOT EXISTS risk_warehouse.fact_fund_movements
(
    event_id String,
    event_version Int32,
    occurred_at_millis Int64,
    produced_at_millis Int64,
    ingested_at_millis Int64,
    subject_type LowCardinality(String),
    subject_id String,
    direction Enum8('inflow' = 1, 'outflow' = 2),
    currency FixedString(3),
    amount_minor Int64,
    source_object_key String,
    source_sha256 FixedString(64)
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;
`

const clickHouseFundActivityTableDDL = `CREATE TABLE IF NOT EXISTS risk_warehouse.subject_fund_activity_24h
(
    feature_set_key LowCardinality(String),
    feature_set_version Int32,
    subject_type LowCardinality(String),
    subject_id String,
    currency FixedString(3),
    window_start_millis Int64,
    window_end_millis Int64,
    as_of_millis Int64,
    inflow_minor Int64,
    outflow_minor Int64,
    outflow_ratio Float64,
    outflow_ratio_defined Bool,
    source_event_count Int64,
    source_run_id String,
    content_fingerprint FixedString(64),
    computed_at_millis Int64
)
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, subject_type, subject_id, currency, as_of_millis);
`

const clickHouseFundActivityHistoryTableDDL = `CREATE TABLE IF NOT EXISTS risk_warehouse.subject_fund_activity_24h_history
(
    feature_set_key LowCardinality(String),
    feature_set_version Int32,
    subject_type LowCardinality(String),
    subject_id String,
    currency FixedString(3),
    window_start_millis Int64,
    window_end_millis Int64,
    as_of_millis Int64,
    inflow_minor Int64,
    outflow_minor Int64,
    outflow_ratio Float64,
    outflow_ratio_defined Bool,
    source_event_count Int64,
    source_run_id String,
    content_fingerprint FixedString(64),
    computed_at_millis Int64
)
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, subject_type, subject_id, currency, as_of_millis, source_run_id);
`

const clickHouseFundActivityRatioDefinedDDL = `ALTER TABLE risk_warehouse.subject_fund_activity_24h
ADD COLUMN IF NOT EXISTS outflow_ratio_defined Bool AFTER outflow_ratio`

const clickHouseFundActivityFingerprintDDL = `ALTER TABLE risk_warehouse.subject_fund_activity_24h
ADD COLUMN IF NOT EXISTS content_fingerprint FixedString(64) DEFAULT '' AFTER source_run_id`

const clickHouseFundActivityHistoryFingerprintDDL = `ALTER TABLE risk_warehouse.subject_fund_activity_24h_history
ADD COLUMN IF NOT EXISTS content_fingerprint FixedString(64) DEFAULT '' AFTER source_run_id`

type ClickHouse struct {
	endpoint string
	user     string
	password string
	client   *http.Client
}

func NewClickHouse(endpoint, user, password string, client *http.Client) (*ClickHouse, error) {
	if _, err := url.ParseRequestURI(endpoint); err != nil || endpoint == "" {
		return nil, fmt.Errorf("valid ClickHouse endpoint is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &ClickHouse{endpoint: strings.TrimRight(endpoint, "/"), user: user, password: password, client: client}, nil
}

func (c *ClickHouse) EnsureSchema(ctx context.Context) error {
	if err := c.execute(ctx, clickHouseDatabaseDDL, nil, "text/plain"); err != nil {
		return err
	}
	return c.execute(ctx, clickHouseFactTableDDL, nil, "text/plain")
}

func (c *ClickHouse) LoadFundMovementParquet(ctx context.Context, parquetBytes []byte) error {
	if len(parquetBytes) == 0 {
		return fmt.Errorf("Parquet content is empty")
	}
	if err := c.execute(
		ctx,
		"INSERT INTO risk_warehouse.fact_fund_movements FORMAT Parquet",
		parquetBytes,
		"application/octet-stream",
	); err != nil {
		return err
	}
	return c.execute(ctx, "OPTIMIZE TABLE risk_warehouse.fact_fund_movements FINAL", nil, "text/plain")
}

func (c *ClickHouse) ReadFundMovementFacts(ctx context.Context, windowStart, asOf time.Time) ([]FundMovementFact, error) {
	query := fmt.Sprintf(`SELECT
event_id, event_version, occurred_at_millis, produced_at_millis, ingested_at_millis,
subject_type, subject_id, direction, currency, amount_minor, source_object_key, source_sha256
FROM risk_warehouse.fact_fund_movements FINAL
WHERE occurred_at_millis > %d AND occurred_at_millis <= %d
ORDER BY event_id FORMAT JSONEachRow`, windowStart.UTC().UnixMilli(), asOf.UTC().UnixMilli())
	body, err := c.query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	decoder := json.NewDecoder(body)
	rows := make([]FundMovementFact, 0)
	for {
		var row FundMovementFact
		if err := decoder.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse fund movement: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (c *ClickHouse) SaveFundActivitySnapshots(ctx context.Context, snapshots []SubjectFundActivitySnapshot) error {
	if err := c.execute(ctx, clickHouseFundActivityHistoryTableDDL, nil, "text/plain"); err != nil {
		return err
	}
	if err := c.execute(ctx, clickHouseFundActivityTableDDL, nil, "text/plain"); err != nil {
		return err
	}
	if err := c.execute(ctx, clickHouseFundActivityRatioDefinedDDL, nil, "text/plain"); err != nil {
		return err
	}
	if err := c.execute(ctx, clickHouseFundActivityHistoryFingerprintDDL, nil, "text/plain"); err != nil {
		return err
	}
	if err := c.execute(ctx, clickHouseFundActivityFingerprintDDL, nil, "text/plain"); err != nil {
		return err
	}
	if len(snapshots) == 0 {
		return nil
	}
	prepared, fingerprint, sourceRunID, err := prepareFundActivitySnapshots(snapshots)
	if err != nil {
		return err
	}
	existingFingerprint, err := c.readFundActivityRunFingerprint(ctx, sourceRunID)
	if err != nil {
		return err
	}
	if existingFingerprint != "" && existingFingerprint != fingerprint {
		return fmt.Errorf("feature snapshot source_run_id conflict: %s", sourceRunID)
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, snapshot := range prepared {
		if err := encoder.Encode(snapshot); err != nil {
			return fmt.Errorf("encode fund activity snapshot: %w", err)
		}
	}
	historyBytes := append([]byte(nil), body.Bytes()...)
	if err := c.execute(
		ctx,
		"INSERT INTO risk_warehouse.subject_fund_activity_24h_history FORMAT JSONEachRow",
		historyBytes,
		"application/x-ndjson",
	); err != nil {
		return err
	}
	if err := c.execute(
		ctx,
		"INSERT INTO risk_warehouse.subject_fund_activity_24h FORMAT JSONEachRow",
		body.Bytes(),
		"application/x-ndjson",
	); err != nil {
		return err
	}
	if err := c.execute(ctx, "OPTIMIZE TABLE risk_warehouse.subject_fund_activity_24h_history FINAL", nil, "text/plain"); err != nil {
		return err
	}
	return c.execute(ctx, "OPTIMIZE TABLE risk_warehouse.subject_fund_activity_24h FINAL", nil, "text/plain")
}

func (c *ClickHouse) ReadLatestFundActivitySnapshot(
	ctx context.Context,
	featureSetVersion int32,
	subjectType string,
	subjectID string,
	currency string,
	asOf time.Time,
) (SubjectFundActivitySnapshot, bool, error) {
	if featureSetVersion <= 0 || subjectType == "" || subjectID == "" || currency == "" || asOf.IsZero() {
		return SubjectFundActivitySnapshot{}, false, fmt.Errorf("feature version, subject, currency and as_of are required")
	}
	query := fmt.Sprintf(`SELECT
feature_set_key, feature_set_version, subject_type, subject_id, currency,
window_start_millis, window_end_millis, as_of_millis,
inflow_minor, outflow_minor, outflow_ratio, outflow_ratio_defined,
source_event_count, source_run_id, content_fingerprint, computed_at_millis
FROM risk_warehouse.subject_fund_activity_24h FINAL
WHERE feature_set_key = 'subject_fund_activity_24h'
  AND feature_set_version = %d
  AND subject_type = '%s'
  AND subject_id = '%s'
  AND currency = '%s'
  AND as_of_millis <= %d
ORDER BY as_of_millis DESC
LIMIT 1
FORMAT JSONEachRow`,
		featureSetVersion,
		clickHouseString(subjectType), clickHouseString(subjectID), clickHouseString(currency),
		asOf.UTC().UnixMilli(),
	)
	body, err := c.query(ctx, query)
	if err != nil {
		return SubjectFundActivitySnapshot{}, false, err
	}
	defer body.Close()
	var snapshot SubjectFundActivitySnapshot
	if err := json.NewDecoder(body).Decode(&snapshot); err == io.EOF {
		return SubjectFundActivitySnapshot{}, false, nil
	} else if err != nil {
		return SubjectFundActivitySnapshot{}, false, fmt.Errorf("decode ClickHouse feature snapshot: %w", err)
	}
	return snapshot, true, nil
}

func (c *ClickHouse) readFundActivityRunFingerprint(ctx context.Context, sourceRunID string) (string, error) {
	query := fmt.Sprintf(`SELECT content_fingerprint
FROM risk_warehouse.subject_fund_activity_24h_history FINAL
WHERE source_run_id = '%s' AND content_fingerprint != ''
LIMIT 1
FORMAT TabSeparated`, clickHouseString(sourceRunID))
	body, err := c.query(ctx, query)
	if err != nil {
		return "", err
	}
	defer body.Close()
	encoded, err := io.ReadAll(io.LimitReader(body, 128))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(encoded)), nil
}

func prepareFundActivitySnapshots(snapshots []SubjectFundActivitySnapshot) ([]SubjectFundActivitySnapshot, string, string, error) {
	prepared := append([]SubjectFundActivitySnapshot(nil), snapshots...)
	sourceRunID := prepared[0].SourceRunID
	if sourceRunID == "" {
		return nil, "", "", fmt.Errorf("source_run_id is required")
	}
	for index := range prepared {
		if prepared[index].SourceRunID != sourceRunID {
			return nil, "", "", fmt.Errorf("all snapshots must use the same source_run_id")
		}
	}
	canonicalSnapshots := append([]SubjectFundActivitySnapshot(nil), prepared...)
	for index := range canonicalSnapshots {
		canonicalSnapshots[index].ContentFingerprint = ""
		canonicalSnapshots[index].ComputedAtMillis = 0
	}
	sort.Slice(canonicalSnapshots, func(i, j int) bool {
		left := canonicalSnapshots[i].SubjectType + "\x00" + canonicalSnapshots[i].SubjectID + "\x00" + canonicalSnapshots[i].Currency
		right := canonicalSnapshots[j].SubjectType + "\x00" + canonicalSnapshots[j].SubjectID + "\x00" + canonicalSnapshots[j].Currency
		return left < right
	})
	canonical, err := json.Marshal(canonicalSnapshots)
	if err != nil {
		return nil, "", "", fmt.Errorf("encode feature snapshot fingerprint: %w", err)
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(canonical))
	for index := range prepared {
		prepared[index].ContentFingerprint = fingerprint
	}
	return prepared, fingerprint, sourceRunID, nil
}

func (c *ClickHouse) HasFundActivitySnapshotSourceRun(ctx context.Context, sourceRunID string) (bool, error) {
	if sourceRunID == "" {
		return false, fmt.Errorf("source_run_id is required")
	}
	query := fmt.Sprintf(`SELECT count()
FROM risk_warehouse.subject_fund_activity_24h_history FINAL
WHERE source_run_id = '%s'
FORMAT TabSeparated`, clickHouseString(sourceRunID))
	body, err := c.query(ctx, query)
	if err != nil {
		return false, err
	}
	defer body.Close()
	encoded, err := io.ReadAll(io.LimitReader(body, 64))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(encoded)) != "0", nil
}

func clickHouseString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

func (c *ClickHouse) query(ctx context.Context, query string) (io.ReadCloser, error) {
	requestURL := c.endpoint + "/?query=" + url.QueryEscape(query)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		request.SetBasicAuth(c.user, c.password)
	}
	response, err := c.client.Do(request)
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

func (c *ClickHouse) execute(ctx context.Context, query string, body []byte, contentType string) error {
	requestURL := c.endpoint + "/?query=" + url.QueryEscape(query)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	if c.user != "" {
		request.SetBasicAuth(c.user, c.password)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	return fmt.Errorf("ClickHouse query failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
}
