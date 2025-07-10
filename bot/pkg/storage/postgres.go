package storage

import (
	"context"
	"database/sql"
	"fmt"

	"bot/pkg/logger"
	"bot/pkg/scraper"

	_ "github.com/lib/pq"
)

// Package logger
var log = logger.Package("storage")

// PostgresStorage implements the StorageInterface using PostgreSQL
type PostgresStorage struct {
	db      *sql.DB
	connStr string
}

// NewPostgres creates a new PostgreSQL storage
func NewPostgres(host, port, user, password, dbname, sslmode string) (StorageInterface, error) {
	ctxLog := log.WithContext("method", "NewPostgres")

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)

	ctxLog.Info("Connecting to PostgreSQL database", "host", host, "port", port, "dbname", dbname)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		ctxLog.Error("Error connecting to database", "error", err)
		return nil, fmt.Errorf("error connecting to database: %v", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		ctxLog.Error("Error pinging database", "error", err)
		return nil, fmt.Errorf("error pinging database: %v", err)
	}

	// Migration strategy:
	// 1. Check if the table exists
	var tableExists bool
	err = db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM information_schema.tables 
			WHERE table_name = 'processed_documents'
		)
	`).Scan(&tableExists)

	if err != nil {
		ctxLog.Error("Error checking if table exists", "error", err)
		return nil, fmt.Errorf("error checking if table exists: %v", err)
	}

	if tableExists {
		ctxLog.Info("Table exists, checking schema")

		// 2. Check if we need to migrate (check for constraint name)
		var constraintExists bool
		err = db.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM information_schema.table_constraints
				WHERE table_name = 'processed_documents'
				AND constraint_name = 'processed_documents_url_key'
			)
		`).Scan(&constraintExists)

		if err != nil {
			ctxLog.Error("Error checking constraints", "error", err)
			return nil, fmt.Errorf("error checking constraints: %v", err)
		}

		if constraintExists {
			// 3. Perform migration
			ctxLog.Info("Migrating table schema - dropping unique constraint on URL")

			// Start a transaction for the migration
			tx, err := db.Begin()
			if err != nil {
				ctxLog.Error("Error starting transaction", "error", err)
				return nil, fmt.Errorf("error starting transaction: %v", err)
			}

			_, err = tx.Exec(`
				ALTER TABLE processed_documents
				DROP CONSTRAINT processed_documents_url_key;
			`)

			if err != nil {
				err := tx.Rollback()
				if err != nil {
					return nil, fmt.Errorf("error rolling back transaction: %v", err)
				}
				ctxLog.Error("Error dropping constraint", "error", err)
				return nil, fmt.Errorf("error dropping constraint: %v", err)
			}

			_, err = tx.Exec(`
				ALTER TABLE processed_documents
				ADD CONSTRAINT processed_documents_title_url_key UNIQUE(title, url);
			`)

			if err != nil {
				err := tx.Rollback()
				if err != nil {
					return nil, fmt.Errorf("error rolling back transaction: %v", err)
				}
				ctxLog.Error("Error adding new constraint", "error", err)
				return nil, fmt.Errorf("error adding new constraint: %v", err)
			}

			if err := tx.Commit(); err != nil {
				ctxLog.Error("Error committing transaction", "error", err)
				return nil, fmt.Errorf("error committing transaction: %v", err)
			}

			ctxLog.Info("Schema migration completed successfully")
		} else {
			ctxLog.Info("Schema already up to date, no migration needed")
		}
	} else {
		// Create the table if it doesn't exist
		ctxLog.Info("Creating table (doesn't exist)")
		_, err = db.Exec(`
			CREATE TABLE IF NOT EXISTS processed_documents (
				id SERIAL PRIMARY KEY,
				title TEXT NOT NULL,
				url TEXT NOT NULL,
				timestamp TIMESTAMP NOT NULL,
				UNIQUE(title, url)
			)
		`)
		if err != nil {
			ctxLog.Error("Error creating table", "error", err)
			return nil, fmt.Errorf("error creating table: %v", err)
		}
	}

	ctxLog.Info("PostgreSQL storage initialized successfully")
	return &PostgresStorage{
		db:      db,
		connStr: connStr,
	}, nil
}

// Reconnect attempts to reconnect to the database
func (s *PostgresStorage) Reconnect() error {
	ctxLog := log.WithContext("method", "Reconnect")

	// Close the existing connection if it exists
	if s.db != nil {
		ctxLog.Info("Closing existing database connection")
		_ = s.db.Close() // Ignore close errors
	}

	// Create a new connection
	ctxLog.Info("Creating new database connection")
	db, err := sql.Open("postgres", s.connStr)
	if err != nil {
		ctxLog.Error("Error reconnecting to database", "error", err)
		return fmt.Errorf("error reconnecting to database: %v", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		ctxLog.Error("Error pinging database after reconnect", "error", err)
		return fmt.Errorf("error pinging database after reconnect: %v", err)
	}

	// Update the db reference
	s.db = db
	ctxLog.Info("Successfully reconnected to database")
	return nil
}

// CheckConnection checks if the database connection is still active
func (s *PostgresStorage) CheckConnection() error {
	ctxLog := log.WithContext("method", "CheckConnection")

	err := s.db.Ping()
	if err != nil {
		ctxLog.Error("Database connection check failed", "error", err)
	} else {
		ctxLog.Debug("Database connection check successful")
	}
	return err
}

// Close closes the database connection
func (s *PostgresStorage) Close() error {
	ctxLog := log.WithContext("method", "Close")

	ctxLog.Info("Closing database connection")
	return s.db.Close()
}

// AddProcessedDocument adds a document to the processed documents list
func (s *PostgresStorage) AddProcessedDocument(ctx context.Context, doc ProcessedDocument) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "AddProcessedDocument").
		WithContext("url", doc.URL)

	// Check if the document already exists
	var exists bool
	ctxLog.Debug("Checking if document already exists")
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM processed_documents WHERE url = $1 AND title = $2)",
		doc.URL, doc.Title).Scan(&exists)
	if err != nil {
		ctxLog.Error("Error checking if document exists", "error", err)
		return fmt.Errorf("error checking if document exists: %v", err)
	}

	if exists {
		ctxLog.Info("Document already processed, skipping")
		return nil // Already processed
	}

	// Insert the document
	ctxLog.Info(fmt.Sprintf("Adding document to processed list: %s", doc.Title))
	_, err = s.db.Exec(
		"INSERT INTO processed_documents (title, url, timestamp) VALUES ($1, $2, $3)",
		doc.Title, doc.URL, doc.Timestamp,
	)
	if err != nil {
		ctxLog.Error("Error inserting document", "error", err)
		return fmt.Errorf("error inserting document: %v", err)
	}

	return nil
}

// IsDocumentProcessed checks if a document has been processed
func (s *PostgresStorage) IsDocumentProcessed(ctx context.Context, doc *scraper.Document) bool {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "IsDocumentProcessed")

	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM processed_documents WHERE url = $1 AND title = $2)",
		doc.URL, doc.Title).Scan(&exists)
	if err != nil {
		// If there's a database error, we'll assume it's not processed
		// The main loop will handle reconnection
		ctxLog.Error("Error checking if document exists", "error", err)
		return false
	}

	if exists {
		ctxLog.Debug("Document is already processed")
	} else {
		ctxLog.Debug("Document is not processed yet")
	}

	return exists
}
