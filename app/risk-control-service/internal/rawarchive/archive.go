package rawarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	archiveIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,159}$`)
	ErrObjectConflict        = errors.New("raw object key belongs to different content")
	ErrInvalidEnvelope       = errors.New("invalid raw event envelope")
)

type RawEventEnvelope struct {
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	EventVersion int             `json:"event_version"`
	OccurredAt   time.Time       `json:"occurred_at"`
	ProducedAt   time.Time       `json:"produced_at"`
	SubjectType  string          `json:"subject_type"`
	SubjectID    string          `json:"subject_id"`
	Payload      json.RawMessage `json:"payload"`
}

type PutState string

const (
	PutStateCreated PutState = "created"
	PutStateReplay  PutState = "replay"
)

type PutResult struct {
	State PutState
}

type ObjectStore interface {
	PutIfAbsent(
		context.Context,
		string,
		[]byte,
		string,
		map[string]string,
	) (PutResult, error)
}

type Archive struct {
	store ObjectStore
}

type ArchiveResult struct {
	EventID    string    `json:"event_id"`
	ObjectKey  string    `json:"object_key"`
	SHA256     string    `json:"sha256"`
	Replay     bool      `json:"replay"`
	ArchivedAt time.Time `json:"archived_at"`
}

func NewArchive(store ObjectStore) *Archive {
	return &Archive{store: store}
}

func (a *Archive) Store(ctx context.Context, raw []byte) (ArchiveResult, error) {
	if a == nil || a.store == nil {
		return ArchiveResult{}, fmt.Errorf("raw archive object store is not configured")
	}
	var event RawEventEnvelope
	if !json.Valid(raw) {
		return ArchiveResult{}, fmt.Errorf("%w: raw event is not valid JSON", ErrInvalidEnvelope)
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return ArchiveResult{}, fmt.Errorf("%w: decode raw event: %v", ErrInvalidEnvelope, err)
	}
	if err := validateEnvelope(event); err != nil {
		return ArchiveResult{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}

	digestBytes := sha256.Sum256(raw)
	digest := hex.EncodeToString(digestBytes[:])
	archivedAt := time.Now().UTC()
	objectKey := fmt.Sprintf(
		"raw/v1/event_type=%s/event_date=%s/event_id=%s.json",
		event.EventType,
		event.OccurredAt.UTC().Format(time.DateOnly),
		event.EventID,
	)
	put, err := a.store.PutIfAbsent(ctx, objectKey, raw, digest, map[string]string{
		"event-id":      event.EventID,
		"event-type":    event.EventType,
		"event-version": fmt.Sprintf("%d", event.EventVersion),
		"sha256":        digest,
		"ingested-at":   archivedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return ArchiveResult{}, err
	}
	return ArchiveResult{
		EventID: event.EventID, ObjectKey: objectKey, SHA256: digest,
		Replay: put.State == PutStateReplay, ArchivedAt: archivedAt,
	}, nil
}

func validateEnvelope(event RawEventEnvelope) error {
	if !archiveIdentifierPattern.MatchString(event.EventID) {
		return fmt.Errorf("event_id is required and must be a safe identifier")
	}
	if !archiveIdentifierPattern.MatchString(event.EventType) {
		return fmt.Errorf("event_type is required and must be a safe identifier")
	}
	if event.EventVersion <= 0 {
		return fmt.Errorf("event_version must be positive")
	}
	if event.OccurredAt.IsZero() || event.ProducedAt.IsZero() {
		return fmt.Errorf("occurred_at and produced_at are required")
	}
	if !archiveIdentifierPattern.MatchString(event.SubjectType) || event.SubjectID == "" {
		return fmt.Errorf("subject_type and subject_id are required")
	}
	if len(event.Payload) == 0 || !json.Valid(event.Payload) {
		return fmt.Errorf("payload must be valid JSON")
	}
	return nil
}
