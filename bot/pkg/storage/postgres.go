package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"bot/pkg/logger"
	"bot/pkg/scraper"

	_ "github.com/lib/pq"
)

// Package logger
var log = logger.Package("storage")

// PostgresStorage implements the StorageInterface using PostgreSQL.
// sql.DB is a self-healing connection pool: dropped connections are
// re-established transparently, so there is no explicit reconnect logic.
type PostgresStorage struct {
	db *sql.DB
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

	// Configure connection pool for concurrent workers
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

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
				if rbErr := tx.Rollback(); rbErr != nil {
					return nil, fmt.Errorf("error dropping constraint: %v (rollback failed: %v)", err, rbErr)
				}
				ctxLog.Error("Error dropping constraint", "error", err)
				return nil, fmt.Errorf("error dropping constraint: %v", err)
			}

			_, err = tx.Exec(`
				ALTER TABLE processed_documents
				ADD CONSTRAINT processed_documents_title_url_key UNIQUE(title, url);
			`)

			if err != nil {
				if rbErr := tx.Rollback(); rbErr != nil {
					return nil, fmt.Errorf("error adding new constraint: %v (rollback failed: %v)", err, rbErr)
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

	// Ensure the composite unique index exists regardless of how the schema
	// was provisioned (fresh create, migrated, or manual): AddProcessedDocument's
	// ON CONFLICT (title, url) fails at runtime without a matching unique
	// index. Both the CREATE TABLE UNIQUE clause and the migration's ADD
	// CONSTRAINT back their constraint with an index of this name, so this is
	// a no-op on healthy schemas.
	_, err = db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS processed_documents_title_url_key
		ON processed_documents (title, url)
	`)
	if err != nil {
		ctxLog.Error("Error ensuring unique index on (title, url)", "error", err)
		return nil, fmt.Errorf("error ensuring unique index on (title, url): %v", err)
	}

	ctxLog.Info("PostgreSQL storage initialized successfully")
	return &PostgresStorage{
		db: db,
	}, nil
}

// CheckConnection checks if the database connection is still active
func (s *PostgresStorage) CheckConnection(ctx context.Context) error {
	ctxLog := log.WithRequestContext(ctx).WithContext("method", "CheckConnection")

	err := s.db.PingContext(ctx)
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

// AddProcessedDocument adds a document to the processed documents list.
// The insert is atomic: the UNIQUE(title, url) constraint plus ON CONFLICT
// DO NOTHING makes re-adding an already processed document a no-op.
func (s *PostgresStorage) AddProcessedDocument(ctx context.Context, doc ProcessedDocument) error {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "AddProcessedDocument").
		WithContext("url", doc.URL)

	ctxLog.Info(fmt.Sprintf("Adding document to processed list: %s", doc.Title))
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO processed_documents (title, url, timestamp) VALUES ($1, $2, $3) ON CONFLICT (title, url) DO NOTHING",
		doc.Title, doc.URL, doc.Timestamp,
	)
	duration := time.Since(start)

	if err != nil {
		ctxLog.ErrorWithType("Error inserting document", err,
			"insert_duration_ms", duration.Milliseconds())
		return fmt.Errorf("error inserting document: %v", err)
	}

	if rows, raErr := res.RowsAffected(); raErr == nil && rows == 0 {
		ctxLog.Info("Document already processed, skipping",
			"insert_duration_ms", duration.Milliseconds())
		return nil
	}

	ctxLog.Info("Document added to processed list successfully",
		"insert_duration_ms", duration.Milliseconds())

	return nil
}

// FilterProcessed returns the set of already-processed documents among docs
// in a single query, keyed by DocKey(title, url).
func (s *PostgresStorage) FilterProcessed(ctx context.Context, docs []*scraper.Document) (map[string]bool, error) {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "FilterProcessed")

	processed := make(map[string]bool, len(docs))
	if len(docs) == 0 {
		return processed, nil
	}

	placeholders := make([]string, 0, len(docs))
	args := make([]any, 0, len(docs)*2)
	for i, doc := range docs {
		placeholders = append(placeholders, fmt.Sprintf("($%d, $%d)", i*2+1, i*2+2))
		args = append(args, doc.Title, doc.URL)
	}

	query := "SELECT title, url FROM processed_documents WHERE (title, url) IN (" +
		strings.Join(placeholders, ", ") + ")"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		ctxLog.ErrorWithType("Error querying processed documents", err,
			"query_duration_ms", time.Since(start).Milliseconds())
		return nil, fmt.Errorf("error querying processed documents: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctxLog.Warn("Error closing rows", "error", err)
		}
	}()

	for rows.Next() {
		var title, url string
		if err := rows.Scan(&title, &url); err != nil {
			return nil, fmt.Errorf("error scanning processed document: %v", err)
		}
		processed[DocKey(title, url)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating processed documents: %v", err)
	}

	ctxLog.Debug("Processed documents check completed",
		"checked", len(docs),
		"already_processed", len(processed),
		"query_duration_ms", time.Since(start).Milliseconds())

	return processed, nil
}
