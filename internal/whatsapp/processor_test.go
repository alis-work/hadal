package whatsapp

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/alimohamed/hadal/internal/transcription"
)

type fakeRepository struct {
	inbound       InboundMessage
	outbound      OutboundMessage
	completed     *OutboundMessage
	retriedIn     bool
	retriedOut    bool
	failedOut     bool
	markedSent    bool
	claimInbound  error
	claimOutbound error
}

func (r *fakeRepository) SaveInbound(context.Context, InboundMessage) (bool, error) { return true, nil }
func (r *fakeRepository) ReconcileInbound(context.Context, int) (bool, error)       { return false, nil }
func (r *fakeRepository) ClaimInbound(context.Context, int) (InboundMessage, error) {
	return r.inbound, r.claimInbound
}
func (r *fakeRepository) CompleteInbound(_ context.Context, _ int64, message *OutboundMessage) error {
	r.completed = message
	return nil
}
func (r *fakeRepository) RetryInbound(context.Context, int64, string) error {
	r.retriedIn = true
	return nil
}
func (r *fakeRepository) ClaimOutbound(context.Context, int) (OutboundMessage, error) {
	return r.outbound, r.claimOutbound
}
func (r *fakeRepository) MarkOutboundSent(context.Context, int64, string) error {
	r.markedSent = true
	return nil
}
func (r *fakeRepository) RetryOutbound(context.Context, int64, string) error {
	r.retriedOut = true
	return nil
}
func (r *fakeRepository) FailOutbound(context.Context, int64, string) error {
	r.failedOut = true
	return nil
}
func (r *fakeRepository) MarkOutboundUnknown(context.Context, int64, string) error { return nil }

type fakeAccess struct {
	phone       string
	countryCode string
	role        access.Role
	getSender   access.Sender
	getErr      error
}

func (a *fakeAccess) Get(context.Context, string) (access.Sender, error) {
	if a.getSender.Role == "" && a.getErr == nil {
		return access.Sender{Role: access.PermittedUserRole, Status: access.Active}, nil
	}
	return a.getSender, a.getErr
}
func (a *fakeAccess) Register(_ context.Context, phone, countryCode string, role access.Role) (access.Sender, error) {
	a.phone, a.countryCode, a.role = phone, countryCode, role
	return access.Sender{Phone: phone, Role: role, Status: access.Active}, nil
}

type fakeLimiter struct {
	allowed bool
	err     error
	called  bool
}

func (l *fakeLimiter) AllowMessage(context.Context, string, string) (bool, error) {
	l.called = true
	return l.allowed, l.err
}
func (l *fakeLimiter) AllowRegistration(context.Context, string, string) (bool, error) {
	l.called = true
	return l.allowed, l.err
}

type fakeClient struct {
	media      Media
	content    string
	resolveErr error
	sendErr    error
	resolved   bool
	sentPhone  string
	sentTo     string
	sentText   string
}

func (c *fakeClient) ResolveMedia(context.Context, string) (Media, error) {
	c.resolved = true
	return c.media, c.resolveErr
}
func (c *fakeClient) DownloadMedia(context.Context, Media, int64) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(c.content)), nil
}
func (c *fakeClient) SendText(_ context.Context, phone, to, _ string, text string) (string, error) {
	c.sentPhone, c.sentTo, c.sentText = phone, to, text
	return "wamid.outbound", c.sendErr
}

type fakeTranscription struct {
	called      bool
	sender      string
	filename    string
	contentType string
	content     string
	err         error
}

func (s *fakeTranscription) SubmitWhatsApp(_ context.Context, _ int64, sender string, source io.Reader, filename, contentType string) (transcription.Record, error) {
	s.called = true
	s.sender, s.filename, s.contentType = sender, filename, contentType
	data, _ := io.ReadAll(source)
	s.content = string(data)
	return transcription.Record{}, s.err
}

func TestProcessorRegistersFourDigitCodeAndQueuesWelcome(t *testing.T) {
	repository := &fakeRepository{inbound: InboundMessage{ID: 1, Sender: "+252611234567", Type: "registration_permitted", Attempts: 1}}
	accessRepository := &fakeAccess{}
	processor := NewProcessor(repository, &fakeClient{}, accessRepository, &fakeLimiter{allowed: true}, &fakeTranscription{}, 1024, 3)

	worked, err := processor.ProcessOne(context.Background())

	if err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if accessRepository.phone != "+252611234567" || accessRepository.countryCode != "+252" || accessRepository.role != access.PermittedUserRole {
		t.Fatalf("registration phone=%q country=%q role=%q", accessRepository.phone, accessRepository.countryCode, accessRepository.role)
	}
	if repository.completed == nil || !strings.Contains(repository.completed.Text, "Welcome to Hadal") {
		t.Fatalf("outbound=%+v", repository.completed)
	}
}

func TestProcessorRateLimitsBeforeAudioDownload(t *testing.T) {
	repository := &fakeRepository{inbound: InboundMessage{ID: 2, Sender: "+252611234567", Type: "audio", MediaID: "media", Attempts: 1}}
	client := &fakeClient{}
	processor := NewProcessor(repository, client, &fakeAccess{}, &fakeLimiter{allowed: false}, &fakeTranscription{}, 1024, 3)

	_, err := processor.ProcessOne(context.Background())

	if err != nil || client.resolved {
		t.Fatalf("err=%v media resolved=%v", err, client.resolved)
	}
	if repository.completed == nil || !strings.Contains(repository.completed.Text, "wait a minute") {
		t.Fatalf("outbound=%+v", repository.completed)
	}
}

func TestProcessorDownloadsAndSubmitsAudio(t *testing.T) {
	repository := &fakeRepository{inbound: InboundMessage{ID: 3, Sender: "+252611234567", Type: "audio", MediaID: "media", Attempts: 1}}
	client := &fakeClient{media: Media{ID: "media", MIMEType: "audio/ogg"}, content: "OggSaudio"}
	service := &fakeTranscription{}
	processor := NewProcessor(repository, client, &fakeAccess{}, &fakeLimiter{allowed: true}, service, 1024, 3)

	_, err := processor.ProcessOne(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if !service.called || service.sender != "+252611234567" || service.filename != "media.ogg" || service.contentType != "audio/ogg" || service.content != "OggSaudio" {
		t.Fatalf("submission=%+v", service)
	}
	if repository.completed != nil {
		t.Fatalf("unexpected immediate outbound=%+v", repository.completed)
	}
}

func TestProcessorRetriesTransientAudioFailure(t *testing.T) {
	repository := &fakeRepository{inbound: InboundMessage{ID: 4, Sender: "+252611234567", Type: "audio", MediaID: "media", Attempts: 1}}
	client := &fakeClient{resolveErr: errors.New("temporary graph failure")}
	processor := NewProcessor(repository, client, &fakeAccess{}, &fakeLimiter{allowed: true}, &fakeTranscription{}, 1024, 3)

	_, err := processor.ProcessOne(context.Background())

	if err != nil || !repository.retriedIn || repository.completed != nil {
		t.Fatalf("err=%v retried=%v completed=%+v", err, repository.retriedIn, repository.completed)
	}
}

func TestDispatcherSendsAndMarksOutbound(t *testing.T) {
	repository := &fakeRepository{outbound: OutboundMessage{ID: 8, Sender: "+252611234567", Text: "hello", Attempts: 1}}
	client := &fakeClient{}
	dispatcher := NewDispatcher(repository, client, "phone-id", 3)

	worked, err := dispatcher.DispatchOne(context.Background())

	if err != nil || !worked || !repository.markedSent {
		t.Fatalf("worked=%v err=%v marked=%v", worked, err, repository.markedSent)
	}
	if client.sentPhone != "phone-id" || client.sentTo != "+252611234567" || client.sentText != "hello" {
		t.Fatalf("send phone=%q to=%q text=%q", client.sentPhone, client.sentTo, client.sentText)
	}
}

func TestDispatcherRetriesTransientFailure(t *testing.T) {
	repository := &fakeRepository{outbound: OutboundMessage{ID: 9, Sender: "+252611234567", Text: "hello", Attempts: 1}}
	client := &fakeClient{sendErr: errors.New("temporary graph failure")}
	dispatcher := NewDispatcher(repository, client, "phone-id", 3)

	_, err := dispatcher.DispatchOne(context.Background())

	if err != nil || !repository.retriedOut || repository.markedSent || repository.failedOut {
		t.Fatalf("err=%v retried=%v marked=%v failed=%v", err, repository.retriedOut, repository.markedSent, repository.failedOut)
	}
}
