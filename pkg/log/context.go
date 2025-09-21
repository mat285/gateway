package log

import "context"

type contextKey struct{}

func WithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, logger)
}

func GetLogger(ctx context.Context) *Logger {
	raw := ctx.Value(contextKey{})
	logger, ok := raw.(*Logger)
	if !ok {
		return nil
	}

	return logger
}
