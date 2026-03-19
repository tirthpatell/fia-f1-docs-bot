package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
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
)

// ctxKey is a private type for context keys in this package, preventing
// collisions with keys from other packages that happen to use the same string.
type ctxKey string

const (
	ctxKeyRequestID ctxKey = "requestID" // per-goroutine / per-document
	ctxKeySessionID ctxKey = "sessionID" // per-scrape-cycle
)

// ParseLevel converts a string to a Level
func ParseLevel(levelStr string) (Level, error) {
	switch strings.ToLower(levelStr) {
	case "debug":
		return LevelDebug, nil
	case "info":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return LevelInfo, fmt.Errorf("invalid log level: %s (valid: debug, info, warn, error)", levelStr)
	}
}

// Logger wraps slog.Logger for structured logging
type Logger struct {
	*slog.Logger
	serviceName string
	sampler     *logSampler
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
	// Environment (e.g., production, staging, development)
	Environment string
	// Version of the application
	Version string
	// SanitizeFields enables sensitive data sanitization
	SanitizeFields bool
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

// logSampler implements log sampling to reduce repeated error messages
type logSampler struct {
	mu          sync.Mutex
	counts      map[string]int
	lastLogged  map[string]time.Time
	sampleAfter int           // After this many logs, start sampling
	sampleRate  int           // 1 in N logs will be recorded after sampleAfter
	resetPeriod time.Duration // Reset counters after this period
}

// newLogSampler creates a new log sampler
func newLogSampler() *logSampler {
	return &logSampler{
		counts:      make(map[string]int),
		lastLogged:  make(map[string]time.Time),
		sampleAfter: 5,               // Log first 5 occurrences
		sampleRate:  10,              // Then log 1 in 10
		resetPeriod: 5 * time.Minute, // Reset after 5 minutes
	}
}

// shouldLog determines if a log message should be emitted based on sampling.
// Returns (emit, count) where count is the occurrence count before this call.
func (s *logSampler) shouldLog(key string) (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Reset counter if enough time has passed
	if lastTime, exists := s.lastLogged[key]; exists {
		if now.Sub(lastTime) > s.resetPeriod {
			s.counts[key] = 0
		}
	}

	count := s.counts[key]
	s.counts[key]++
	s.lastLogged[key] = now

	// Always log the first N occurrences
	if count < s.sampleAfter {
		return true, count
	}

	// After that, sample at the specified rate
	return count%s.sampleRate == 0, count
}

// sanitizingHandler wraps a handler to sanitize sensitive data
type sanitizingHandler struct {
	slog.Handler
	sensitivePatterns []string
}

// newSanitizingHandler creates a handler that sanitizes sensitive fields
func newSanitizingHandler(handler slog.Handler) *sanitizingHandler {
	return &sanitizingHandler{
		Handler: handler,
		sensitivePatterns: []string{
			"token", "password", "secret", "api_key", "apikey",
			"authorization", "auth", "credentials", "key",
		},
	}
}

// Handle sanitizes sensitive data before logging
func (h *sanitizingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Create a new record with sanitized attributes
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)

	r.Attrs(func(a slog.Attr) bool {
		key := strings.ToLower(a.Key)

		// Check if this is a sensitive field
		for _, pattern := range h.sensitivePatterns {
			if strings.Contains(key, pattern) {
				// Sanitize the value
				newRecord.AddAttrs(slog.String(a.Key, "***REDACTED***"))
				return true
			}
		}

		// Keep the original attribute
		newRecord.AddAttrs(a)
		return true
	})

	return h.Handler.Handle(ctx, newRecord)
}

// WithAttrs returns a new Handler with sanitization
func (h *sanitizingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sanitizingHandler{
		Handler:           h.Handler.WithAttrs(attrs),
		sensitivePatterns: h.sensitivePatterns,
	}
}

// WithGroup returns a new Handler with sanitization
func (h *sanitizingHandler) WithGroup(name string) slog.Handler {
	return &sanitizingHandler{
		Handler:           h.Handler.WithGroup(name),
		sensitivePatterns: h.sensitivePatterns,
	}
}

// customHandler is a custom handler that adds service name and message to each log record
type customHandler struct {
	slog.Handler
	serviceName string
	environment string
	version     string
}

// Handle adds service name, environment, and version to each log record
func (h *customHandler) Handle(ctx context.Context, r slog.Record) error {
	// Add standard fields to each log
	attrs := []slog.Attr{
		slog.String("service", h.serviceName),
	}

	if h.environment != "" {
		attrs = append(attrs, slog.String("environment", h.environment))
	}

	if h.version != "" {
		attrs = append(attrs, slog.String("version", h.version))
	}

	r.AddAttrs(attrs...)
	return h.Handler.Handle(ctx, r)
}

// WithAttrs returns a new Handler whose attributes consist of h's attributes followed by attrs.
func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &customHandler{
		Handler:     h.Handler.WithAttrs(attrs),
		serviceName: h.serviceName,
		environment: h.environment,
		version:     h.version,
	}
}

// WithGroup returns a new Handler with the given group appended to the Handler's
// existing groups.
func (h *customHandler) WithGroup(name string) slog.Handler {
	return &customHandler{
		Handler:     h.Handler.WithGroup(name),
		serviceName: h.serviceName,
		environment: h.environment,
		version:     h.version,
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

	environment := cfg.Environment
	if environment == "" {
		environment = "production"
	}

	version := cfg.Version
	if version == "" {
		version = "unknown"
	}

	// Create handler with JSON format for structured logging.
	// ReplaceAttr renames the default "msg" key to "message" so the log
	// message appears as a queryable structured field in log aggregators.
	baseHandler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level:     level,
		AddSource: cfg.AddSource,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.MessageKey {
				a.Key = "message"
			}
			return a
		},
	})

	// Wrap with sanitizing handler if enabled
	var handler slog.Handler = baseHandler
	if cfg.SanitizeFields {
		handler = newSanitizingHandler(handler)
	}

	// Wrap with custom handler to add service metadata
	handler = &customHandler{
		Handler:     handler,
		serviceName: serviceName,
		environment: environment,
		version:     version,
	}

	// Create base logger
	slogger := slog.New(handler)

	logger := &Logger{
		Logger:      slogger,
		serviceName: serviceName,
		sampler:     newLogSampler(),
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

	// Recreate all existing package loggers with the new default logger
	loggersMu.Lock()
	for pkg := range loggers {
		loggers[pkg] = defaultLogger.WithContext("package", pkg)
	}
	loggersMu.Unlock()
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
func (l *Logger) Debug(msg string, args ...any) {
	l.logWithCaller(slog.LevelDebug, msg, args...)
}

// Info logs at info level
func (l *Logger) Info(msg string, args ...any) {
	l.logWithCaller(slog.LevelInfo, msg, args...)
}

// Warn logs at warn level
func (l *Logger) Warn(msg string, args ...any) {
	l.logWithCaller(slog.LevelWarn, msg, args...)
}

// Error logs at error level
func (l *Logger) Error(msg string, args ...any) {
	l.logWithCaller(slog.LevelError, msg, args...)
}

// ErrorWithType logs an error with its type information for better debugging
func (l *Logger) ErrorWithType(msg string, err error, args ...any) {
	allArgs := make([]any, 0, len(args)+4)
	allArgs = append(allArgs, "error", err)
	if err != nil {
		allArgs = append(allArgs, "error_type", fmt.Sprintf("%T", err))
	}
	allArgs = append(allArgs, args...)
	l.logWithCaller(slog.LevelError, msg, allArgs...)
}

// SampledError logs an error with sampling to avoid flooding logs with repeated errors
func (l *Logger) SampledError(key string, msg string, args ...any) {
	if emit, count := l.sampler.shouldLog(key); emit {
		if count >= l.sampler.sampleAfter {
			// Add sampling metadata
			allArgs := make([]any, 0, len(args)+4)
			allArgs = append(allArgs, "sampled", true, "occurrence_count", count+1)
			allArgs = append(allArgs, args...)
			l.logWithCaller(slog.LevelError, msg, allArgs...)
		} else {
			l.logWithCaller(slog.LevelError, msg, args...)
		}
	}
}

// logWithCaller logs with proper caller information, skipping the wrapper frame
func (l *Logger) logWithCaller(level slog.Level, msg string, args ...any) {
	ctx := context.Background()
	if !l.Enabled(ctx, level) {
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

// WithRequestContext creates a logger enriched with tracing IDs from ctx.
// It attaches sessionID (cycle-level) and/or requestID (goroutine-level)
// whenever they are present, so logs can be filtered by either.
func (l *Logger) WithRequestContext(ctx context.Context) *Logger {
	sessionID, _ := ctx.Value(ctxKeySessionID).(string)
	reqID, _ := ctx.Value(ctxKeyRequestID).(string)

	attrs := make([]any, 0, 4)
	if sessionID != "" {
		attrs = append(attrs, "sessionID", sessionID)
	}
	if reqID != "" {
		attrs = append(attrs, "requestID", reqID)
	}
	if len(attrs) == 0 {
		attrs = append(attrs, "requestID", "unknown")
	}

	logger := l.With(attrs...)
	return &Logger{
		Logger:      logger,
		serviceName: l.serviceName,
		sampler:     l.sampler,
	}
}

// WithContext adds arbitrary context values to the logger
func (l *Logger) WithContext(key string, value any) *Logger {
	logger := l.With(key, value)
	return &Logger{
		Logger:      logger,
		serviceName: l.serviceName,
		sampler:     l.sampler,
	}
}

// NewRequestContext creates a new context with request ID
func NewRequestContext() (context.Context, string) {
	// Generate a unique ID for this request/operation
	reqID := generateRequestID()
	ctx := context.WithValue(context.Background(), ctxKeyRequestID, reqID)
	return ctx, reqID
}

// NewSessionContextFrom creates a child of parent with a sessionID for
// tracking a full scrape cycle. All goroutines spawned within the cycle
// inherit this ID, enabling cycle-wide log filtering.
// Cancellation of parent propagates to the returned context.
func NewSessionContextFrom(parent context.Context) (context.Context, string) {
	sessionID := generateRequestID()
	ctx := context.WithValue(parent, ctxKeySessionID, sessionID)
	return ctx, sessionID
}

// NewRequestContextFrom creates a child of parent with a requestID for
// tracking an individual goroutine or operation within a cycle.
// Any sessionID on parent is preserved and inherited.
// Cancellation of parent propagates to the returned context.
func NewRequestContextFrom(parent context.Context) (context.Context, string) {
	reqID := generateRequestID()
	ctx := context.WithValue(parent, ctxKeyRequestID, reqID)
	return ctx, reqID
}

// Helper to generate a unique request ID
func generateRequestID() string {
	id := uuid.New()
	return id.String()
}
