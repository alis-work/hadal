package transcription

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alimohamed/hadal/internal/access"
	"github.com/google/uuid"
)

type fakeStorage struct {
	stored  StoredFile
	deleted []string
}

func (s *fakeStorage) Store(context.Context, io.Reader, string) (StoredFile, error) {
	return s.stored, nil
}
func (s *fakeStorage) Delete(_ context.Context, path string) error {
	s.deleted = append(s.deleted, path)
	return nil
}

type fakeRepository struct {
	item     Record
	inserted bool
	err      error
	created  Record
}

func (r *fakeRepository) CreateOrGetAccepted(_ context.Context, item Record) (Record, bool, error) {
	r.created = item
	return r.item, r.inserted, r.err
}
func (r *fakeRepository) Get(context.Context, uuid.UUID) (Record, error) { return r.item, nil }

type fakeQueue struct {
	ids []uuid.UUID
	err error
}

type fakeProber struct {
	duration time.Duration
	err      error
}

func (p fakeProber) Probe(context.Context, string) (time.Duration, error) { return p.duration, p.err }

type fakeSenders struct {
	sender access.Sender
	err    error
}

func (s fakeSenders) Get(context.Context, string) (access.Sender, error) { return s.sender, s.err }
func (s fakeSenders) Register(context.Context, string, string, access.Role) (access.Sender, error) {
	return access.Sender{}, nil
}
func testService(repository Repository, queue Queue, storage Storage) *Service {
	return NewService(repository, queue, storage, fakeProber{duration: time.Second}, fakeSenders{sender: access.Sender{ID: 1, Role: access.PermittedUserRole, Status: access.Active}})
}

func (q *fakeQueue) Enqueue(_ context.Context, id uuid.UUID) error {
	q.ids = append(q.ids, id)
	return q.err
}

func TestSubmitEnqueuesNewUpload(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepository{item: Record{ID: id, Status: Pending}, inserted: true}
	queue := &fakeQueue{}
	service := testService(repo, queue, &fakeStorage{stored: StoredFile{Path: "/tmp/audio", SHA256: strings.Repeat("a", 64), Size: 5}})
	item, err := service.Submit(context.Background(), "+15550000001", strings.NewReader("OggS audio"), "voice.ogg", "audio/ogg")
	if err != nil || item.ID != id || len(queue.ids) != 1 || queue.ids[0] != id || repo.created.ContentSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("unexpected submission: item=%+v queue=%v err=%v", item, queue.ids, err)
	}
}
func TestSubmitDuplicateDoesNotEnqueue(t *testing.T) {
	repo := &fakeRepository{item: Record{ID: uuid.New(), Status: Completed}, inserted: false}
	storage := &fakeStorage{stored: StoredFile{Path: "/tmp/audio", Size: 5, Created: true}}
	queue := &fakeQueue{}
	_, err := testService(repo, queue, storage).Submit(context.Background(), "+15550000001", strings.NewReader("ID3 audio"), "voice.mp3", "audio/mpeg")
	if err != nil || len(queue.ids) != 0 || len(storage.deleted) != 1 {
		t.Fatalf("duplicate must be a no-op: queue=%v deleted=%v err=%v", queue.ids, storage.deleted, err)
	}
}
func TestSubmitDuplicateKeepsExistingAudio(t *testing.T) {
	repo := &fakeRepository{item: Record{ID: uuid.New(), Status: Completed}, inserted: false}
	storage := &fakeStorage{stored: StoredFile{Path: "/tmp/audio", Size: 5, Created: false}}
	_, err := testService(repo, &fakeQueue{}, storage).Submit(context.Background(), "+15550000001", strings.NewReader("OggS audio"), "voice.ogg", "audio/ogg")
	if err != nil || len(storage.deleted) != 0 {
		t.Fatalf("duplicate must retain an existing immutable upload: deleted=%v err=%v", storage.deleted, err)
	}
}
func TestSubmitRejectsUnsupportedAudio(t *testing.T) {
	_, err := testService(&fakeRepository{}, &fakeQueue{}, &fakeStorage{}).Submit(context.Background(), "+15550000001", strings.NewReader("x"), "voice.txt", "text/plain")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported audio error, got %v", err)
	}
}
func TestSubmitDoesNotHideQueueFailure(t *testing.T) {
	queue := &fakeQueue{err: errors.New("redis unavailable")}
	repo := &fakeRepository{item: Record{ID: uuid.New()}, inserted: true}
	_, err := testService(repo, queue, &fakeStorage{stored: StoredFile{Path: "/tmp/audio", Size: 5}}).Submit(context.Background(), "+15550000001", strings.NewReader("ID3 audio"), "voice.mp3", "audio/mpeg")
	if err == nil {
		t.Fatal("expected queue failure")
	}
}

func TestSubmitRejectsLongRecruiterAudioWithoutCreatingRecord(t *testing.T) {
	repository := &fakeRepository{}
	storage := &fakeStorage{stored: StoredFile{Path: "/tmp/audio", Created: true}}
	service := NewService(repository, &fakeQueue{}, storage, fakeProber{duration: 11 * time.Second}, fakeSenders{sender: access.Sender{ID: 1, Role: access.RecruiterRole, Status: access.Active}})
	_, err := service.Submit(context.Background(), "+15550000001", strings.NewReader("OggS audio"), "voice.ogg", "audio/ogg")
	if !errors.Is(err, ErrAudioTooLong) || len(storage.deleted) != 1 || repository.created.ID != uuid.Nil {
		t.Fatalf("long recruiter audio must not be accepted: err=%v deleted=%v record=%+v", err, storage.deleted, repository.created)
	}
}

func TestSubmitRejectsUnregisteredSenderBeforeStorage(t *testing.T) {
	storage := &fakeStorage{}
	service := NewService(&fakeRepository{}, &fakeQueue{}, storage, fakeProber{}, fakeSenders{err: access.ErrSenderNotFound})
	_, err := service.Submit(context.Background(), "+15550000001", strings.NewReader("OggS audio"), "voice.ogg", "audio/ogg")
	if !errors.Is(err, ErrSenderNotFound) || storage.stored.Path != "" {
		t.Fatalf("unregistered sender must be rejected before storage: err=%v", err)
	}
}
