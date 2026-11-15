package featureplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCurrentFeatureStore struct {
	client    redis.UniversalClient
	keyPrefix string
	ttl       time.Duration
}

func NewRedisCurrentFeatureStore(
	client redis.UniversalClient,
	keyPrefix string,
	ttl time.Duration,
) (*RedisCurrentFeatureStore, error) {
	if client == nil || keyPrefix == "" || ttl <= 0 {
		return nil, fmt.Errorf("redis client, key prefix and positive ttl are required")
	}
	return &RedisCurrentFeatureStore{client: client, keyPrefix: keyPrefix, ttl: ttl}, nil
}

func (s *RedisCurrentFeatureStore) PutCurrent(
	ctx context.Context,
	snapshot FeatureSnapshot,
) (CurrentWriteResult, error) {
	if err := validateCurrentSnapshot(snapshot); err != nil {
		return "", err
	}
	payload, err := encodeOnlineSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	key := s.snapshotKey(snapshot.FeatureSetKey, snapshot.FeatureSetVersion, snapshot.Subject)
	var result CurrentWriteResult
	for attempt := 0; attempt < 5; attempt++ {
		err = s.client.Watch(ctx, func(tx *redis.Tx) error {
			existingPayload, getErr := tx.Get(ctx, key).Bytes()
			if getErr != nil && !errors.Is(getErr, redis.Nil) {
				return getErr
			}
			if getErr == nil {
				existing, decodeErr := decodeOnlineSnapshot(existingPayload)
				if decodeErr != nil {
					return fmt.Errorf("decode existing current feature: %w", decodeErr)
				}
				switch {
				case snapshot.AsOf.Before(existing.AsOf):
					result = CurrentWriteIgnoredOlder
					return nil
				case snapshot.AsOf.Equal(existing.AsOf) && snapshot.ContentFingerprint == existing.ContentFingerprint:
					result = CurrentWriteReplay
					return nil
				case snapshot.AsOf.Equal(existing.AsOf):
					return ErrCurrentSnapshotConflict
				}
			}
			_, pipelineErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, payload, s.ttl)
				return nil
			})
			if pipelineErr == nil {
				result = CurrentWriteApplied
			}
			return pipelineErr
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return "", err
		}
		return result, nil
	}
	return "", fmt.Errorf("current feature write exceeded transaction retries")
}

func (s *RedisCurrentFeatureStore) ReadLatestBefore(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	if err := validateCurrentReadRequest(request); err != nil {
		return nil, err
	}
	commands := make([]*redis.StringCmd, len(request.Subjects))
	_, err := s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index, subject := range request.Subjects {
			commands[index] = pipe.Get(ctx, s.snapshotKey(request.FeatureSetKey, request.FeatureSetVersion, subject))
		}
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read Redis current features: %w", err)
	}
	rows := make([]FeatureSnapshot, 0, len(request.Subjects))
	for _, command := range commands {
		payload, commandErr := command.Bytes()
		if errors.Is(commandErr, redis.Nil) {
			continue
		}
		if commandErr != nil {
			return nil, commandErr
		}
		snapshot, decodeErr := decodeOnlineSnapshot(payload)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Redis current feature: %w", decodeErr)
		}
		if snapshot.AsOf.After(request.Cutoff) {
			continue
		}
		rows = append(rows, projectFeatureSnapshot(snapshot, request.FeatureKeys))
	}
	return rows, nil
}

func (s *RedisCurrentFeatureStore) snapshotKey(featureSetKey string, version int, subject Subject) string {
	identity := currentSnapshotIdentity(featureSetKey, version, subject)
	sum := sha256.Sum256([]byte(identity))
	return s.keyPrefix + ":" + hex.EncodeToString(sum[:])
}

type onlineSnapshotDocument struct {
	FeatureSetKey      string                         `json:"feature_set_key"`
	FeatureSetVersion  int                            `json:"feature_set_version"`
	Subject            Subject                        `json:"subject"`
	AsOf               time.Time                      `json:"as_of"`
	ComputedAt         time.Time                      `json:"computed_at"`
	SourceRunID        string                         `json:"source_run_id"`
	ContentFingerprint string                         `json:"content_fingerprint"`
	QualityStatus      string                         `json:"quality_status"`
	Values             map[string]onlineValueDocument `json:"values"`
	Undefined          map[string]bool                `json:"undefined"`
}

type onlineValueDocument struct {
	Kind          string          `json:"kind"`
	IntValue      int64           `json:"int_value"`
	DoubleValue   float64         `json:"double_value"`
	StringValue   string          `json:"string_value"`
	BoolValue     bool            `json:"bool_value"`
	Timestamp     time.Time       `json:"timestamp"`
	DurationNanos int64           `json:"duration_nanos"`
	JSONValue     json.RawMessage `json:"json_value"`
}

func encodeOnlineSnapshot(snapshot FeatureSnapshot) ([]byte, error) {
	document := onlineSnapshotDocument{
		FeatureSetKey: snapshot.FeatureSetKey, FeatureSetVersion: snapshot.FeatureSetVersion,
		Subject: cloneSubject(snapshot.Subject), AsOf: snapshot.AsOf, ComputedAt: snapshot.ComputedAt,
		SourceRunID: snapshot.SourceRunID, ContentFingerprint: snapshot.ContentFingerprint,
		QualityStatus: snapshot.QualityStatus, Values: make(map[string]onlineValueDocument, len(snapshot.Values)),
		Undefined: snapshot.Undefined,
	}
	for key, value := range snapshot.Values {
		encoded, err := encodeOnlineValue(value)
		if err != nil {
			return nil, fmt.Errorf("encode feature %q: %w", key, err)
		}
		document.Values[key] = encoded
	}
	return json.Marshal(document)
}

func decodeOnlineSnapshot(payload []byte) (FeatureSnapshot, error) {
	var document onlineSnapshotDocument
	if err := json.Unmarshal(payload, &document); err != nil {
		return FeatureSnapshot{}, err
	}
	snapshot := FeatureSnapshot{
		FeatureSetKey: document.FeatureSetKey, FeatureSetVersion: document.FeatureSetVersion,
		Subject: document.Subject, AsOf: document.AsOf, ComputedAt: document.ComputedAt,
		SourceRunID: document.SourceRunID, ContentFingerprint: document.ContentFingerprint,
		QualityStatus: document.QualityStatus, Values: make(map[string]any, len(document.Values)),
		Undefined: document.Undefined,
	}
	for key, encoded := range document.Values {
		value, err := decodeOnlineValue(encoded)
		if err != nil {
			return FeatureSnapshot{}, fmt.Errorf("decode feature %q: %w", key, err)
		}
		snapshot.Values[key] = value
	}
	return snapshot, validateCurrentSnapshot(snapshot)
}

func encodeOnlineValue(value any) (onlineValueDocument, error) {
	switch typed := value.(type) {
	case int64:
		return onlineValueDocument{Kind: "int", IntValue: typed}, nil
	case float64:
		return onlineValueDocument{Kind: "double", DoubleValue: typed}, nil
	case string:
		return onlineValueDocument{Kind: "string", StringValue: typed}, nil
	case bool:
		return onlineValueDocument{Kind: "bool", BoolValue: typed}, nil
	case time.Time:
		return onlineValueDocument{Kind: "timestamp", Timestamp: typed}, nil
	case time.Duration:
		return onlineValueDocument{Kind: "duration", DurationNanos: int64(typed)}, nil
	case map[string]any:
		encoded, err := json.Marshal(typed)
		return onlineValueDocument{Kind: "json", JSONValue: encoded}, err
	default:
		return onlineValueDocument{}, fmt.Errorf("unsupported online feature type %T", value)
	}
}

func decodeOnlineValue(value onlineValueDocument) (any, error) {
	switch value.Kind {
	case "int":
		return value.IntValue, nil
	case "double":
		return value.DoubleValue, nil
	case "string":
		return value.StringValue, nil
	case "bool":
		return value.BoolValue, nil
	case "timestamp":
		return value.Timestamp, nil
	case "duration":
		return time.Duration(value.DurationNanos), nil
	case "json":
		var decoded map[string]any
		if err := json.Unmarshal(value.JSONValue, &decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported online feature kind %q", value.Kind)
	}
}
