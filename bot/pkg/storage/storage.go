package storage

import (
	"bot/pkg/scraper"
	"context"
	"time"
)

// ProcessedDocument represents a document that has been processed by the bot
type ProcessedDocument struct {
	Title     string
	URL       string
	Timestamp time.Time
}

// StorageInterface defines the interface for storage implementations
type StorageInterface interface {
	// AddProcessedDocument adds a document to the processed documents list
	AddProcessedDocument(ctx context.Context, doc ProcessedDocument) error

	// IsDocumentProcessed checks if a document has been processed
	IsDocumentProcessed(ctx context.Context, doc *scraper.Document) bool

	// CheckConnection checks if the database connection is still active
	CheckConnection() error

	// Reconnect attempts to reconnect to the database if the connection is lost
	Reconnect() error

	// Close closes the storage (if needed)
	Close() error
}
