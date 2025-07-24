package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Level string

const (
	// Log levels
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"

	// Context key for request ID
	ctxKeyRequestID = "requestID"
)

// Logger wraps slog.Logger for structured logging
type Logger struct {
	*slog.Logger
	serviceName string
}

// Config holds configuration for the logger
type Config struct {
	// OutputWriter where logs will be written (defaults to os.Stdout if nil)
	OutputWriter io.Writer
	// Level sets the minimum log level (debug, info, warn, error)
	Level Level
	// AddSource adds source code information to log
	AddSource bool
	// ServiceName to include in logs
	ServiceName string
}

// Global logger registry
var (
	defaultLogger *Logger
	loggers       map[string]*Logger
	loggersMu     sync.RWMutex
)

// Initialize logger registry
func init() {
	loggers = make(map[string]*Logger)
}

// customHandler is a custom handler that adds service name and message to each log record
type customHandler struct {
	slog.Handler
	serviceName string
}

// Handle adds service name to each log record
func (h *customHandler) Handle(ctx context.Context, r slog.Record) error {
	// Add service name to each log
	r.AddAttrs(
		slog.String("service", h.serviceName),
		slog.String("message", r.Message),
	)
	return h.Handler.Handle(ctx, r)
}

// WithAttrs returns a new Handler whose attributes consist of h's attributes followed by attrs.
func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &customHandler{
		Handler:     h.Handler.WithAttrs(attrs),
		serviceName: h.serviceName,
	}
}

// WithGroup returns a new Handler with the given group appended to the Handler's
// existing groups.
func (h *customHandler) WithGroup(name string) slog.Handler {
	return &customHandler{
		Handler:     h.Handler.WithGroup(name),
		serviceName: h.serviceName,
	}
}

// New creates a new structured logger
func New(cfg Config) *Logger {
	var level slog.Level
	switch cfg.Level {
	case LevelDebug:
		level = slog.LevelDebug
	case LevelWarn:
		level = slog.LevelWarn
	case LevelError:
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	output := cfg.OutputWriter
	if output == nil {
		output = os.Stdout
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "f1-docs-bot"
	}

	// Create handler with JSON format for Datadog structured logging
	baseHandler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level:     level,
		AddSource: cfg.AddSource,
	})

	// Wrap with custom handler
	handler := &customHandler{
		Handler:     baseHandler,
		serviceName: serviceName,
	}

	// Create base logger
	slogger := slog.New(handler)

	logger := &Logger{
		Logger:      slogger,
		serviceName: serviceName,
	}

	// Set as default logger if this is the first one
	if defaultLogger == nil {
		defaultLogger = logger
	}

	return logger
}

// SetDefaultLogger sets the default logger used throughout the application
func SetDefaultLogger(logger *Logger) {
	defaultLogger = logger
}

// Package returns a logger for a specific package
func Package(pkg string) *Logger {
	if defaultLogger == nil {
		// Create a basic default logger if none exists
		defaultLogger = New(Config{})
	}

	loggersMu.RLock()
	logger, exists := loggers[pkg]
	loggersMu.RUnlock()

	if exists {
		return logger
	}

	// Create a new logger with package context
	newLogger := defaultLogger.WithContext("package", pkg)

	loggersMu.Lock()
	loggers[pkg] = newLogger
	loggersMu.Unlock()

	return newLogger
}

// Debug logs at debug level
func (l *Logger) Debug(msg string, args ...interface{}) {
	l.logWithCaller(slog.LevelDebug, msg, args...)
}

// Info logs at info level
func (l *Logger) Info(msg string, args ...interface{}) {
	l.logWithCaller(slog.LevelInfo, msg, args...)
}

// Warn logs at warn level
func (l *Logger) Warn(msg string, args ...interface{}) {
	l.logWithCaller(slog.LevelWarn, msg, args...)
}

// Error logs at error level
func (l *Logger) Error(msg string, args ...interface{}) {
	l.logWithCaller(slog.LevelError, msg, args...)
}

// logWithCaller logs with proper caller information, skipping the wrapper frame
func (l *Logger) logWithCaller(level slog.Level, msg string, args ...interface{}) {
	ctx := context.Background()
	if !l.Logger.Enabled(ctx, level) {
		return
	}

	var pcs [1]uintptr
	runtime.Callers(3, pcs[:]) // skip [Callers, logWithCaller, Info/Debug/etc]
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])

	// Convert args to slog.Attr
	if len(args) > 0 {
		attrs := make([]slog.Attr, 0, len(args)/2)
		for i := 0; i < len(args); i += 2 {
			if i+1 < len(args) {
				key := args[i]
				value := args[i+1]
				if keyStr, ok := key.(string); ok {
					attrs = append(attrs, slog.Any(keyStr, value))
				}
			}
		}
		r.AddAttrs(attrs...)
	}

	_ = l.Logger.Handler().Handle(ctx, r)
}

// WithRequestContext creates a logger with request context information
func (l *Logger) WithRequestContext(ctx context.Context) *Logger {
	// Extract request ID from context if available
	reqID, ok := ctx.Value(ctxKeyRequestID).(string)
	if !ok || reqID == "" {
		reqID = "unknown"
	}

	logger := l.Logger.With("requestID", reqID)
	return &Logger{
		Logger:      logger,
		serviceName: l.serviceName,
	}
}

// WithContext adds arbitrary context values to the logger
func (l *Logger) WithContext(key string, value interface{}) *Logger {
	logger := l.Logger.With(key, value)
	return &Logger{
		Logger:      logger,
		serviceName: l.serviceName,
	}
}

// NewRequestContext creates a new context with request ID
func NewRequestContext() (context.Context, string) {
	// Generate a unique ID for this request/operation
	reqID := generateRequestID()
	ctx := context.WithValue(context.Background(), ctxKeyRequestID, reqID)
	return ctx, reqID
}

// Helper to generate a unique request ID
func generateRequestID() string {
	id := uuid.New()
	return id.String()
}
