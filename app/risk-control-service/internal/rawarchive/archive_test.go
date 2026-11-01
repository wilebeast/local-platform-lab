package rawarchive

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

type memoryObjectStore struct {
	objects map[string]storedObject
}

type storedObject struct {
	body     []byte
	digest   string
	metadata map[string]string
}

func (s *memoryObjectStore) PutIfAbsent(
	_ context.Context,
	key string,
	body []byte,
	digest string,
	metadata map[string]string,
) (PutResult, error) {
	if s.objects == nil {
		s.objects = make(map[string]storedObject)
	}
	if existing, ok := s.objects[key]; ok {
		if existing.digest == digest {
			return PutResult{State: PutStateReplay}, nil
		}
		return PutResult{}, ErrObjectConflict
	}
	metadataCopy := make(map[string]string, len(metadata))
	for metadataKey, value := range metadata {
		metadataCopy[metadataKey] = value
	}
	s.objects[key] = storedObject{body: bytes.Clone(body), digest: digest, metadata: metadataCopy}
	return PutResult{State: PutStateCreated}, nil
}

func TestArchiveStoresExactRawEventAtDeterministicKey(t *testing.T) {
	store := &memoryObjectStore{}
	archive := NewArchive(store)
	raw := []byte(`{"event_id":"evt_001","event_type":"funds.movement.recorded","event_version":1,"occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z","subject_type":"organization","subject_id":"org_001","payload":{"currency":"USD","amount_minor":100000}}`)

	result, err := archive.Store(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := "raw/v1/event_type=funds.movement.recorded/event_date=2026-11-01/event_id=evt_001.json"
	if result.ObjectKey != wantKey || result.Replay {
		t.Fatalf("unexpected archive result: %+v", result)
	}
	if !bytes.Equal(store.objects[wantKey].body, raw) {
		t.Fatal("raw object bytes changed during archival")
	}
	if store.objects[wantKey].metadata["ingested-at"] == "" {
		t.Fatal("raw object is missing ingestion time metadata")
	}
}

func TestArchiveTreatsExactDuplicateAsReplayAndRejectsConflict(t *testing.T) {
	store := &memoryObjectStore{}
	archive := NewArchive(store)
	original := []byte(`{"event_id":"evt_002","event_type":"funds.movement.recorded","event_version":1,"occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z","subject_type":"organization","subject_id":"org_002","payload":{"amount_minor":100000}}`)
	conflict := []byte(`{"event_id":"evt_002","event_type":"funds.movement.recorded","event_version":1,"occurred_at":"2026-11-01T10:00:00Z","produced_at":"2026-11-01T10:00:01Z","subject_type":"organization","subject_id":"org_002","payload":{"amount_minor":200000}}`)

	if _, err := archive.Store(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	replayed, err := archive.Store(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replay || len(store.objects) != 1 {
		t.Fatalf("exact duplicate was not replayed: %+v", replayed)
	}
	if _, err := archive.Store(context.Background(), conflict); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("expected object conflict, got %v", err)
	}
	if len(store.objects) != 1 {
		t.Fatalf("conflict created another object: %d", len(store.objects))
	}
}

func TestArchiveRejectsInvalidEnvelopeBeforeObjectWrite(t *testing.T) {
	store := &memoryObjectStore{}
	archive := NewArchive(store)

	if _, err := archive.Store(context.Background(), []byte(`{"event_id":"../escape"}`)); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("expected invalid envelope error, got %v", err)
	}
	if len(store.objects) != 0 {
		t.Fatal("invalid event reached object storage")
	}
}
