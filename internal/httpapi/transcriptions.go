package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/alimohamed/hadal/internal/transcription"
	"github.com/google/uuid"
)

type Handler struct {
	service        *transcription.Service
	logger         *slog.Logger
	maxUploadBytes int64
	access         access.Repository
	codes          access.RegistrationCodes
	rateLimiter    transcription.RateLimiter
}
type createResponse struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
}
type transcriptionResponse struct {
	ID             uuid.UUID  `json:"id"`
	Status         string     `json:"status"`
	Language       *string    `json:"language,omitempty"`
	TargetLanguage *string    `json:"targetLanguage,omitempty"`
	Transcript     *string    `json:"transcript,omitempty"`
	Text           *string    `json:"text,omitempty"`
	Error          *string    `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	ProcessingAt   *time.Time `json:"processingAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

func NewHandler(service *transcription.Service, logger *slog.Logger, maxUploadBytes int64, repository access.Repository, codes access.RegistrationCodes, rateLimiter transcription.RateLimiter) *Handler {
	return &Handler{service: service, logger: logger, maxUploadBytes: maxUploadBytes, access: repository, codes: codes, rateLimiter: rateLimiter}
}
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/transcriptions", h.create)
	mux.HandleFunc("POST /api/registrations", h.register)
	mux.HandleFunc("GET /api/transcriptions/{id}", h.get)
	return mux
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("transcription requested")
	sender, ok := senderFromRequest(w, r)
	if !ok {
		return
	}
	allowed, err := h.rateLimiter.Allow(r.Context(), sender)
	if err != nil {
		h.logger.Error("rate limit unavailable", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unable to check the upload rate limit")
		return
	}
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "Too many upload requests. Please wait a minute and try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	if err := r.ParseMultipartForm(h.maxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		file, header, err = r.FormFile("audio")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "audio file is required")
		return
	}
	defer file.Close()
	item, err := h.service.Submit(r.Context(), sender, file, header.Filename, header.Header.Get("Content-Type"))
	if err != nil {
		switch {
		case errors.Is(err, transcription.ErrUnsupportedAudio):
			writeError(w, http.StatusBadRequest, "The audio type is not supported.")
		case errors.Is(err, transcription.ErrSenderNotFound):
			writeError(w, http.StatusUnauthorized, "This sender is not registered.")
		case errors.Is(err, transcription.ErrSenderDisabled):
			writeError(w, http.StatusForbidden, "This sender is disabled.")
		case errors.Is(err, transcription.ErrSenderAdmin):
			writeError(w, http.StatusForbidden, "Administrator uploads are not enabled.")
		case errors.Is(err, transcription.ErrAudioTooLong):
			writeError(w, http.StatusBadRequest, "The audio exceeds your allowed duration.")
		case errors.Is(err, transcription.ErrDailyQuota):
			writeError(w, http.StatusTooManyRequests, "You have reached your daily message limit.")
		case errors.Is(err, transcription.ErrRecruiterQuota):
			writeError(w, http.StatusForbidden, "You have reached your lifetime message limit.")
		case errors.Is(err, transcription.ErrDurationProbe):
			writeError(w, http.StatusBadRequest, "The audio duration could not be determined.")
		default:
			h.logger.Error("transcription submission failed", "error", err)
			writeError(w, http.StatusServiceUnavailable, "unable to queue transcription")
		}
		return
	}
	h.logger.Info("transcription job queued", "transcription_id", item.ID)
	writeJSON(w, http.StatusAccepted, createResponse{ID: item.ID, Status: item.Status})
}

type registrationRequest struct {
	Code string `json:"code"`
}
type registrationResponse struct {
	Role    access.Role `json:"role"`
	Message string      `json:"message"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	sender, ok := senderFromRequest(w, r)
	if !ok {
		return
	}
	defer r.Body.Close()
	var input registrationRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Code == "" {
		writeError(w, http.StatusBadRequest, "A JSON registration code is required.")
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "A JSON registration code is required.")
		return
	}
	role, err := h.codes.RoleFor(input.Code)
	if errors.Is(err, access.ErrInvalidRegistrationCode) {
		writeError(w, http.StatusUnauthorized, "The registration code is invalid.")
		return
	}
	if err != nil {
		h.logger.Error("registration code configuration failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "Registration is unavailable.")
		return
	}
	countryCode, err := access.CountryCode(sender)
	if errors.Is(err, access.ErrInvalidPhone) {
		writeError(w, http.StatusBadRequest, "X-Hadal-Sender must be a valid E.164 phone number.")
		return
	}
	if err != nil {
		h.logger.Error("sender country-code lookup failed", "error", err)
		writeError(w, http.StatusBadRequest, "X-Hadal-Sender must be a valid E.164 phone number.")
		return
	}
	registered, err := h.access.Register(r.Context(), sender, countryCode, role)
	if errors.Is(err, access.ErrAdmin) {
		writeError(w, http.StatusForbidden, "Administrators cannot register with an access code.")
		return
	}
	if errors.Is(err, access.ErrRoleDowngrade) {
		writeError(w, http.StatusForbidden, "Full users cannot register as recruiters.")
		return
	}
	if err != nil {
		h.logger.Error("registration failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "Registration is unavailable.")
		return
	}
	message, _ := access.WelcomeMessage(registered.Role)
	writeJSON(w, http.StatusOK, registrationResponse{Role: registered.Role, Message: message})
}

func senderFromRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	sender := strings.TrimSpace(r.Header.Get("X-Hadal-Sender"))
	if len(sender) < 8 || len(sender) > 16 || sender[0] != '+' {
		writeError(w, http.StatusBadRequest, "X-Hadal-Sender must be an E.164 phone number.")
		return "", false
	}
	for _, character := range sender[1:] {
		if character < '0' || character > '9' {
			writeError(w, http.StatusBadRequest, "X-Hadal-Sender must be an E.164 phone number.")
			return "", false
		}
	}
	return sender, true
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/api/transcriptions/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transcription id")
		return
	}
	item, err := h.service.Get(r.Context(), id)
	if errors.Is(err, transcription.ErrNotFound) {
		writeError(w, http.StatusNotFound, "transcription not found")
		return
	}
	if err != nil {
		h.logger.Error("transcription lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "unable to load transcription")
		return
	}
	writeJSON(w, http.StatusOK, responseFromRecord(item))
}
func responseFromRecord(item transcription.Record) transcriptionResponse {
	return transcriptionResponse{ID: item.ID, Status: item.Status, Language: item.DetectedLanguage, TargetLanguage: item.TargetLanguage, Transcript: item.Transcript, Text: item.TranslatedText, Error: item.FailureReason, CreatedAt: item.CreatedAt, ProcessingAt: item.ProcessingAt, CompletedAt: item.CompletedAt}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
