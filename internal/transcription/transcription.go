package transcription

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const (
	Pending    = "PENDING"
	Processing = "PROCESSING"
	Completed  = "COMPLETED"
	Failed     = "FAILED"
)

type Record struct {
	ID               uuid.UUID  `json:"id"`
	ContentSHA256    string     `json:"-"`
	OriginalFilename string     `json:"original_filename"`
	ContentType      string     `json:"content_type"`
	ByteSize         int64      `json:"byte_size"`
	StoragePath      string     `json:"-"`
	Language         string     `json:"language"`
	Status           string     `json:"status"`
	Transcript       *string    `json:"transcript,omitempty"`
	DetectedLanguage *string    `json:"detected_language,omitempty"`
	TargetLanguage   *string    `json:"target_language,omitempty"`
	TranslatedText   *string    `json:"translated_text,omitempty"`
	FailureReason    *string    `json:"failure_reason,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ProcessingAt     *time.Time `json:"processing_started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type Repository interface {
	CreateOrGet(context.Context, Record) (Record, bool, error)
	Get(context.Context, uuid.UUID) (Record, error)
}

type Queue interface {
	Enqueue(context.Context, uuid.UUID) error
}
