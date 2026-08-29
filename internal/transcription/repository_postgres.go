package transcription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const recordColumns = `id, sender_id, content_sha256, original_filename, content_type, byte_size, audio_duration_seconds, storage_path, language, status, transcript, detected_language, target_language, translated_text, failure_reason, created_at, updated_at, processing_started_at, completed_at`

func scanRecord(row pgx.Row) (r Record, err error) {
	var durationSeconds *float64
	err = row.Scan(&r.ID, &r.SenderID, &r.ContentSHA256, &r.OriginalFilename, &r.ContentType, &r.ByteSize, &durationSeconds, &r.StoragePath, &r.Language, &r.Status, &r.Transcript, &r.DetectedLanguage, &r.TargetLanguage, &r.TranslatedText, &r.FailureReason, &r.CreatedAt, &r.UpdatedAt, &r.ProcessingAt, &r.CompletedAt)
	if durationSeconds != nil {
		r.AudioDuration = time.Duration(*durationSeconds * float64(time.Second))
	}
	return r, err
}

func (r *PostgresRepository) CreateOrGetAccepted(ctx context.Context, item Record) (Record, bool, error) {
	if item.SenderID == nil {
		return Record{}, false, ErrSenderNotFound
	}
	senderID := *item.SenderID
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Record{}, false, err
	}
	defer tx.Rollback(ctx)
	var role access.Role
	var status string
	err = tx.QueryRow(ctx, `SELECT role, access_status FROM whatsapp_senders WHERE id = $1 FOR UPDATE`, senderID).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, ErrSenderNotFound
	}
	if err != nil {
		return Record{}, false, err
	}
	if status != access.Active {
		return Record{}, false, ErrSenderDisabled
	}
	if role == access.AdminRole {
		return Record{}, false, ErrSenderAdmin
	}
	policy, ok := access.PolicyFor(role)
	if !ok {
		return Record{}, false, ErrSenderDisabled
	}
	if item.AudioDuration > policy.MaxDuration {
		return Record{}, false, ErrAudioTooLong
	}
	var existing Record
	existing, existingErr := scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM transcriptions WHERE sender_id = $1 AND content_sha256 = $2`, senderID, item.ContentSHA256))
	if existingErr == nil {
		return existing, false, tx.Commit(ctx)
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) && !errors.Is(existingErr, ErrNotFound) {
		return Record{}, false, existingErr
	}
	if role == access.PermittedUserRole {
		var reserved int
		err = tx.QueryRow(ctx, `INSERT INTO daily_quota_usage (sender_id, usage_date, reserved_requests) VALUES ($1, CURRENT_DATE, 1) ON CONFLICT (sender_id, usage_date) DO UPDATE SET reserved_requests = daily_quota_usage.reserved_requests + 1, updated_at = NOW() WHERE daily_quota_usage.reserved_requests < $2 RETURNING reserved_requests`, senderID, policy.MaxMessagesPerDay).Scan(&reserved)
		if errors.Is(err, pgx.ErrNoRows) {
			return Record{}, false, ErrDailyQuota
		}
		if err != nil {
			return Record{}, false, err
		}
	} else if role == access.RecruiterRole {
		var used int
		err = tx.QueryRow(ctx, `UPDATE whatsapp_senders SET recruiter_audio_messages_used = recruiter_audio_messages_used + 1, access_status = CASE WHEN recruiter_audio_messages_used + 1 >= $2 THEN 'DISABLED' ELSE 'ACTIVE' END, disabled_at = CASE WHEN recruiter_audio_messages_used + 1 >= $2 THEN NOW() ELSE NULL END, updated_at = NOW() WHERE id = $1 AND recruiter_audio_messages_used < $2 RETURNING recruiter_audio_messages_used`, senderID, policy.MaxMessagesTotal).Scan(&used)
		if errors.Is(err, pgx.ErrNoRows) {
			return Record{}, false, ErrRecruiterQuota
		}
		if err != nil {
			return Record{}, false, err
		}
	}
	query := `INSERT INTO transcriptions (id, sender_id, content_sha256, original_filename, content_type, byte_size, audio_duration_seconds, storage_path, language, status) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'auto', 'PENDING') RETURNING ` + recordColumns
	output, err := scanRecord(tx.QueryRow(ctx, query, item.ID, senderID, item.ContentSHA256, item.OriginalFilename, item.ContentType, item.ByteSize, item.AudioDuration.Seconds(), item.StoragePath))
	if err != nil {
		return Record{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, false, err
	}
	return output, true, nil
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Record, error) {
	item, err := scanRecord(r.pool.QueryRow(ctx, `SELECT `+recordColumns+` FROM transcriptions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return item, err
}

var ErrNotFound = fmt.Errorf("transcription not found")
