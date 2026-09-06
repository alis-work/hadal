package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/alimohamed/hadal/internal/whatsapp"
)

type webhookStore struct {
	messages []whatsapp.InboundMessage
	inserted bool
}

func (s *webhookStore) SaveInbound(_ context.Context, message whatsapp.InboundMessage) (bool, error) {
	s.messages = append(s.messages, message)
	return s.inserted, nil
}

func TestWebhookVerificationReturnsChallenge(t *testing.T) {
	webhook := NewWebhook(slog.New(slog.NewTextHandler(io.Discard, nil)), "verify-token")
	request := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token=verify-token&hub.challenge=challenge", nil)
	response := httptest.NewRecorder()

	webhook.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "challenge" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWebhookVerificationRejectsInvalidToken(t *testing.T) {
	webhook := NewWebhook(slog.New(slog.NewTextHandler(io.Discard, nil)), "verify-token")
	request := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=challenge", nil)
	response := httptest.NewRecorder()

	webhook.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWebhookAcceptsValidSignatureAndPersistsNormalizedMessage(t *testing.T) {
	body := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.1","from":"252611234567","type":"audio","audio":{"id":"media-1","mime_type":"audio/ogg; codecs=opus"}}]}}]}]}`)
	store := &webhookStore{inserted: true}
	webhook := NewWebhook(slog.New(slog.NewTextHandler(io.Discard, nil)), "verify-token", WithWebhookAppSecret("secret"), WithWebhookInboundStore(store), WithWebhookRegistrationCodes(access.RegistrationCodes{PermittedUser: "1234", Recruiter: "5678"}))
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", signature(body, "secret"))
	response := httptest.NewRecorder()

	webhook.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if len(store.messages) != 1 {
		t.Fatalf("saved messages=%d", len(store.messages))
	}
	message := store.messages[0]
	if message.ProviderMessageID != "wamid.1" || message.Sender != "+252611234567" || message.Type != "audio" || message.MediaID != "media-1" || message.MIMEType != "audio/ogg; codecs=opus" {
		t.Fatalf("saved message=%+v", message)
	}
}

func TestWebhookClassifiesRegistrationWithoutPersistingCode(t *testing.T) {
	body := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.code","from":"252611234567","type":"text","text":{"body":"1234"}}]}}]}]}`)
	store := &webhookStore{inserted: true}
	webhook := NewWebhook(slog.New(slog.NewTextHandler(io.Discard, nil)), "verify-token", WithWebhookAppSecret("secret"), WithWebhookInboundStore(store), WithWebhookRegistrationCodes(access.RegistrationCodes{PermittedUser: "1234", Recruiter: "5678"}))
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", signature(body, "secret"))
	response := httptest.NewRecorder()

	webhook.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(store.messages) != 1 {
		t.Fatalf("status=%d messages=%+v", response.Code, store.messages)
	}
	if store.messages[0].Type != "registration_permitted" || store.messages[0].Text != "" {
		t.Fatalf("registration secret was not removed: %+v", store.messages[0])
	}
}

func TestWebhookRejectsInvalidSignature(t *testing.T) {
	body := []byte(`{"entry":[]}`)
	webhook := NewWebhook(slog.New(slog.NewTextHandler(io.Discard, nil)), "verify-token", WithWebhookAppSecret("secret"))
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", signature(body, "wrong-secret"))
	response := httptest.NewRecorder()

	webhook.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWebhookDuplicateAndStatusPayloadsAreSuccessful(t *testing.T) {
	messageBody := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.duplicate","from":"+252611234567","type":"text","text":{"body":"1234"}}]}}]}]}`)
	store := &webhookStore{inserted: false}
	webhook := NewWebhook(slog.New(slog.NewTextHandler(io.Discard, nil)), "verify-token", WithWebhookAppSecret("secret"), WithWebhookInboundStore(store), WithWebhookRegistrationCodes(access.RegistrationCodes{PermittedUser: "1234", Recruiter: "5678"}))
	for _, body := range [][]byte{messageBody, []byte(`{"entry":[{"changes":[{"value":{"statuses":[{"id":"wamid.sent","status":"sent"}]}}]}]}`)} {
		request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
		request.Header.Set("X-Hub-Signature-256", signature(body, "secret"))
		response := httptest.NewRecorder()
		webhook.Routes().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
	}
	if len(store.messages) != 1 {
		t.Fatalf("save calls=%d", len(store.messages))
	}
}

func signature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
