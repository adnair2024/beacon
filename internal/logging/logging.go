package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// MaxSize is when the log is rotated (checked when it's opened, which is
// often enough: hooks open it on every call).
const MaxSize = 1 << 20

// File is the log's name inside the state dir.
const File = "beacon.log"

// Discard returns a logger that drops everything, for when the log can't be opened.
func Discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Open returns a logger writing to <dir>/beacon.log, rotating it to
// beacon.log.1 once it passes MaxSize (one generation is kept).
func Open(dir string) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Discard(), io.NopCloser(nil), err
	}
	path := filepath.Join(dir, File)
	if fi, err := os.Stat(path); err == nil && fi.Size() > MaxSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Discard(), io.NopCloser(nil), err
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})), f, nil
}
