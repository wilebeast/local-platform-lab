package featureplatform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisCurrentFeatureStorePreservesTypesAndMonotonicWrites(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR is required for Redis Online Store integration")
	}
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })
	prefix := fmt.Sprintf("test:feature-current:%d", time.Now().UnixNano())
	store, err := NewRedisCurrentFeatureStore(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := onlineStoreTestSnapshot(time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), int64(100_000), "redis-fingerprint")
	snapshot.Values["outflow_ratio"] = float64(0.95)

	result, err := store.PutCurrent(ctx, snapshot)
	if err != nil || result != CurrentWriteApplied {
		t.Fatalf("initial Redis write result=%q err=%v", result, err)
	}
	result, err = store.PutCurrent(ctx, snapshot)
	if err != nil || result != CurrentWriteReplay {
		t.Fatalf("Redis replay result=%q err=%v", result, err)
	}
	conflicting := cloneFeatureSnapshot(snapshot)
	conflicting.ContentFingerprint = "different"
	if _, err := store.PutCurrent(ctx, conflicting); !errors.Is(err, ErrCurrentSnapshotConflict) {
		t.Fatalf("Redis conflict error=%v, want ErrCurrentSnapshotConflict", err)
	}

	rows, err := store.ReadLatestBefore(ctx, SnapshotReadRequest{
		RequestID: "redis-current-read", FeatureSetKey: snapshot.FeatureSetKey,
		FeatureSetVersion: snapshot.FeatureSetVersion,
		FeatureKeys:       []string{"inflow_minor", "outflow_ratio"},
		Subjects:          []Subject{snapshot.Subject}, Cutoff: snapshot.AsOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Values["inflow_minor"] != int64(100_000) || rows[0].Values["outflow_ratio"] != float64(0.95) {
		t.Fatalf("Redis round trip changed Feature types: %+v", rows)
	}
}
