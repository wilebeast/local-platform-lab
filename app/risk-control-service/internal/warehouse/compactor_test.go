package warehouse_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestFundMovementCompactorWritesStandardFactsToParquet(t *testing.T) {
	objects := []warehouse.RawObject{
		{
			Key:      "raw/v1/event_type=funds.movement.recorded/event_date=2026-11-01/event_id=evt_out_001.json",
			Body:     []byte(`{"event_id":"evt_out_001","event_type":"funds.movement.recorded","event_version":1,"occurred_at":"2026-11-01T10:05:00Z","produced_at":"2026-11-01T10:05:01Z","subject_type":"organization","subject_id":"org_001","payload":{"direction":"outflow","currency":"USD","amount_minor":95000}}`),
			Metadata: map[string]string{"ingested-at": "2026-11-01T10:05:02Z"},
		},
		{
			Key:      "raw/v1/event_type=funds.movement.recorded/event_date=2026-11-01/event_id=evt_in_001.json",
			Body:     []byte(`{"event_id":"evt_in_001","event_type":"funds.movement.recorded","event_version":1,"occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z","subject_type":"organization","subject_id":"org_001","payload":{"direction":"inflow","currency":"USD","amount_minor":100000}}`),
			Metadata: map[string]string{"ingested-at": "2026-11-01T10:00:02Z"},
		},
	}
	for index := range objects {
		objects[index].Metadata["sha256"] = warehouse.ContentSHA256(objects[index].Body)
	}

	result, err := warehouse.NewFundMovementCompactor().Compact(
		context.Background(),
		warehouse.CompactionRequest{EventDate: "2026-11-01", Objects: objects},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 2 || len(result.Rejected) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	rows, err := parquet.Read[warehouse.FundMovementFact](
		bytes.NewReader(result.Parquet), int64(len(result.Parquet)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].EventID != "evt_in_001" || rows[0].Direction != "inflow" ||
		rows[0].AmountMinor != 100000 || rows[0].Currency != "USD" ||
		rows[0].SubjectID != "org_001" || rows[0].SourceSHA256 != warehouse.ContentSHA256(objects[1].Body) {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	if rows[1].EventID != "evt_out_001" || rows[1].Direction != "outflow" ||
		rows[1].AmountMinor != 95000 || rows[1].SourceObjectKey != objects[0].Key {
		t.Fatalf("unexpected second row: %+v", rows[1])
	}
}

func TestFundMovementCompactorRejectsObjectWhoseDigestDoesNotMatchMetadata(t *testing.T) {
	object := warehouse.RawObject{
		Key:      "raw/v1/event_type=funds.movement.recorded/event_date=2026-11-01/event_id=evt_tampered.json",
		Body:     []byte(`{"event_id":"evt_tampered","event_type":"funds.movement.recorded","event_version":1,"occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z","subject_type":"organization","subject_id":"org_001","payload":{"direction":"inflow","currency":"USD","amount_minor":100000}}`),
		Metadata: map[string]string{"ingested-at": "2026-11-01T10:00:02Z", "sha256": "not-the-content-digest"},
	}

	result, err := warehouse.NewFundMovementCompactor().Compact(
		context.Background(),
		warehouse.CompactionRequest{EventDate: "2026-11-01", Objects: []warehouse.RawObject{object}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 0 || len(result.Rejected) != 1 {
		t.Fatalf("tampered object reached DWD: %+v", result)
	}
	if result.Rejected[0].ObjectKey != object.Key {
		t.Fatalf("rejection lost source lineage: %+v", result.Rejected[0])
	}
}

func TestFundMovementCompactorIsDeterministicAcrossInputOrder(t *testing.T) {
	first := rawFundMovementObject("evt_002", "outflow", 95000, "2026-11-01T10:05:00Z")
	second := rawFundMovementObject("evt_001", "inflow", 100000, "2026-11-01T10:00:00Z")
	compactor := warehouse.NewFundMovementCompactor()

	forward, err := compactor.Compact(context.Background(), warehouse.CompactionRequest{
		EventDate: "2026-11-01", Objects: []warehouse.RawObject{first, second},
	})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := compactor.Compact(context.Background(), warehouse.CompactionRequest{
		EventDate: "2026-11-01", Objects: []warehouse.RawObject{second, first},
	})
	if err != nil {
		t.Fatal(err)
	}
	if forward.SHA256 != reversed.SHA256 || !bytes.Equal(forward.Parquet, reversed.Parquet) {
		t.Fatalf("same partition produced different output: %s != %s", forward.SHA256, reversed.SHA256)
	}
}

func TestCompactionResultUsesImmutableContentAddressedObjectKey(t *testing.T) {
	object := rawFundMovementObject("evt_001", "inflow", 100000, "2026-11-01T10:00:00Z")
	result, err := warehouse.NewFundMovementCompactor().Compact(
		context.Background(),
		warehouse.CompactionRequest{EventDate: "2026-11-01", Objects: []warehouse.RawObject{object}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "dwd/v1/fact_fund_movements/event_date=2026-11-01/content_sha256=" + result.SHA256 + "/part-00000.parquet"
	if result.ParquetObjectKey("2026-11-01") != want {
		t.Fatalf("got %q, want %q", result.ParquetObjectKey("2026-11-01"), want)
	}
}

func rawFundMovementObject(eventID, direction string, amount int64, occurredAt string) warehouse.RawObject {
	body := []byte(`{"event_id":"` + eventID + `","event_type":"funds.movement.recorded","event_version":1,"occurred_at":"` + occurredAt + `","produced_at":"2026-11-01T10:06:00Z","subject_type":"organization","subject_id":"org_001","payload":{"direction":"` + direction + `","currency":"USD","amount_minor":` + formatInt(amount) + `}}`)
	return warehouse.RawObject{
		Key:  "raw/v1/event_type=funds.movement.recorded/event_date=2026-11-01/event_id=" + eventID + ".json",
		Body: body,
		Metadata: map[string]string{
			"ingested-at": "2026-11-01T10:07:00Z",
			"sha256":      warehouse.ContentSHA256(body),
		},
	}
}

func formatInt(value int64) string {
	return fmt.Sprintf("%d", value)
}
