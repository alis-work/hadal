package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/alimohamed/hadal/internal/transcription"
	"github.com/google/uuid"
)

type accessFake struct {
	sender     access.Sender
	registered access.Sender
	err        error
}

func (f *accessFake) Get(context.Context, string) (access.Sender, error) { return f.sender, f.err }
func (f *accessFake) Register(context.Context, string, string, access.Role) (access.Sender, error) {
	return f.registered, f.err
}

type limiterFake struct {
	allowed bool
	calls   int
}

func (l *limiterFake) Allow(context.Context, string) (bool, error) { l.calls++; return l.allowed, nil }

type repositoryFake struct{}

func (repositoryFake) CreateOrGetAccepted(context.Context, transcription.Record) (transcription.Record, bool, error) {
	return transcription.Record{ID: uuid.New(), Status: transcription.Pending}, true, nil
}
func (repositoryFake) Get(context.Context, uuid.UUID) (transcription.Record, error) {
	return transcription.Record{}, transcription.ErrNotFound
}

type queueFake struct{}

func (queueFake) Enqueue(context.Context, uuid.UUID) error { return nil }

type storageFake struct{}

func (storageFake) Store(context.Context, io.Reader, string) (transcription.StoredFile, error) {
	return transcription.StoredFile{}, nil
}
func (storageFake) Delete(context.Context, string) error { return nil }

type proberFake struct{}

func (proberFake) Probe(context.Context, string) (time.Duration, error) { return time.Second, nil }

func testHandler(limiter transcription.RateLimiter, accessRepository access.Repository) *Handler {
	service := transcription.NewService(repositoryFake{}, queueFake{}, storageFake{}, proberFake{}, accessRepository)
	return NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil)), 1024, accessRepository, access.RegistrationCodes{PermittedUser: "1234", Recruiter: "5678"}, limiter)
}

func TestCreateRequiresSenderHeader(t *testing.T) {
	handler := testHandler(&limiterFake{allowed: true}, &accessFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/transcriptions", nil)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "X-Hadal-Sender") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateMapsRateLimitRejection(t *testing.T) {
	limiter := &limiterFake{}
	handler := testHandler(limiter, &accessFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/transcriptions", nil)
	request.Header.Set("X-Hadal-Sender", "+15550000001")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || limiter.calls != 1 || !strings.Contains(response.Body.String(), "Too many upload requests") {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, limiter.calls, response.Body.String())
	}
}

func TestRegistrationReturnsWelcomeMessage(t *testing.T) {
	fake := &accessFake{registered: access.Sender{Role: access.PermittedUserRole, Status: access.Active}}
	handler := testHandler(&limiterFake{allowed: true}, fake)
	request := httptest.NewRequest(http.MethodPost, "/api/registrations", strings.NewReader(`{"code":"1234"}`))
	request.Header.Set("X-Hadal-Sender", "+15550000001")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Welcome to Hadal") || !strings.Contains(response.Body.String(), "PERMITTED_USER") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
