package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type Logger struct {
	*slog.Logger
	file *os.File
}

func New(logDir string, level slog.Level) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(logDir, "scert.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	writer := io.MultiWriter(os.Stdout, file)
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	return &Logger{Logger: slog.New(handler), file: file}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}
