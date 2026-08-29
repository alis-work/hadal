package transcription

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RateLimiter interface {
	Allow(context.Context, string) (bool, error)
}

type RedisRateLimiter struct{ client redis.UniversalClient }

func NewRedisRateLimiter(client redis.UniversalClient) *RedisRateLimiter {
	return &RedisRateLimiter{client: client}
}

var rateLimitScript = redis.NewScript(`local count = redis.call('INCR', KEYS[1]); if count == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]); end; if count > tonumber(ARGV[2]) then return 0 end; return 1`)

func (l *RedisRateLimiter) Allow(ctx context.Context, sender string) (bool, error) {
	result, err := rateLimitScript.Run(ctx, l.client, []string{"hadal:upload-rate:" + sender}, 60, 3).Int()
	if err != nil {
		return false, fmt.Errorf("check upload rate limit: %w", err)
	}
	return result == 1, nil
}
