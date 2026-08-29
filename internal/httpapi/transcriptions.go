package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alimohamed/hadal/internal/transcription"
	"github.com/google/uuid"
)

type Handler struct {
	service        *transcription.Service
	logger         *slog.Logger
	maxUploadBytes int64
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

func NewHandler(service *transcription.Service, logger *slog.Logger, maxUploadBytes int64) *Handler {
	return &Handler{service: service, logger: logger, maxUploadBytes: maxUploadBytes}
}
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/transcriptions", h.create)
	mux.HandleFunc("GET /api/transcriptions/{id}", h.get)
	return mux
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("transcription requested")
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
	item, err := h.service.Submit(r.Context(), file, header.Filename, header.Header.Get("Content-Type"))
	if err != nil {
		if errors.Is(err, transcription.ErrUnsupportedAudio) {
			writeError(w, http.StatusBadRequest, "unsupported audio type")
			return
		}
		h.logger.Error("transcription submission failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unable to queue transcription")
		return
	}
	h.logger.Info("transcription job queued", "transcription_id", item.ID)
	writeJSON(w, http.StatusAccepted, createResponse{ID: item.ID, Status: item.Status})
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
