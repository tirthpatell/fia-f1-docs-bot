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

	// FilterProcessed returns the set of already-processed documents among
	// docs, keyed by DocKey. Documents absent from the map are unprocessed.
	FilterProcessed(ctx context.Context, docs []*scraper.Document) (map[string]bool, error)

	// CheckConnection checks if the database connection is still active
	CheckConnection() error

	// Close closes the storage (if needed)
	Close() error
}

// DocKey builds the lookup key used by FilterProcessed results.
func DocKey(title, url string) string {
	return title + "\x00" + url
}
