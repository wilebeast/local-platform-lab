package registration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type recordingRedisCommands struct {
	setKey   string
	setValue any
	setTTL   time.Duration
	delKey   string
}

func (r *recordingRedisCommands) Set(_ context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	r.setKey, r.setValue, r.setTTL = key, value, expiration
	command := redis.NewStatusCmd(context.Background())
	command.SetVal("OK")
	return command
}

func (r *recordingRedisCommands) Del(_ context.Context, keys ...string) *redis.IntCmd {
	if len(keys) > 0 {
		r.delKey = keys[0]
	}
	command := redis.NewIntCmd(context.Background())
	command.SetVal(1)
	return command
}

func TestRedisChallengeStoreUsesDigestAsKeyAndKeepsRawTokenOutOfPayload(t *testing.T) {
	commands := &recordingRedisCommands{}
	store := NewRedisChallengeStore(commands)
	challenge := RegistrationChallenge{
		TokenDigest: strings.Repeat("a", 64),
		Email:       "reader@example.com",
		Purpose:     RegistrationPurpose,
	}

	if err := store.Save(context.Background(), challenge, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if commands.setKey != "user:registration:challenge:"+challenge.TokenDigest {
		t.Fatalf("unexpected Redis key %q", commands.setKey)
	}
	if commands.setTTL != 10*time.Minute {
		t.Fatalf("unexpected TTL %s", commands.setTTL)
	}
	payload := commands.setValue.(string)
	if !strings.Contains(payload, `"email":"reader@example.com"`) || !strings.Contains(payload, `"purpose":"registration"`) {
		t.Fatalf("unexpected challenge payload %s", payload)
	}
	if strings.Contains(payload, "opaque-registration-token") || strings.Contains(payload, challenge.TokenDigest) {
		t.Fatalf("challenge payload leaked token material: %s", payload)
	}

	if err := store.Delete(context.Background(), challenge.TokenDigest); err != nil {
		t.Fatal(err)
	}
	if commands.delKey != commands.setKey {
		t.Fatalf("delete key %q does not match save key %q", commands.delKey, commands.setKey)
	}
}
