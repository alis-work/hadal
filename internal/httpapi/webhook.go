package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/alimohamed/hadal/internal/whatsapp"
)

const maxWebhookBytes = 1 << 20

type Webhook struct {
	logger      *slog.Logger
	verifyToken string
	appSecret   string
	inbound     whatsapp.InboundStore
	codes       access.RegistrationCodes
}

type WebhookOption func(*Webhook)

func WithWebhookAppSecret(appSecret string) WebhookOption {
	return func(webhook *Webhook) { webhook.appSecret = appSecret }
}

func WithWebhookInboundStore(store whatsapp.InboundStore) WebhookOption {
	return func(webhook *Webhook) { webhook.inbound = store }
}

func WithWebhookRegistrationCodes(codes access.RegistrationCodes) WebhookOption {
	return func(webhook *Webhook) { webhook.codes = codes }
}

func NewWebhook(logger *slog.Logger, verifyToken string, options ...WebhookOption) *Webhook {
	webhook := &Webhook{logger: logger, verifyToken: verifyToken}
	for _, option := range options {
		option(webhook)
	}
	return webhook
}

func (h *Webhook) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /webhook", h.verify)
	mux.HandleFunc("POST /webhook", h.receive)
	return mux
}

func (h *Webhook) verify(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("hub.mode") != "subscribe" || r.URL.Query().Get("hub.verify_token") != h.verifyToken {
		writeError(w, http.StatusForbidden, "Invalid webhook verification token.")
		return
	}
	challenge := r.URL.Query().Get("hub.challenge")
	if challenge == "" {
		writeError(w, http.StatusBadRequest, "Missing webhook challenge.")
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(challenge))
}

func (h *Webhook) receive(w http.ResponseWriter, r *http.Request) {
	if h.appSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "Webhook signing is not configured.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "Webhook payload is too large.")
			return
		}
		writeError(w, http.StatusBadRequest, "Unable to read webhook payload.")
		return
	}
	if !validWebhookSignature(body, r.Header.Get("X-Hub-Signature-256"), h.appSecret) {
		writeError(w, http.StatusUnauthorized, "Invalid webhook signature.")
		return
	}
	messages, err := whatsapp.ParsePayload(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid webhook payload.")
		return
	}
	if len(messages) > 0 && h.inbound == nil {
		writeError(w, http.StatusServiceUnavailable, "Webhook intake is unavailable.")
		return
	}
	for _, message := range messages {
		if message.Type == "text" && isFourDigitMessage(message.Text) {
			role, err := h.codes.RoleFor(strings.TrimSpace(message.Text))
			switch {
			case errors.Is(err, access.ErrInvalidRegistrationCode):
				message.Type = "registration_invalid"
			case err != nil:
				h.logger.Error("registration code configuration failed", "error", err)
				writeError(w, http.StatusServiceUnavailable, "Registration is unavailable.")
				return
			case role == access.PermittedUserRole:
				message.Type = "registration_permitted"
			case role == access.RecruiterRole:
				message.Type = "registration_recruiter"
			}
			message.Text = ""
		}
		if _, err := h.inbound.SaveInbound(r.Context(), message); err != nil {
			h.logger.Error("persist whatsapp message failed", "error", err, "provider_message_id", message.ProviderMessageID)
			writeError(w, http.StatusServiceUnavailable, "Webhook intake is unavailable.")
			return
		}
	}
	h.logger.Info("webhook event accepted", "message_count", len(messages))
	w.WriteHeader(http.StatusOK)
}

func isFourDigitMessage(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 4 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validWebhookSignature(body []byte, header, secret string) bool {
	prefix, encoded, found := strings.Cut(header, "=")
	if !found || prefix != "sha256" {
		return false
	}
	provided, err := hex.DecodeString(encoded)
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}
