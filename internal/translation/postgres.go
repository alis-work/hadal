package translation

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

type PostgresRepository struct {
	pool             *pgxpool.Pool
	globalDailyLimit int
}

func NewPostgresRepository(pool *pgxpool.Pool, globalDailyLimit int) *PostgresRepository {
	if globalDailyLimit <= 0 {
		globalDailyLimit = 100
	}
	return &PostgresRepository{pool: pool, globalDailyLimit: globalDailyLimit}
}

const recordColumns = `translation.id, translation.inbound_message_id, translation.sender_id,
	message.sender_phone, message.whatsapp_message_id, translation.source_text,
	translation.status, translation.provider_attempt_count, translation.detected_language,
	translation.target_language, translation.translated_text, translation.interpreted_source_text,
	translation.clarification_required, translation.clarification_question,
	translation.failure_reason, translation.created_at, translation.updated_at,
	translation.processing_started_at, translation.completed_at`

func scanRecord(row pgx.Row) (record Record, err error) {
	err = row.Scan(&record.ID, &record.InboundMessageID, &record.SenderID,
		&record.SenderPhone, &record.WhatsAppMessageID, &record.SourceText,
		&record.Status, &record.Attempts, &record.SourceLanguage,
		&record.TargetLanguage, &record.TranslatedText, &record.InterpretedSource,
		&record.ClarificationRequired, &record.ClarificationQuestion,
		&record.FailureReason, &record.CreatedAt, &record.UpdatedAt,
		&record.ProcessingStartedAt, &record.CompletedAt)
	return record, err
}

func (r *PostgresRepository) Submit(ctx context.Context, submission Submission) (Record, bool, error) {
	if err := validateSource(submission.SourceText); err != nil {
		return Record{}, false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Record{}, false, err
	}
	defer tx.Rollback(ctx)

	var inboundSender, inboundType, inboundStatus string
	err = tx.QueryRow(ctx, `SELECT sender_phone, message_type, status FROM whatsapp_inbound_messages WHERE id = $1 FOR UPDATE`, submission.InboundMessageID).Scan(&inboundSender, &inboundType, &inboundStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, ErrInvalidInbound
	}
	if err != nil {
		return Record{}, false, err
	}
	if inboundSender != submission.SenderPhone || inboundType != "text" {
		return Record{}, false, ErrInvalidInbound
	}

	existing, existingErr := scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM text_translations AS translation JOIN whatsapp_inbound_messages AS message ON message.id = translation.inbound_message_id WHERE translation.inbound_message_id = $1`, submission.InboundMessageID))
	if existingErr == nil {
		return existing, false, tx.Commit(ctx)
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return Record{}, false, existingErr
	}
	if inboundStatus != Processing {
		return Record{}, false, ErrInvalidInbound
	}

	var senderID int64
	var role access.Role
	var status string
	err = tx.QueryRow(ctx, `SELECT id, role, access_status FROM whatsapp_senders WHERE phone_number = $1 FOR UPDATE`, submission.SenderPhone).Scan(&senderID, &role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, ErrSenderNotFound
	}
	if err != nil {
		return Record{}, false, err
	}
	if status != access.Active {
		return Record{}, false, ErrSenderDisabled
	}
	policy, ok := access.PolicyFor(role)
	if !ok {
		if role == access.AdminRole {
			return Record{}, false, ErrSenderAdmin
		}
		return Record{}, false, ErrSenderDisabled
	}

	var reserved int
	err = tx.QueryRow(ctx, `INSERT INTO global_quota_usage (usage_date, reserved_requests) VALUES (CURRENT_DATE, 1)
		ON CONFLICT (usage_date) DO UPDATE SET reserved_requests = global_quota_usage.reserved_requests + 1, updated_at = NOW()
		WHERE global_quota_usage.reserved_requests < $1 RETURNING reserved_requests`, r.globalDailyLimit).Scan(&reserved)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, ErrGlobalQuota
	}
	if err != nil {
		return Record{}, false, err
	}
	if role == access.PermittedUserRole {
		err = tx.QueryRow(ctx, `INSERT INTO daily_quota_usage (sender_id, usage_date, reserved_requests) VALUES ($1, CURRENT_DATE, 1)
			ON CONFLICT (sender_id, usage_date) DO UPDATE SET reserved_requests = daily_quota_usage.reserved_requests + 1, updated_at = NOW()
			WHERE daily_quota_usage.reserved_requests < $2 RETURNING reserved_requests`, senderID, policy.MaxMessagesPerDay).Scan(&reserved)
		if errors.Is(err, pgx.ErrNoRows) {
			return Record{}, false, ErrDailyQuota
		}
		if err != nil {
			return Record{}, false, err
		}
	} else {
		err = tx.QueryRow(ctx, `UPDATE whatsapp_senders SET paid_messages_used = paid_messages_used + 1,
			access_status = CASE WHEN paid_messages_used + 1 >= $2 THEN 'DISABLED' ELSE 'ACTIVE' END,
			disabled_at = CASE WHEN paid_messages_used + 1 >= $2 THEN NOW() ELSE NULL END, updated_at = NOW()
			WHERE id = $1 AND paid_messages_used < $2 RETURNING paid_messages_used`, senderID, policy.MaxMessagesTotal).Scan(&reserved)
		if errors.Is(err, pgx.ErrNoRows) {
			return Record{}, false, ErrRecruiterQuota
		}
		if err != nil {
			return Record{}, false, err
		}
	}

	record, err := scanRecord(tx.QueryRow(ctx, `INSERT INTO text_translations
		(id, inbound_message_id, sender_id, source_text, status) VALUES ($1, $2, $3, $4, 'PENDING')
		RETURNING id, inbound_message_id, sender_id, $5::text, (SELECT whatsapp_message_id FROM whatsapp_inbound_messages WHERE id = $2), source_text,
		status, provider_attempt_count, detected_language, target_language, translated_text, interpreted_source_text,
		clarification_required, clarification_question, failure_reason, created_at, updated_at, processing_started_at, completed_at`,
		submission.ID, submission.InboundMessageID, senderID, submission.SourceText, submission.SenderPhone))
	if err != nil {
		return Record{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

func (r *PostgresRepository) Claim(ctx context.Context, maxAttempts int, staleAfter time.Duration) (Record, error) {
	row := r.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM text_translations
		WHERE provider_attempt_count < $1 AND ((status = 'PENDING' AND next_attempt_at <= NOW())
			OR (status = 'PROCESSING' AND processing_started_at < NOW() - ($2 * INTERVAL '1 second')))
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE text_translations AS translation SET status = 'PROCESSING', provider_attempt_count = provider_attempt_count + 1,
			processing_started_at = NOW(), next_attempt_at = NULL, failure_reason = NULL, updated_at = NOW()
		FROM candidate WHERE translation.id = candidate.id RETURNING translation.*
	)
	SELECT `+recordColumns+` FROM claimed AS translation JOIN whatsapp_inbound_messages AS message ON message.id = translation.inbound_message_id`, maxAttempts, staleAfter.Seconds())
	record, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNoWork
	}
	return record, err
}

func (r *PostgresRepository) Complete(ctx context.Context, id uuid.UUID, result Result) error {
	if err := result.Validate(); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var senderID, inboundID int64
	var usageDate time.Time
	var whatsappID, sender string
	err = tx.QueryRow(ctx, `UPDATE text_translations SET status = 'COMPLETED', detected_language = $2, target_language = $3,
		translated_text = NULLIF($4, ''), interpreted_source_text = $5, clarification_required = $6, clarification_question = $7,
		next_attempt_at = NULL, completed_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status = 'PROCESSING'
		RETURNING sender_id, inbound_message_id, created_at::date`, id, result.SourceLanguage, result.TargetLanguage,
		result.TranslatedText, result.InterpretedSource, result.ClarificationRequired, result.ClarificationQuestion).Scan(&senderID, &inboundID, &usageDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	global, err := tx.Exec(ctx, `UPDATE global_quota_usage SET completed_requests = completed_requests + 1, updated_at = NOW()
		WHERE usage_date = $1 AND completed_requests < reserved_requests`, usageDate)
	if err != nil || global.RowsAffected() != 1 {
		if err == nil {
			err = errors.New("global completion reservation missing")
		}
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE daily_quota_usage SET completed_requests = completed_requests + 1, updated_at = NOW()
		WHERE sender_id = $1 AND usage_date = $2 AND completed_requests < reserved_requests`, senderID, usageDate)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `UPDATE whatsapp_inbound_messages SET status = 'COMPLETED', failure_reason = NULL,
		next_attempt_at = NULL, processing_started_at = NULL, completed_at = NOW(), updated_at = NOW()
		WHERE id = $1 RETURNING whatsapp_message_id, sender_phone`, inboundID).Scan(&whatsappID, &sender)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_outbound_messages
		(source_message_id, recipient_phone, reply_to_whatsapp_message_id, body) VALUES ($1, $2, $1, $3)
		ON CONFLICT (source_message_id) DO NOTHING`, whatsappID, sender, FormatReply(result)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) Retry(ctx context.Context, id uuid.UUID, _ string, delay time.Duration) error {
	result, err := r.pool.Exec(ctx, `UPDATE text_translations SET status = 'PENDING', failure_reason = NULL,
		processing_started_at = NULL, next_attempt_at = NOW() + ($2 * INTERVAL '1 second'), updated_at = NOW()
		WHERE id = $1 AND status = 'PROCESSING'`, id, delay.Seconds())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) Fail(ctx context.Context, id uuid.UUID, reason string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := failOne(ctx, tx, id, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) FailExhausted(ctx context.Context, maxAttempts int, staleAfter time.Duration) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM text_translations WHERE provider_attempt_count >= $1
		AND (status = 'PENDING' OR (status = 'PROCESSING' AND processing_started_at < NOW() - ($2 * INTERVAL '1 second')))
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`, maxAttempts, staleAfter.Seconds()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if err := failOne(ctx, tx, id, "translation retry limit reached"); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func failOne(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) error {
	var inboundID int64
	err := tx.QueryRow(ctx, `UPDATE text_translations SET status = 'FAILED', failure_reason = $2,
		processing_started_at = COALESCE(processing_started_at, NOW()), next_attempt_at = NULL, completed_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status IN ('PENDING', 'PROCESSING') RETURNING inbound_message_id`, id, truncate(reason)).Scan(&inboundID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var whatsappID, sender string
	err = tx.QueryRow(ctx, `UPDATE whatsapp_inbound_messages SET status = 'FAILED', failure_reason = $2,
		processing_started_at = NULL, next_attempt_at = NULL, completed_at = NULL, updated_at = NOW()
		WHERE id = $1 RETURNING whatsapp_message_id, sender_phone`, inboundID, truncate(reason)).Scan(&whatsappID, &sender)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO whatsapp_outbound_messages
		(source_message_id, recipient_phone, reply_to_whatsapp_message_id, body) VALUES ($1, $2, $1, $3)
		ON CONFLICT (source_message_id) DO NOTHING`, whatsappID, sender, safeFailureReply)
	return err
}

func truncate(value string) string {
	const maximum = 1000
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}

var _ Repository = (*PostgresRepository)(nil)

func (r Record) String() string {
	return fmt.Sprintf("translation %s (%s)", r.ID, r.Status)
}
