package translation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	Somali      = "so"
	English     = "en"
	Unsupported = "unsupported"

	Pending    = "PENDING"
	Processing = "PROCESSING"
	Completed  = "COMPLETED"
	Failed     = "FAILED"

	MaxSourceBytes           = 4096
	UnsupportedLanguageReply = "Hadal currently supports Somali and English only."
)

var (
	ErrNoWork         = errors.New("no translation work available")
	ErrNotFound       = errors.New("translation not found")
	ErrEmptySource    = errors.New("source text is empty")
	ErrSourceTooLong  = errors.New("source text exceeds 4096 bytes")
	ErrInvalidResult  = errors.New("invalid translation result")
	ErrSenderNotFound = errors.New("sender is not registered")
	ErrSenderDisabled = errors.New("sender is disabled")
	ErrSenderAdmin    = errors.New("administrator translation is not enabled")
	ErrDailyQuota     = errors.New("daily message limit reached")
	ErrRecruiterQuota = errors.New("recruiter message limit reached")
	ErrGlobalQuota    = errors.New("global daily processing limit reached")
	ErrInvalidInbound = errors.New("inbound whatsapp message is not processable")
)

// Result is provider-neutral. Nullable strings distinguish an omitted value
// from text that failed validation.
type Result struct {
	SourceLanguage        string  `json:"source_language"`
	TargetLanguage        string  `json:"target_language"`
	TranslatedText        string  `json:"translated_text"`
	InterpretedSource     *string `json:"interpreted_source,omitempty"`
	ClarificationRequired bool    `json:"clarification_required"`
	ClarificationQuestion *string `json:"clarification_question,omitempty"`
}

func (r Result) Validate() error {
	if r.SourceLanguage == Unsupported {
		if strings.TrimSpace(r.TargetLanguage) != "" || strings.TrimSpace(r.TranslatedText) != "" || r.InterpretedSource != nil || r.ClarificationRequired || r.ClarificationQuestion != nil {
			return fmt.Errorf("%w: unsupported language must not include translation output", ErrInvalidResult)
		}
		return nil
	}
	if !validPair(r.SourceLanguage, r.TargetLanguage) {
		return fmt.Errorf("%w: languages must be opposite Somali/English pairs", ErrInvalidResult)
	}
	if r.InterpretedSource != nil && strings.TrimSpace(*r.InterpretedSource) == "" {
		return fmt.Errorf("%w: interpreted source must be non-empty when present", ErrInvalidResult)
	}
	translated := strings.TrimSpace(r.TranslatedText)
	if r.ClarificationRequired {
		if translated != "" {
			return fmt.Errorf("%w: ambiguous input must not include a translation", ErrInvalidResult)
		}
		if r.ClarificationQuestion == nil || strings.TrimSpace(*r.ClarificationQuestion) == "" {
			return fmt.Errorf("%w: clarification question is required", ErrInvalidResult)
		}
		return nil
	}
	if translated == "" {
		return fmt.Errorf("%w: translated text is required", ErrInvalidResult)
	}
	if r.ClarificationQuestion != nil {
		return fmt.Errorf("%w: clarification question requires clarification", ErrInvalidResult)
	}
	return nil
}

func validPair(source, target string) bool {
	return source == Somali && target == English || source == English && target == Somali
}

type Provider interface {
	Translate(context.Context, string) (Result, error)
}

type Record struct {
	ID                    uuid.UUID
	InboundMessageID      int64
	SenderID              int64
	SenderPhone           string
	WhatsAppMessageID     string
	SourceText            string
	Status                string
	Attempts              int
	SourceLanguage        *string
	TargetLanguage        *string
	TranslatedText        *string
	InterpretedSource     *string
	ClarificationRequired bool
	ClarificationQuestion *string
	FailureReason         *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ProcessingStartedAt   *time.Time
	CompletedAt           *time.Time
}

type Submission struct {
	ID               uuid.UUID
	InboundMessageID int64
	SenderPhone      string
	SourceText       string
}

// Repository methods own all state transitions. Complete and Fail also update
// the linked inbound message and insert its single idempotent outbox reply.
type Repository interface {
	Submit(context.Context, Submission) (Record, bool, error)
	Claim(context.Context, int, time.Duration) (Record, error)
	Complete(context.Context, uuid.UUID, Result) error
	Retry(context.Context, uuid.UUID, string, time.Duration) error
	Fail(context.Context, uuid.UUID, string) error
	FailExhausted(context.Context, int, time.Duration) (bool, error)
}

type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }
func (e transientError) Unwrap() error { return e.err }

func transient(err error) error {
	if err == nil {
		return nil
	}
	return transientError{err: err}
}

func IsTransient(err error) bool {
	var target transientError
	return errors.As(err, &target)
}
