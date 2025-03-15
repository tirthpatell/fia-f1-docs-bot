package storage

import (
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
	AddProcessedDocument(doc ProcessedDocument) error

	// IsDocumentProcessed checks if a document has been processed
	IsDocumentProcessed(url string) bool

	// Close closes the storage (if needed)
	Close() error
}
