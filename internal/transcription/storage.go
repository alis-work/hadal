package transcription

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type StoredFile struct {
	Path    string
	SHA256  string
	Size    int64
	Created bool
}

type Storage interface {
	Store(context.Context, io.Reader, string) (StoredFile, error)
	Delete(context.Context, string) error
}

type FileStorage struct{ Directory string }

func (s FileStorage) Store(ctx context.Context, source io.Reader, extension string) (StoredFile, error) {
	if err := os.MkdirAll(s.Directory, 0o750); err != nil {
		return StoredFile{}, fmt.Errorf("create audio directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.Directory, "upload-*")
	if err != nil {
		return StoredFile{}, fmt.Errorf("create audio file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), &contextReader{ctx: ctx, reader: source})
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return StoredFile{}, fmt.Errorf("store audio: %w", err)
	}
	checksum := fmt.Sprintf("%x", hash.Sum(nil))
	path := filepath.Join(s.Directory, checksum+extension)
	// A matching hash is the same immutable upload; retain the original file.
	if _, err := os.Stat(path); err == nil {
		return StoredFile{Path: path, SHA256: checksum, Size: size}, nil
	}
	if err := os.Rename(temporaryPath, path); err != nil && !os.IsExist(err) {
		return StoredFile{}, fmt.Errorf("finalize audio file: %w", err)
	}
	return StoredFile{Path: path, SHA256: checksum, Size: size, Created: true}, nil
}

func (s FileStorage) Delete(_ context.Context, path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}
