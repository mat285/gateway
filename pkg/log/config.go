package log

import (
	"log/slog"
	"strings"
)

type Config struct {
	Level string `yaml:"level,omitempty" json:"level,omitempty"`
}

func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.Level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo // Default to Info if no valid level is specified
	}
}
