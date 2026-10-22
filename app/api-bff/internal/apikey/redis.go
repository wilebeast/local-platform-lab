package apikey

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisNonceStore struct{ client *redis.Client }

func NewRedisNonceStore(address string) *RedisNonceStore {
	return &RedisNonceStore{client: redis.NewClient(&redis.Options{Addr: address})}
}

func (s *RedisNonceStore) Claim(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, "1", ttl).Result()
}

func (s *RedisNonceStore) Ping(ctx context.Context) error { return s.client.Ping(ctx).Err() }
func (s *RedisNonceStore) Close() error                   { return s.client.Close() }
