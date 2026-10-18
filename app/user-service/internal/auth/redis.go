package auth

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const refreshPrefix = "auth:refresh:"

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(address string) *RedisStore {
	return &RedisStore{client: redis.NewClient(&redis.Options{Addr: address})}
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}

func (s *RedisStore) Put(ctx context.Context, hash, userID string, ttl time.Duration) error {
	return s.client.Set(ctx, refreshPrefix+hash, userID, ttl).Err()
}

func (s *RedisStore) Consume(ctx context.Context, hash string) (string, error) {
	value, err := s.client.GetDel(ctx, refreshPrefix+hash).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrInvalidRefreshToken
	}
	return value, err
}

func (s *RedisStore) Delete(ctx context.Context, hash string) (bool, error) {
	deleted, err := s.client.Del(ctx, refreshPrefix+hash).Result()
	return deleted > 0, err
}
