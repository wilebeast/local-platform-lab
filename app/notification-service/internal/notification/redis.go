package notification

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCodeStore struct{ client *redis.Client }

func NewRedisCodeStore(client *redis.Client) *RedisCodeStore { return &RedisCodeStore{client: client} }

func (s *RedisCodeStore) IsBlocked(ctx context.Context, recipient string) (bool, error) {
	count, err := s.client.Exists(ctx, blockKey(recipient)).Result()
	return count > 0, err
}

func (s *RedisCodeStore) AcquireCooldown(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, "mail:code:cooldown:"+key, "1", ttl).Result()
}

func (s *RedisCodeStore) ReleaseCooldown(ctx context.Context, key string) error {
	return s.client.Del(ctx, "mail:code:cooldown:"+key).Err()
}

func (s *RedisCodeStore) SaveCode(ctx context.Context, key string, digest []byte, ttl time.Duration) error {
	return s.client.Set(ctx, "mail:code:value:"+key, hex.EncodeToString(digest), ttl).Err()
}

func (s *RedisCodeStore) DeleteCode(ctx context.Context, key string) error {
	return s.client.Del(ctx, "mail:code:value:"+key).Err()
}

var consumeCodeScript = redis.NewScript(`
local stored = redis.call('GET', KEYS[1])
if not stored then return 0 end
if stored ~= ARGV[1] then return 1 end
redis.call('DEL', KEYS[1])
return 2
`)

func (s *RedisCodeStore) ConsumeCode(ctx context.Context, key string, digest []byte) (CodeMatch, error) {
	result, err := consumeCodeScript.Run(ctx, s.client, []string{"mail:code:value:" + key}, hex.EncodeToString(digest)).Int()
	if err != nil {
		return CodeMissing, err
	}
	switch result {
	case 2:
		return CodeMatched, nil
	case 1:
		return CodeMismatch, nil
	default:
		return CodeMissing, nil
	}
}

var failureScript = redis.NewScript(`
local failures = redis.call('INCR', KEYS[1])
if failures == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
if failures >= tonumber(ARGV[1]) then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
  return 1
end
return 0
`)

func (s *RedisCodeStore) RegisterFailure(ctx context.Context, recipient string, limit int, window, blockedFor time.Duration) (bool, error) {
	result, err := failureScript.Run(ctx, s.client,
		[]string{failureKey(recipient), blockKey(recipient)},
		limit, window.Milliseconds(), blockedFor.Milliseconds()).Int()
	return result == 1, err
}

func (s *RedisCodeStore) ClearFailures(ctx context.Context, recipient string) error {
	return s.client.Del(ctx, failureKey(recipient)).Err()
}

func failureKey(recipient string) string { return "mail:code:failures:" + recipientDigest(recipient) }
func blockKey(recipient string) string   { return "mail:code:blocked:" + recipientDigest(recipient) }

func recipientDigest(recipient string) string {
	key := codeKey(recipient, "recipient")
	return key[len("recipient:"):]
}
