package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) SaveInbound(ctx context.Context, message InboundMessage) (bool, error) {
	result, err := r.pool.Exec(ctx, `
		INSERT INTO whatsapp_inbound_messages
			(whatsapp_message_id, sender_phone, message_type, message_text, media_id, media_content_type)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''))
		ON CONFLICT (whatsapp_message_id) DO NOTHING`,
		message.ProviderMessageID, message.Sender, message.Type, message.Text, message.MediaID, message.MIMEType)
	if err != nil {
		return false, fmt.Errorf("insert inbound whatsapp message: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (r *PostgresRepository) ReconcileInbound(ctx context.Context, maxAttempts int) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id int64
	var providerID, sender, transcriptionStatus string
	var transcript, detectedLanguage, targetLanguage, translatedText *string
	err = tx.QueryRow(ctx, `
		SELECT message.id, message.whatsapp_message_id, message.sender_phone,
			COALESCE(transcription.status, ''), transcription.transcript,
			transcription.detected_language, transcription.target_language,
			transcription.translated_text
		FROM whatsapp_inbound_messages AS message
		LEFT JOIN transcriptions AS transcription ON transcription.id = message.transcription_id
		WHERE (message.status = 'PROCESSING' AND transcription.status IN ('COMPLETED', 'FAILED'))
		   OR (message.attempt_count >= $1 AND (
				message.status IN ('PENDING', 'FAILED')
				OR (message.status = 'PROCESSING' AND message.transcription_id IS NULL
					AND message.processing_started_at < NOW() - INTERVAL '5 minutes')))
		ORDER BY message.created_at
		FOR UPDATE OF message SKIP LOCKED
		LIMIT 1`, maxAttempts).Scan(&id, &providerID, &sender, &transcriptionStatus, &transcript, &detectedLanguage, &targetLanguage, &translatedText)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	status, reason := "FAILED", "whatsapp intake retry limit reached"
	body := "We could not process that message. Please try again later."
	if transcriptionStatus == "COMPLETED" && transcript != nil && detectedLanguage != nil && targetLanguage != nil && translatedText != nil {
		status, reason = "COMPLETED", ""
		body = formatResultReply(*transcript, *detectedLanguage, *targetLanguage, *translatedText)
	} else if transcriptionStatus == "FAILED" {
		reason = "audio processing failed"
		body = "Sorry, we could not process your audio. Please try again."
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO whatsapp_outbound_messages
			(source_message_id, recipient_phone, reply_to_whatsapp_message_id, body)
		VALUES ($1, $2, $1, $3)
		ON CONFLICT (source_message_id) DO NOTHING`, providerID, sender, body); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE whatsapp_inbound_messages
		SET status = $2, completed_at = CASE WHEN $2 = 'COMPLETED' THEN NOW() ELSE NULL END,
			processing_started_at = NULL, next_attempt_at = NULL,
			failure_reason = NULLIF($3, ''), updated_at = NOW()
		WHERE id = $1`, id, status, reason)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func formatResultReply(transcript, detectedLanguage, targetLanguage, translatedText string) string {
	languages := map[string]string{"so": "Somali", "en": "English"}
	return fmt.Sprintf("Source transcript:\n%s\n\nDetected language: %s (%s)\nTarget language: %s (%s)\n\nTranslation:\n%s", transcript, languages[detectedLanguage], detectedLanguage, languages[targetLanguage], targetLanguage, translatedText)
}

func (r *PostgresRepository) ClaimInbound(ctx context.Context, maxAttempts int) (InboundMessage, error) {
	row := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM whatsapp_inbound_messages
			WHERE attempt_count < $1
			  AND ((status IN ('PENDING', 'FAILED') AND next_attempt_at <= NOW())
			       OR (status = 'PROCESSING' AND transcription_id IS NULL
			           AND processing_started_at < NOW() - INTERVAL '5 minutes'))
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE whatsapp_inbound_messages AS message
		SET status = 'PROCESSING', attempt_count = attempt_count + 1,
			processing_started_at = NOW(), next_attempt_at = NULL,
			failure_reason = NULL, updated_at = NOW()
		FROM candidate
		WHERE message.id = candidate.id
		RETURNING message.id, message.whatsapp_message_id, message.sender_phone,
			message.message_type, COALESCE(message.message_text, ''),
			COALESCE(message.media_id, ''), COALESCE(message.media_content_type, ''),
			message.attempt_count`, maxAttempts)
	var message InboundMessage
	err := row.Scan(&message.ID, &message.ProviderMessageID, &message.Sender, &message.Type, &message.Text, &message.MediaID, &message.MIMEType, &message.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return InboundMessage{}, ErrNoWork
	}
	return message, err
}

func (r *PostgresRepository) CompleteInbound(ctx context.Context, id int64, outbound *OutboundMessage) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var providerID, sender string
	err = tx.QueryRow(ctx, `
		UPDATE whatsapp_inbound_messages
		SET status = 'COMPLETED', completed_at = NOW(), processing_started_at = NULL,
			next_attempt_at = NULL, failure_reason = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'PROCESSING'
		RETURNING whatsapp_message_id, sender_phone`, id).Scan(&providerID, &sender)
	if err != nil {
		return fmt.Errorf("complete inbound whatsapp message: %w", err)
	}
	if outbound != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO whatsapp_outbound_messages
				(source_message_id, recipient_phone, reply_to_whatsapp_message_id, body)
			VALUES ($1, $2, $1, $3)
			ON CONFLICT (source_message_id) DO NOTHING`, providerID, sender, outbound.Text); err != nil {
			return fmt.Errorf("insert outbound whatsapp reply: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) RetryInbound(ctx context.Context, id int64, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE whatsapp_inbound_messages
		SET status = 'FAILED', failure_reason = $2, processing_started_at = NULL,
			next_attempt_at = NOW() + (LEAST(attempt_count, 5) * INTERVAL '5 seconds'), updated_at = NOW()
		WHERE id = $1 AND status = 'PROCESSING'`, id, truncate(reason))
	return err
}

func (r *PostgresRepository) ClaimOutbound(ctx context.Context, maxAttempts int) (OutboundMessage, error) {
	if _, err := r.pool.Exec(ctx, `
		UPDATE whatsapp_outbound_messages
		SET status = 'UNKNOWN', last_error = 'delivery outcome unknown after interrupted send',
			sending_started_at = NULL, next_attempt_at = NULL, updated_at = NOW()
		WHERE status = 'SENDING' AND sending_started_at < NOW() - INTERVAL '5 minutes'`); err != nil {
		return OutboundMessage{}, err
	}
	row := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM whatsapp_outbound_messages
			WHERE attempt_count < $1
			  AND status IN ('PENDING', 'FAILED') AND next_attempt_at <= NOW()
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE whatsapp_outbound_messages AS message
		SET status = 'SENDING', attempt_count = attempt_count + 1,
			sending_started_at = NOW(), next_attempt_at = NULL,
			last_error = NULL, updated_at = NOW()
		FROM candidate
		WHERE message.id = candidate.id
		RETURNING message.id, message.recipient_phone,
			COALESCE(message.reply_to_whatsapp_message_id, ''), message.body,
			message.attempt_count`, maxAttempts)
	var message OutboundMessage
	err := row.Scan(&message.ID, &message.Sender, &message.ReplyTo, &message.Text, &message.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundMessage{}, ErrNoWork
	}
	return message, err
}

func (r *PostgresRepository) MarkOutboundSent(ctx context.Context, id int64, providerID string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE whatsapp_outbound_messages
		SET status = 'SENT', provider_message_id = $2, sent_at = NOW(),
			sending_started_at = NULL, last_error = NULL, next_attempt_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'SENDING'`, id, providerID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("outbound whatsapp message was not sending")
	}
	return nil
}

func (r *PostgresRepository) RetryOutbound(ctx context.Context, id int64, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE whatsapp_outbound_messages
		SET status = 'FAILED', last_error = $2, sending_started_at = NULL,
			next_attempt_at = NOW() + (LEAST(attempt_count, 5) * INTERVAL '5 seconds'), updated_at = NOW()
		WHERE id = $1 AND status = 'SENDING'`, id, truncate(reason))
	return err
}

func (r *PostgresRepository) FailOutbound(ctx context.Context, id int64, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE whatsapp_outbound_messages
		SET status = 'FAILED', last_error = $2, sending_started_at = NULL,
			next_attempt_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'SENDING'`, id, truncate(reason))
	return err
}

func (r *PostgresRepository) MarkOutboundUnknown(ctx context.Context, id int64, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE whatsapp_outbound_messages
		SET status = 'UNKNOWN', last_error = $2, sending_started_at = NULL,
			next_attempt_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'SENDING'`, id, truncate(reason))
	return err
}

func truncate(value string) string {
	const maximum = 1000
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}
