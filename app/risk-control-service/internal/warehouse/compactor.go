package warehouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
)

const fundMovementEventType = "funds.movement.recorded"

type RawObject struct {
	Key      string
	Body     []byte
	Metadata map[string]string
}

type CompactionRequest struct {
	EventDate string
	Objects   []RawObject
}

type RejectedObject struct {
	ObjectKey string
	Reason    string
}

type CompactionResult struct {
	Parquet  []byte
	SHA256   string
	RowCount int
	Rejected []RejectedObject
}

func (r CompactionResult) ParquetObjectKey(eventDate string) string {
	return "dwd/v1/fact_fund_movements/event_date=" + eventDate +
		"/content_sha256=" + r.SHA256 + "/part-00000.parquet"
}

type FundMovementFact struct {
	EventID          string `parquet:"event_id" json:"event_id"`
	EventVersion     int32  `parquet:"event_version" json:"event_version"`
	OccurredAtMillis int64  `parquet:"occurred_at_millis" json:"occurred_at_millis"`
	ProducedAtMillis int64  `parquet:"produced_at_millis" json:"produced_at_millis"`
	IngestedAtMillis int64  `parquet:"ingested_at_millis" json:"ingested_at_millis"`
	SubjectType      string `parquet:"subject_type" json:"subject_type"`
	SubjectID        string `parquet:"subject_id" json:"subject_id"`
	Direction        string `parquet:"direction" json:"direction"`
	Currency         string `parquet:"currency" json:"currency"`
	AmountMinor      int64  `parquet:"amount_minor" json:"amount_minor"`
	SourceObjectKey  string `parquet:"source_object_key" json:"source_object_key"`
	SourceSHA256     string `parquet:"source_sha256" json:"source_sha256"`
}

type fundMovementPayload struct {
	Direction   string `json:"direction"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
}

type rawEnvelope struct {
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	EventVersion int             `json:"event_version"`
	OccurredAt   time.Time       `json:"occurred_at"`
	ProducedAt   time.Time       `json:"produced_at"`
	SubjectType  string          `json:"subject_type"`
	SubjectID    string          `json:"subject_id"`
	Payload      json.RawMessage `json:"payload"`
}

type FundMovementCompactor struct{}

func NewFundMovementCompactor() *FundMovementCompactor {
	return &FundMovementCompactor{}
}

func ContentSHA256(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func (c *FundMovementCompactor) Compact(_ context.Context, request CompactionRequest) (CompactionResult, error) {
	if _, err := time.Parse(time.DateOnly, request.EventDate); err != nil {
		return CompactionResult{}, fmt.Errorf("event date must use YYYY-MM-DD: %w", err)
	}

	rows := make([]FundMovementFact, 0, len(request.Objects))
	rejected := make([]RejectedObject, 0)
	for _, object := range request.Objects {
		row, err := standardizeFundMovement(request.EventDate, object)
		if err != nil {
			rejected = append(rejected, RejectedObject{ObjectKey: object.Key, Reason: err.Error()})
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].EventID < rows[j].EventID })

	var output bytes.Buffer
	if err := parquet.Write(&output, rows); err != nil {
		return CompactionResult{}, fmt.Errorf("write fund movement parquet: %w", err)
	}
	parquetBytes := output.Bytes()
	digest := sha256.Sum256(parquetBytes)
	return CompactionResult{
		Parquet: parquetBytes, SHA256: hex.EncodeToString(digest[:]),
		RowCount: len(rows), Rejected: rejected,
	}, nil
}

func standardizeFundMovement(eventDate string, object RawObject) (FundMovementFact, error) {
	if object.Metadata["sha256"] == "" || object.Metadata["sha256"] != ContentSHA256(object.Body) {
		return FundMovementFact{}, fmt.Errorf("raw object SHA-256 does not match metadata")
	}
	var event rawEnvelope
	if err := json.Unmarshal(object.Body, &event); err != nil {
		return FundMovementFact{}, fmt.Errorf("decode raw event: %w", err)
	}
	if event.EventType != fundMovementEventType || event.EventVersion <= 0 ||
		event.EventID == "" || event.SubjectType == "" || event.SubjectID == "" ||
		event.OccurredAt.IsZero() || event.ProducedAt.IsZero() {
		return FundMovementFact{}, fmt.Errorf("invalid fund movement envelope")
	}
	if event.OccurredAt.UTC().Format(time.DateOnly) != eventDate {
		return FundMovementFact{}, fmt.Errorf("event date does not match partition")
	}
	var payload fundMovementPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return FundMovementFact{}, fmt.Errorf("decode fund movement payload: %w", err)
	}
	if payload.Direction != "inflow" && payload.Direction != "outflow" {
		return FundMovementFact{}, fmt.Errorf("direction must be inflow or outflow")
	}
	if len(payload.Currency) != 3 || payload.Currency != strings.ToUpper(payload.Currency) {
		return FundMovementFact{}, fmt.Errorf("currency must be a three-letter uppercase code")
	}
	if payload.AmountMinor <= 0 {
		return FundMovementFact{}, fmt.Errorf("amount_minor must be positive")
	}
	ingestedAt, err := time.Parse(time.RFC3339Nano, object.Metadata["ingested-at"])
	if err != nil {
		return FundMovementFact{}, fmt.Errorf("ingested-at metadata is required: %w", err)
	}
	return FundMovementFact{
		EventID: event.EventID, EventVersion: int32(event.EventVersion),
		OccurredAtMillis: event.OccurredAt.UTC().UnixMilli(),
		ProducedAtMillis: event.ProducedAt.UTC().UnixMilli(),
		IngestedAtMillis: ingestedAt.UTC().UnixMilli(),
		SubjectType:      event.SubjectType, SubjectID: event.SubjectID,
		Direction: payload.Direction, Currency: payload.Currency,
		AmountMinor: payload.AmountMinor, SourceObjectKey: object.Key,
		SourceSHA256: object.Metadata["sha256"],
	}, nil
}
