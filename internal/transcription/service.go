package transcription

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

var ErrUnsupportedAudio = errors.New("unsupported audio")

type Service struct {
	repository Repository
	queue      Queue
	storage    Storage
}

func NewService(repository Repository, queue Queue, storage Storage) *Service {
	return &Service{repository: repository, queue: queue, storage: storage}
}

func (s *Service) Submit(ctx context.Context, source io.Reader, filename, contentType string) (Record, error) {
	extension, normalizedType, err := validateAudio(filename, contentType)
	if err != nil {
		return Record{}, err
	}
	reader := bufio.NewReader(source)
	if err := validateAudioHeader(reader, extension); err != nil {
		return Record{}, err
	}
	stored, err := s.storage.Store(ctx, reader, extension)
	if err != nil {
		return Record{}, err
	}
	item, inserted, err := s.repository.CreateOrGet(ctx, Record{ID: uuid.New(), ContentSHA256: stored.SHA256, OriginalFilename: filepath.Base(filename), ContentType: normalizedType, ByteSize: stored.Size, StoragePath: stored.Path})
	if err != nil {
		if stored.Created {
			_ = s.storage.Delete(ctx, stored.Path)
		}
		return Record{}, fmt.Errorf("persist transcription: %w", err)
	}
	if !inserted {
		if stored.Created {
			_ = s.storage.Delete(ctx, stored.Path)
		}
		return item, nil
	}
	if err := s.queue.Enqueue(ctx, item.ID); err != nil {
		return Record{}, err
	}
	return item, nil
}
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Record, error) {
	return s.repository.Get(ctx, id)
}

func validateAudio(filename, contentType string) (string, string, error) {
	extension := strings.ToLower(filepath.Ext(filename))
	types := map[string]string{".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".opus": "audio/ogg", ".m4a": "audio/mp4", ".mp4": "audio/mp4", ".webm": "audio/webm"}
	expected, ok := types[extension]
	if !ok {
		return "", "", fmt.Errorf("%w type", ErrUnsupportedAudio)
	}
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType != "" && contentType != "application/octet-stream" && contentType != expected {
		return "", "", fmt.Errorf("%w content type", ErrUnsupportedAudio)
	}
	return extension, expected, nil
}

func validateAudioHeader(reader *bufio.Reader, extension string) error {
	header, _ := reader.Peek(12)
	valid := false
	switch extension {
	case ".wav":
		valid = len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE"
	case ".ogg", ".opus":
		valid = len(header) >= 4 && string(header[:4]) == "OggS"
	case ".mp3":
		valid = len(header) >= 3 && string(header[:3]) == "ID3" || len(header) >= 2 && header[0] == 0xff && header[1]&0xe0 == 0xe0
	case ".m4a", ".mp4":
		valid = len(header) >= 8 && string(header[4:8]) == "ftyp"
	case ".webm":
		valid = len(header) >= 4 && header[0] == 0x1a && header[1] == 0x45 && header[2] == 0xdf && header[3] == 0xa3
	}
	if !valid {
		return fmt.Errorf("%w file signature", ErrUnsupportedAudio)
	}
	return nil
}
