package transcription

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

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

func (r *fakeRepository) CreateOrGet(_ context.Context, item Record) (Record, bool, error) {
	r.created = item
	return r.item, r.inserted, r.err
}
func (r *fakeRepository) Get(context.Context, uuid.UUID) (Record, error) { return r.item, nil }

type fakeQueue struct {
	ids []uuid.UUID
	err error
}

func (q *fakeQueue) Enqueue(_ context.Context, id uuid.UUID) error {
	q.ids = append(q.ids, id)
	return q.err
}

func TestSubmitEnqueuesNewUpload(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepository{item: Record{ID: id, Status: Pending}, inserted: true}
	queue := &fakeQueue{}
	service := NewService(repo, queue, &fakeStorage{stored: StoredFile{Path: "/tmp/audio", SHA256: strings.Repeat("a", 64), Size: 5}})
	item, err := service.Submit(context.Background(), strings.NewReader("OggS audio"), "voice.ogg", "audio/ogg")
	if err != nil || item.ID != id || len(queue.ids) != 1 || queue.ids[0] != id || repo.created.ContentSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("unexpected submission: item=%+v queue=%v err=%v", item, queue.ids, err)
	}
}
func TestSubmitDuplicateDoesNotEnqueue(t *testing.T) {
	repo := &fakeRepository{item: Record{ID: uuid.New(), Status: Completed}, inserted: false}
	storage := &fakeStorage{stored: StoredFile{Path: "/tmp/audio", Size: 5, Created: true}}
	queue := &fakeQueue{}
	_, err := NewService(repo, queue, storage).Submit(context.Background(), strings.NewReader("ID3 audio"), "voice.mp3", "audio/mpeg")
	if err != nil || len(queue.ids) != 0 || len(storage.deleted) != 1 {
		t.Fatalf("duplicate must be a no-op: queue=%v deleted=%v err=%v", queue.ids, storage.deleted, err)
	}
}
func TestSubmitDuplicateKeepsExistingAudio(t *testing.T) {
	repo := &fakeRepository{item: Record{ID: uuid.New(), Status: Completed}, inserted: false}
	storage := &fakeStorage{stored: StoredFile{Path: "/tmp/audio", Size: 5, Created: false}}
	_, err := NewService(repo, &fakeQueue{}, storage).Submit(context.Background(), strings.NewReader("OggS audio"), "voice.ogg", "audio/ogg")
	if err != nil || len(storage.deleted) != 0 {
		t.Fatalf("duplicate must retain an existing immutable upload: deleted=%v err=%v", storage.deleted, err)
	}
}
func TestSubmitRejectsUnsupportedAudio(t *testing.T) {
	_, err := NewService(&fakeRepository{}, &fakeQueue{}, &fakeStorage{}).Submit(context.Background(), strings.NewReader("x"), "voice.txt", "text/plain")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported audio error, got %v", err)
	}
}
func TestSubmitDoesNotHideQueueFailure(t *testing.T) {
	queue := &fakeQueue{err: errors.New("redis unavailable")}
	repo := &fakeRepository{item: Record{ID: uuid.New()}, inserted: true}
	_, err := NewService(repo, queue, &fakeStorage{stored: StoredFile{Path: "/tmp/audio", Size: 5}}).Submit(context.Background(), strings.NewReader("ID3 audio"), "voice.mp3", "audio/mpeg")
	if err == nil {
		t.Fatal("expected queue failure")
	}
}
