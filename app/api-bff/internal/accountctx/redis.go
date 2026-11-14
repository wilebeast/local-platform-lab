package accountctx

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	projectionPrefix        = "auth:account-access:"
	projectionVersionPrefix = "auth:account-access-version:"
	selectionPrefix         = "auth:session-account:"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore { return &RedisStore{client: client} }

func (s *RedisStore) PutProjection(ctx context.Context, value Projection, ttl time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	version := strconv.FormatInt(value.PermissionVersion, 10)
	_, err = s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, projectionPrefix+value.UserID+":"+version, encoded, ttl)
		pipe.Set(ctx, projectionVersionPrefix+value.UserID, version, ttl)
		return nil
	})
	return err
}

func (s *RedisStore) GetProjection(ctx context.Context, userID string) (Projection, error) {
	version, err := s.client.Get(ctx, projectionVersionPrefix+userID).Result()
	if errors.Is(err, redis.Nil) {
		return Projection{}, ErrProjectionNotFound
	}
	if err != nil {
		return Projection{}, err
	}
	encoded, err := s.client.Get(ctx, projectionPrefix+userID+":"+version).Bytes()
	if errors.Is(err, redis.Nil) {
		return Projection{}, ErrProjectionNotFound
	}
	if err != nil {
		return Projection{}, err
	}
	var value Projection
	if json.Unmarshal(encoded, &value) != nil {
		return Projection{}, ErrInvalidProjection
	}
	return value, nil
}

func (s *RedisStore) PutSelection(ctx context.Context, sessionID string, value Selection, ttl time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, selectionPrefix+sessionID, encoded, ttl).Err()
}

func (s *RedisStore) GetSelection(ctx context.Context, sessionID string) (Selection, error) {
	encoded, err := s.client.Get(ctx, selectionPrefix+sessionID).Bytes()
	if errors.Is(err, redis.Nil) {
		return Selection{}, ErrSelectionNotFound
	}
	if err != nil {
		return Selection{}, err
	}
	var value Selection
	if json.Unmarshal(encoded, &value) != nil {
		return Selection{}, ErrSelectionNotFound
	}
	return value, nil
}
