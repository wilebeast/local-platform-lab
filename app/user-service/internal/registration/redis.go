package registration

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const registrationChallengeKeyPrefix = "user:registration:challenge:"

type redisChallengeCommands interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

type RedisChallengeStore struct {
	commands redisChallengeCommands
}

type registrationChallengePayload struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
}

func NewRedisChallengeStore(commands redisChallengeCommands) *RedisChallengeStore {
	return &RedisChallengeStore{commands: commands}
}

func (s *RedisChallengeStore) Save(ctx context.Context, challenge RegistrationChallenge, ttl time.Duration) error {
	if challenge.TokenDigest == "" || challenge.Email == "" || challenge.Purpose == "" || ttl <= 0 {
		return errors.New("registration challenge and positive TTL are required")
	}
	payload, err := json.Marshal(registrationChallengePayload{Email: challenge.Email, Purpose: challenge.Purpose})
	if err != nil {
		return err
	}
	return s.commands.Set(ctx, registrationChallengeKeyPrefix+challenge.TokenDigest, string(payload), ttl).Err()
}

func (s *RedisChallengeStore) Delete(ctx context.Context, tokenDigest string) error {
	if tokenDigest == "" {
		return errors.New("registration token digest is required")
	}
	return s.commands.Del(ctx, registrationChallengeKeyPrefix+tokenDigest).Err()
}
