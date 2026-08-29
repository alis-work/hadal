package transcription

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUnsupportedAudio = errors.New("unsupported audio")
	ErrDurationProbe    = errors.New("could not determine audio duration")
	ErrAudioTooLong     = errors.New("audio exceeds the allowed duration")
	ErrSenderNotFound   = errors.New("sender is not registered")
	ErrSenderDisabled   = errors.New("sender is disabled")
	ErrSenderAdmin      = errors.New("administrator uploads are not enabled")
	ErrDailyQuota       = errors.New("daily message limit reached")
	ErrRecruiterQuota   = errors.New("recruiter message limit reached")
)

const (
	Pending    = "PENDING"
	Processing = "PROCESSING"
	Completed  = "COMPLETED"
	Failed     = "FAILED"
)

type Record struct {
	ID               uuid.UUID     `json:"id"`
	SenderID         *int64        `json:"sender_id,omitempty"`
	ContentSHA256    string        `json:"-"`
	OriginalFilename string        `json:"original_filename"`
	ContentType      string        `json:"content_type"`
	ByteSize         int64         `json:"byte_size"`
	AudioDuration    time.Duration `json:"-"`
	StoragePath      string        `json:"-"`
	Language         string        `json:"language"`
	Status           string        `json:"status"`
	Transcript       *string       `json:"transcript,omitempty"`
	DetectedLanguage *string       `json:"detected_language,omitempty"`
	TargetLanguage   *string       `json:"target_language,omitempty"`
	TranslatedText   *string       `json:"translated_text,omitempty"`
	FailureReason    *string       `json:"failure_reason,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	ProcessingAt     *time.Time    `json:"processing_started_at,omitempty"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
}

type Repository interface {
	CreateOrGetAccepted(context.Context, Record) (Record, bool, error)
	Get(context.Context, uuid.UUID) (Record, error)
}

type Queue interface {
	Enqueue(context.Context, uuid.UUID) error
}
