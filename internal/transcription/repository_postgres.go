package transcription

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const recordColumns = `id, content_sha256, original_filename, content_type, byte_size, storage_path, language, status, transcript, failure_reason, created_at, updated_at, processing_started_at, completed_at`

func scanRecord(row pgx.Row) (r Record, err error) {
	err = row.Scan(&r.ID, &r.ContentSHA256, &r.OriginalFilename, &r.ContentType, &r.ByteSize, &r.StoragePath, &r.Language, &r.Status, &r.Transcript, &r.FailureReason, &r.CreatedAt, &r.UpdatedAt, &r.ProcessingAt, &r.CompletedAt)
	return r, err
}

func (r *PostgresRepository) CreateOrGet(ctx context.Context, item Record) (Record, bool, error) {
	query := `INSERT INTO transcriptions (id, content_sha256, original_filename, content_type, byte_size, storage_path, language, status)
	VALUES ($1, $2, $3, $4, $5, $6, 'so', 'PENDING')
ON CONFLICT (content_sha256) DO UPDATE SET updated_at = transcriptions.updated_at
RETURNING ` + recordColumns + `, (xmax = 0) AS inserted`
	var output Record
	var inserted bool
	err := r.pool.QueryRow(ctx, query, item.ID, item.ContentSHA256, item.OriginalFilename, item.ContentType, item.ByteSize, item.StoragePath).Scan(&output.ID, &output.ContentSHA256, &output.OriginalFilename, &output.ContentType, &output.ByteSize, &output.StoragePath, &output.Language, &output.Status, &output.Transcript, &output.FailureReason, &output.CreatedAt, &output.UpdatedAt, &output.ProcessingAt, &output.CompletedAt, &inserted)
	return output, inserted, err
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Record, error) {
	item, err := scanRecord(r.pool.QueryRow(ctx, `SELECT `+recordColumns+` FROM transcriptions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return item, err
}

var ErrNotFound = fmt.Errorf("transcription not found")
