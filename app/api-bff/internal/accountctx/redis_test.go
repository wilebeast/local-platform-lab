package accountctx

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoreUsesOneVersionedProjectionAndSessionSelections(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	store := NewRedisStore(client)
	projection := Projection{UserID: "user-1", PermissionVersion: 7, Accesses: []Access{{AccountID: "account-1", AccountUID: "personal", AccountType: "personal", Relationship: "owner"}}}
	if err := store.PutProjection(context.Background(), projection, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSelection(context.Background(), "session-phone", Selection{AccountUID: "personal", PermissionVersion: 7, SelectedAt: 100}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSelection(context.Background(), "session-desktop", Selection{AccountUID: "personal", PermissionVersion: 7, SelectedAt: 200}, time.Hour); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetProjection(context.Background(), "user-1")
	if err != nil || got.PermissionVersion != 7 || len(got.Accesses) != 1 {
		t.Fatalf("unexpected projection: value=%+v err=%v", got, err)
	}
	keys := server.Keys()
	wantKeys := map[string]bool{
		"auth:account-access-version:user-1":   true,
		"auth:account-access:user-1:7":         true,
		"auth:session-account:session-phone":   true,
		"auth:session-account:session-desktop": true,
	}
	if len(keys) != len(wantKeys) {
		t.Fatalf("unexpected Redis keys: %v", keys)
	}
	for _, key := range keys {
		if !wantKeys[key] {
			t.Fatalf("unexpected Redis key %q", key)
		}
		if server.TTL(key) <= 0 {
			t.Fatalf("key %q has no TTL", key)
		}
	}
}
