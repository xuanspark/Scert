package logging

import (
	"io"
	"log/slog"
	"os"
)

type Logger struct {
	*slog.Logger
	closer io.Closer
}

func New(logDir string, level slog.Level) (*Logger, error) {
	fileWriter, err := newDailyFileWriter(logDir)
	if err != nil {
		return nil, err
	}
	writer := io.MultiWriter(os.Stdout, fileWriter)
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	return &Logger{Logger: slog.New(handler), closer: fileWriter}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}
