package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/alimohamed/hadal/internal/transcription"
	"github.com/alimohamed/hadal/internal/translation"
)

type TranscriptionService interface {
	SubmitWhatsApp(context.Context, int64, string, io.Reader, string, string) (transcription.Record, error)
}

type TranslationService interface {
	Submit(context.Context, int64, string, string) (translation.Record, error)
}

type MessageRateLimiter interface {
	AllowMessage(context.Context, string, string) (bool, error)
	AllowRegistration(context.Context, string, string) (bool, error)
}

type Processor struct {
	repository    Repository
	client        Client
	access        access.Repository
	rateLimiter   MessageRateLimiter
	transcription TranscriptionService
	translation   TranslationService
	maxMediaBytes int64
	maxAttempts   int
}

type processResult struct {
	reply    string
	deferred bool
}

func NewProcessor(repository Repository, client Client, accessRepository access.Repository, rateLimiter MessageRateLimiter, transcriptionService TranscriptionService, translationService TranslationService, maxMediaBytes int64, maxAttempts int) *Processor {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &Processor{repository: repository, client: client, access: accessRepository, rateLimiter: rateLimiter, transcription: transcriptionService, translation: translationService, maxMediaBytes: maxMediaBytes, maxAttempts: maxAttempts}
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	if reconciled, err := p.repository.ReconcileInbound(ctx, p.maxAttempts); err != nil {
		return false, fmt.Errorf("reconcile inbound whatsapp message: %w", err)
	} else if reconciled {
		return true, nil
	}
	message, err := p.repository.ClaimInbound(ctx, p.maxAttempts)
	if errors.Is(err, ErrNoWork) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim inbound whatsapp message: %w", err)
	}

	result, processErr := p.process(ctx, message)
	if processErr != nil && IsTransient(processErr) && message.Attempts < p.maxAttempts {
		if err := p.repository.RetryInbound(ctx, message.ID, processErr.Error()); err != nil {
			return true, fmt.Errorf("retry inbound whatsapp message: %w", err)
		}
		return true, nil
	}
	if processErr != nil {
		result.reply = userMessage(processErr)
	}
	if result.deferred {
		return true, nil
	}
	var outbound *OutboundMessage
	if result.reply != "" {
		outbound = &OutboundMessage{Sender: message.Sender, ReplyTo: message.ProviderMessageID, Text: result.reply}
	}
	if err := p.repository.CompleteInbound(ctx, message.ID, outbound); err != nil {
		return true, fmt.Errorf("complete inbound whatsapp message: %w", err)
	}
	return true, nil
}

func (p *Processor) Runner(interval time.Duration) Runner {
	return Runner{Process: p.ProcessOne, Interval: interval}
}

func (p *Processor) process(ctx context.Context, message InboundMessage) (processResult, error) {
	switch message.Type {
	case "registration_permitted":
		return p.processRegistrationAttempt(ctx, message, access.PermittedUserRole)
	case "registration_recruiter":
		return p.processRegistrationAttempt(ctx, message, access.RecruiterRole)
	case "registration_invalid":
		return p.processRegistrationAttempt(ctx, message, "")
	case "text":
		return p.processText(ctx, message)
	case "audio":
		return p.processAudio(ctx, message)
	default:
		return processResult{reply: "Hadal currently supports four-digit registration codes, text messages, and voice notes."}, nil
	}
}

func (p *Processor) processText(ctx context.Context, message InboundMessage) (processResult, error) {
	allowed, err := p.rateLimiter.AllowMessage(ctx, message.Sender, message.ProviderMessageID)
	if err != nil {
		return processResult{}, fmt.Errorf("check text rate limit: %w", err)
	}
	if !allowed {
		return processResult{reply: "Too many text messages. Please wait a minute and try again."}, nil
	}
	if _, err := p.translation.Submit(ctx, message.ID, message.Sender, message.Text); err != nil {
		if isTranslationSubmissionError(err) {
			return processResult{}, permanent(err)
		}
		return processResult{}, fmt.Errorf("submit text translation: %w", err)
	}
	return processResult{deferred: true}, nil
}

func (p *Processor) processRegistrationAttempt(ctx context.Context, message InboundMessage, role access.Role) (processResult, error) {
	allowed, err := p.rateLimiter.AllowRegistration(ctx, message.Sender, message.ProviderMessageID)
	if err != nil {
		return processResult{}, fmt.Errorf("check registration rate limit: %w", err)
	}
	if !allowed {
		return processResult{reply: "Too many registration attempts. Please try again tomorrow."}, nil
	}
	if role == "" {
		return processResult{reply: "That registration code is invalid."}, nil
	}
	return p.processRegistration(ctx, message, role)
}

func (p *Processor) processRegistration(ctx context.Context, message InboundMessage, role access.Role) (processResult, error) {
	countryCode, err := access.CountryCode(message.Sender)
	if err != nil {
		return processResult{}, permanent(err)
	}
	registered, err := p.access.Register(ctx, message.Sender, countryCode, role)
	if err != nil {
		if errors.Is(err, access.ErrAdmin) || errors.Is(err, access.ErrRoleDowngrade) {
			return processResult{reply: "This registration is not permitted for your account."}, nil
		}
		return processResult{}, fmt.Errorf("register whatsapp sender: %w", err)
	}
	welcome, _ := access.WelcomeMessage(registered.Role)
	return processResult{reply: welcome}, nil
}

func (p *Processor) processAudio(ctx context.Context, message InboundMessage) (processResult, error) {
	sender, err := p.access.Get(ctx, message.Sender)
	if errors.Is(err, access.ErrSenderNotFound) {
		return processResult{}, permanent(transcription.ErrSenderNotFound)
	}
	if err != nil {
		return processResult{}, fmt.Errorf("authorize whatsapp sender: %w", err)
	}
	if sender.Status != access.Active {
		return processResult{}, permanent(transcription.ErrSenderDisabled)
	}
	if _, ok := access.PolicyFor(sender.Role); !ok {
		if sender.Role == access.AdminRole {
			return processResult{}, permanent(transcription.ErrSenderAdmin)
		}
		return processResult{}, permanent(transcription.ErrSenderDisabled)
	}
	allowed, err := p.rateLimiter.AllowMessage(ctx, message.Sender, message.ProviderMessageID)
	if err != nil {
		return processResult{}, fmt.Errorf("check audio rate limit: %w", err)
	}
	if !allowed {
		return processResult{reply: "Too many voice notes. Please wait a minute and try again."}, nil
	}
	if message.MediaID == "" {
		return processResult{}, permanent(errors.New("voice note has no media ID"))
	}
	media, err := p.client.ResolveMedia(ctx, message.MediaID)
	if err != nil {
		return processResult{}, fmt.Errorf("resolve whatsapp media: %w", err)
	}
	reader, err := p.client.DownloadMedia(ctx, media, p.maxMediaBytes)
	if err != nil {
		return processResult{}, fmt.Errorf("download whatsapp media: %w", err)
	}
	defer reader.Close()
	if media.ID == "" {
		media.ID = message.MediaID
	}
	contentType := media.MIMEType
	if contentType == "" {
		contentType = message.MIMEType
	}
	filename := media.ID + extensionForMIME(contentType)
	_, err = p.transcription.SubmitWhatsApp(ctx, message.ID, message.Sender, reader, filename, contentType)
	if err != nil {
		if isPolicyError(err) {
			return processResult{}, permanent(err)
		}
		return processResult{}, fmt.Errorf("submit voice note: %w", err)
	}
	return processResult{deferred: true}, nil
}

func extensionForMIME(contentType string) string {
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	known := map[string]string{
		"audio/ogg":  ".ogg",
		"audio/opus": ".opus",
		"audio/mpeg": ".mp3",
		"audio/mp4":  ".mp4",
		"audio/wav":  ".wav",
		"audio/webm": ".webm",
	}
	if extension := known[contentType]; extension != "" {
		return extension
	}
	extensions, _ := mime.ExtensionsByType(contentType)
	if len(extensions) > 0 {
		return extensions[0]
	}
	if contentType == "audio/ogg" {
		return ".ogg"
	}
	return filepath.Ext("media.bin")
}

func isPolicyError(err error) bool {
	return errors.Is(err, transcription.ErrUnsupportedAudio) ||
		errors.Is(err, transcription.ErrAudioTooLong) ||
		errors.Is(err, transcription.ErrSenderNotFound) ||
		errors.Is(err, transcription.ErrSenderDisabled) ||
		errors.Is(err, transcription.ErrSenderAdmin) ||
		errors.Is(err, transcription.ErrDailyQuota) ||
		errors.Is(err, transcription.ErrGlobalQuota) ||
		errors.Is(err, transcription.ErrRecruiterQuota) ||
		errors.Is(err, transcription.ErrDurationProbe)
}

func isTranslationSubmissionError(err error) bool {
	return errors.Is(err, translation.ErrEmptySource) ||
		errors.Is(err, translation.ErrSourceTooLong) ||
		errors.Is(err, translation.ErrSenderNotFound) ||
		errors.Is(err, translation.ErrSenderDisabled) ||
		errors.Is(err, translation.ErrSenderAdmin) ||
		errors.Is(err, translation.ErrDailyQuota) ||
		errors.Is(err, translation.ErrGlobalQuota) ||
		errors.Is(err, translation.ErrRecruiterQuota) ||
		errors.Is(err, translation.ErrInvalidInbound)
}

func userMessage(err error) string {
	switch {
	case errors.Is(err, ErrMediaTooLarge):
		return "That voice note is too large. Please send a shorter one."
	case errors.Is(err, transcription.ErrUnsupportedAudio):
		return "That voice-note format is not supported."
	case errors.Is(err, transcription.ErrAudioTooLong):
		return "That voice note exceeds your allowed duration."
	case errors.Is(err, transcription.ErrSenderNotFound):
		return "Register with your four-digit code before sending voice notes."
	case errors.Is(err, transcription.ErrSenderDisabled), errors.Is(err, transcription.ErrRecruiterQuota):
		return "You've used all 3 translation requests. This access does not reset."
	case errors.Is(err, transcription.ErrSenderAdmin):
		return "Voice notes are not enabled for this account."
	case errors.Is(err, transcription.ErrDailyQuota):
		return "You've used today's 10 translation requests. Your limit resets at midnight UTC."
	case errors.Is(err, transcription.ErrGlobalQuota):
		return "Hadal has reached today's processing limit. Please try again tomorrow."
	case errors.Is(err, transcription.ErrDurationProbe):
		return "The voice-note duration could not be read."
	case errors.Is(err, translation.ErrEmptySource):
		return "Send some Somali or English text to translate."
	case errors.Is(err, translation.ErrSourceTooLong):
		return "That text is too long. Please send a shorter message."
	case errors.Is(err, translation.ErrSenderNotFound):
		return "Register with your four-digit code before sending text."
	case errors.Is(err, translation.ErrSenderDisabled), errors.Is(err, translation.ErrRecruiterQuota):
		return "You've used all 3 translation requests. This access does not reset."
	case errors.Is(err, translation.ErrSenderAdmin):
		return "Text translation is not enabled for this account."
	case errors.Is(err, translation.ErrDailyQuota):
		return "You've used today's 10 translation requests. Your limit resets at midnight UTC."
	case errors.Is(err, translation.ErrGlobalQuota):
		return "Hadal has reached today's processing limit. Please try again tomorrow."
	default:
		return "We could not process that message. Please try again later."
	}
}
