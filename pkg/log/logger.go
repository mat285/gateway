package log

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Logger struct {
	*slog.Logger
}

func New(cfg Config) *Logger {
	sl := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	return &Logger{sl}
}

func (l *Logger) Info(msg string, msgs ...any) {
	if l == nil {
		return
	}
	strs := make([]string, 0, len(msgs)+1)
	strs = append(strs, msg)
	for _, m := range msgs {
		strs = append(strs, fmt.Sprintf("%v", m))
	}
	l.Infof("%s", strings.Join(strs, " "))
}

func (l *Logger) Infof(format string, args ...any) {
	l.Logger.Info(fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...any) {
	l.Logger.Error(fmt.Sprintf(format, args...))
}

func (l *Logger) Warnf(format string, args ...any) {
	l.Logger.Warn(fmt.Sprintf(format, args...))
}

func (l *Logger) Debug(msg string, msgs ...any) {
	if l == nil {
		return
	}
	strs := make([]string, 0, len(msgs)+1)
	strs = append(strs, msg)
	for _, m := range msgs {
		strs = append(strs, fmt.Sprintf("%v", m))
	}
	l.Debugf("%s", strings.Join(strs, " "))
}

func (l *Logger) Debugf(format string, args ...any) {
	l.Logger.Debug(fmt.Sprintf(format, args...))
}
