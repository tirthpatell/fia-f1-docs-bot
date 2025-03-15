package storage

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

// PostgresStorage implements the StorageInterface using PostgreSQL
type PostgresStorage struct {
	db *sql.DB
}

// NewPostgres creates a new PostgreSQL storage
func NewPostgres(host, port, user, password, dbname, sslmode string) (StorageInterface, error) {
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("error connecting to database: %v", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("error pinging database: %v", err)
	}

	// Create the table if it doesn't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS processed_documents (
			id SERIAL PRIMARY KEY,
			title TEXT NOT NULL,
			url TEXT UNIQUE NOT NULL,
			timestamp TIMESTAMP NOT NULL
		)
	`)
	if err != nil {
		return nil, fmt.Errorf("error creating table: %v", err)
	}

	return &PostgresStorage{db: db}, nil
}

// Close closes the database connection
func (s *PostgresStorage) Close() error {
	return s.db.Close()
}

// AddProcessedDocument adds a document to the processed documents list
func (s *PostgresStorage) AddProcessedDocument(doc ProcessedDocument) error {
	// Check if the document already exists
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM processed_documents WHERE url = $1)", doc.URL).Scan(&exists)
	if err != nil {
		return fmt.Errorf("error checking if document exists: %v", err)
	}

	if exists {
		return nil // Already processed
	}

	// Insert the document
	_, err = s.db.Exec(
		"INSERT INTO processed_documents (title, url, timestamp) VALUES ($1, $2, $3)",
		doc.Title, doc.URL, doc.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("error inserting document: %v", err)
	}

	return nil
}

// IsDocumentProcessed checks if a document has been processed
func (s *PostgresStorage) IsDocumentProcessed(url string) bool {
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM processed_documents WHERE url = $1)", url).Scan(&exists)
	if err != nil {
		// Log the error but return false to be safe
		fmt.Printf("Error checking if document exists: %v\n", err)
		return false
	}
	return exists
}
