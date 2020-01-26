package forward

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func NewLogger() (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.DisableCaller = true
	cfg.DisableStacktrace = true
	cfg.Sampling = nil
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.LevelKey = "severity"
	cfg.EncoderConfig.MessageKey = "message"
	cfg.EncoderConfig.TimeKey = "time"

	return cfg.Build()
}

type contextKeyLoggerMarker struct{}

// contextKeyLogger is key of context.Context for storing logger instance to ctx.
var contextKeyLogger = &contextKeyLoggerMarker{}

// WithLogger returns new context instance which holds logger.
func WithLogger(ctx context.Context, logger *zap.Logger) context.Context {
	return context.WithValue(ctx, contextKeyLogger, logger)
}

var nopLogger = zap.NewNop()

// GetLogger retrieve logger from context.
// If logger is not set, it returns nullLogger.
func ExtractLogger(ctx context.Context) *zap.Logger {
	if r, ok := ctx.Value(contextKeyLogger).(*zap.Logger); ok {
		return r
	}
	return nopLogger
}
