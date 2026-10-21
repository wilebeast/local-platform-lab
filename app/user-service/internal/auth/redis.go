package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const refreshPrefix = "auth:refresh:"

const (
	sessionPrefix     = "auth:session:"
	userVersionPrefix = "auth:user-version:"
)

const (
	totpSecretPrefix   = "auth:totp:secret:"
	totpSetupPrefix    = "auth:totp:setup:"
	loginSessionPrefix = "auth:totp:login:"
	recoveryPrefix     = "auth:totp:recovery:"
)

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

func (s *RedisStore) PutSession(ctx context.Context, session Session, ttl time.Duration) error {
	value, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, sessionPrefix+session.ID, value, ttl).Err()
}

func (s *RedisStore) GetSession(ctx context.Context, sid string) (Session, error) {
	value, err := s.client.Get(ctx, sessionPrefix+sid).Bytes()
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(value, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *RedisStore) RevokeSession(ctx context.Context, sid string) error {
	key := sessionPrefix + sid
	value, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	var session Session
	if err := json.Unmarshal(value, &session); err != nil {
		return err
	}
	session.Status = "revoked"
	updated, err := json.Marshal(session)
	if err != nil {
		return err
	}
	ttl, err := s.client.TTL(ctx, key).Result()
	if err != nil {
		return err
	}
	return s.client.Set(ctx, key, updated, ttl).Err()
}

func (s *RedisStore) GetUserVersion(ctx context.Context, userID string) (int64, error) {
	key := userVersionPrefix + userID
	value, err := s.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		created, setErr := s.client.SetNX(ctx, key, "1", 0).Result()
		if setErr != nil {
			return 0, setErr
		}
		if created {
			return 1, nil
		}
		value, err = s.client.Get(ctx, key).Result()
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

func (s *RedisStore) PutTOTPSecret(ctx context.Context, userID, secret string) error {
	return s.client.Set(ctx, totpSecretPrefix+userID, secret, 0).Err()
}

func (s *RedisStore) GetTOTPSecret(ctx context.Context, userID string) (string, error) {
	value, err := s.client.Get(ctx, totpSecretPrefix+userID).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return value, err
}

func (s *RedisStore) DeleteTOTP(ctx context.Context, userID string) error {
	return s.client.Del(ctx, totpSecretPrefix+userID, recoveryPrefix+userID).Err()
}

func (s *RedisStore) PutTOTPSetup(ctx context.Context, token, secret string, ttl time.Duration) error {
	return s.client.Set(ctx, totpSetupPrefix+token, secret, ttl).Err()
}

func (s *RedisStore) ConsumeTOTPSetup(ctx context.Context, token string) (string, error) {
	value, err := s.client.GetDel(ctx, totpSetupPrefix+token).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return value, err
}

func (s *RedisStore) PutLoginSession(ctx context.Context, token, userID string, ttl time.Duration) error {
	return s.client.Set(ctx, loginSessionPrefix+token, userID, ttl).Err()
}

func (s *RedisStore) ConsumeLoginSession(ctx context.Context, token string) (string, error) {
	value, err := s.client.GetDel(ctx, loginSessionPrefix+token).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return value, err
}

func (s *RedisStore) ReplaceRecoveryDigests(ctx context.Context, userID string, digests []string) error {
	key := recoveryPrefix + userID
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		if len(digests) > 0 {
			values := make([]any, len(digests))
			for i := range digests {
				values[i] = digests[i]
			}
			pipe.SAdd(ctx, key, values...)
		}
		return nil
	})
	return err
}

func (s *RedisStore) ConsumeRecoveryDigest(ctx context.Context, userID, digest string) (bool, error) {
	removed, err := s.client.SRem(ctx, recoveryPrefix+userID, digest).Result()
	return removed == 1, err
}
