package transcription

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const StreamName = "transcription-jobs"

type RedisQueue struct{ client redis.UniversalClient }

func NewRedisQueue(client redis.UniversalClient) *RedisQueue { return &RedisQueue{client: client} }
func (q *RedisQueue) Enqueue(ctx context.Context, id uuid.UUID) error {
	if err := q.client.XAdd(ctx, &redis.XAddArgs{Stream: StreamName, Values: map[string]any{"transcription_id": id.String()}}).Err(); err != nil {
		return fmt.Errorf("enqueue transcription: %w", err)
	}
	return nil
}
