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
var messageRateLimitScript = redis.NewScript(`if redis.call('EXISTS', KEYS[2]) == 1 then return 1 end; local count = redis.call('INCR', KEYS[1]); if count == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]); end; if count > tonumber(ARGV[2]) then return 0 end; redis.call('SET', KEYS[2], '1', 'EX', ARGV[3]); return 1`)

func (l *RedisRateLimiter) Allow(ctx context.Context, sender string) (bool, error) {
	result, err := rateLimitScript.Run(ctx, l.client, []string{"hadal:upload-rate:" + sender}, 60, 3).Int()
	if err != nil {
		return false, fmt.Errorf("check upload rate limit: %w", err)
	}
	return result == 1, nil
}

func (l *RedisRateLimiter) AllowRegistration(ctx context.Context, sender, messageID string) (bool, error) {
	result, err := messageRateLimitScript.Run(ctx, l.client, []string{"hadal:registration-rate:" + sender, "hadal:registration-message:" + messageID}, 86400, 5, 86400).Int()
	if err != nil {
		return false, fmt.Errorf("check registration rate limit: %w", err)
	}
	return result == 1, nil
}

func (l *RedisRateLimiter) AllowMessage(ctx context.Context, sender, messageID string) (bool, error) {
	result, err := messageRateLimitScript.Run(ctx, l.client, []string{"hadal:upload-rate:" + sender, "hadal:upload-message:" + messageID}, 60, 3, 86400).Int()
	if err != nil {
		return false, fmt.Errorf("check upload rate limit: %w", err)
	}
	return result == 1, nil
}
