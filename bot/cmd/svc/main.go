package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"bot/pkg/config"
	"bot/pkg/logger"
	"bot/pkg/poster"
	"bot/pkg/scraper"
	"bot/pkg/storage"
	"bot/pkg/summary"
	"bot/pkg/utils"
)

const (
	maxConcurrentProcessing = 5               // Maximum number of documents to process concurrently
	documentsToFetch        = 8               // Number of recent documents to check
	tempDir                 = "temp"          // Temporary directory for downloaded PDFs
	shortRetryInterval      = 1 * time.Minute // Short retry interval for DB connection
	longRetryInterval       = 5 * time.Minute // Long retry interval for DB connection
	serviceName             = "f1-docs-bot"   // Service name for logging
)

// Global logger
var log *logger.Logger

// waitForDBConnection attempts to establish a database connection with retries
func waitForDBConnection(ctx context.Context, store storage.StorageInterface) {
	// Get context-aware logger
	dbLog := log.WithRequestContext(ctx).WithContext("component", "database")

	// First try with short interval
	if err := store.CheckConnection(); err != nil {
		dbLog.Error("Database connection lost", "error", err)
		dbLog.Info("Waiting before retrying", "interval", shortRetryInterval)
		time.Sleep(shortRetryInterval)

		// Try to reconnect
		if err := store.Reconnect(); err != nil {
			dbLog.Error("Failed to reconnect to database", "error", err)

			// Keep trying with long interval until successful
			for {
				dbLog.Info("Waiting before retrying", "interval", longRetryInterval)
				time.Sleep(longRetryInterval)

				if err := store.Reconnect(); err != nil {
					dbLog.Error("Failed to reconnect to database", "error", err)
				} else {
					dbLog.Info("Successfully reconnected to database")
					break
				}
			}
		} else {
			dbLog.Info("Successfully reconnected to database")
		}
	}
}

func main() {
	// Initialize structured logger
	log = logger.New(logger.Config{
		Level:       logger.LevelInfo,
		AddSource:   true,
		ServiceName: serviceName,
	})

	// Set as the default logger for the entire application
	logger.SetDefaultLogger(log)

	// Create application context
	appCtx, _ := logger.NewRequestContext()
	appLog := log.WithRequestContext(appCtx).WithContext("component", "main")

	appLog.Info("Starting F1 Documents Bot service")

	cfg, err := config.Load()
	if err != nil {
		appLog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		appLog.Error("Failed to create temp directory", "error", err)
		os.Exit(1)
	}

	// Automatically run this every 45 days to refresh the long-lived access token
	go func() {
		tokenCtx, _ := logger.NewRequestContext()
		tokenLog := log.WithRequestContext(tokenCtx).WithContext("component", "token_refresher")

		for {
			// Refresh the long-lived access token
			tokenLog.Info("Refreshing access token")
			newToken, err := utils.RefreshToken(tokenCtx, cfg.ThreadsAccessToken)
			if err != nil {
				tokenLog.Error("Error refreshing token", "error", err)
			} else {
				tokenLog.Info("Successfully refreshed token")
				cfg.ThreadsAccessToken = newToken
			}

			// Sleep for 45 days
			tokenLog.Info("Sleeping until next token refresh", "days", 45)
			time.Sleep(45 * 24 * time.Hour)
		}
	}()

	// Initialize storage based on configuration
	appLog.Info("Initializing PostgreSQL storage")
	store, err := storage.NewPostgres(
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBName,
		cfg.DBSSLMode,
	)
	if err != nil {
		appLog.Error("Failed to initialize PostgreSQL storage", "error", err)
		os.Exit(1)
	}

	// Close storage when done
	defer func() {
		if err := store.Close(); err != nil {
			appLog.Error("Error closing storage", "error", err)
		}
	}()

	// Initialize the packages
	appLog.Info("Initializing summarizer")
	summarizer, err := summary.New(summary.Config{
		APIKey: cfg.GeminiAPIKey,
	})
	if err != nil {
		appLog.Error("Failed to initialize summarizer", "error", err)
	}
	defer summarizer.Close()

	appLog.Info("Initializing scraper and poster")
	sc := scraper.New(cfg.FIAUrl)
	poster := poster.New(cfg.ThreadsAccessToken, cfg.ThreadsUserID, cfg.PicsurAPI, cfg.PicsurURL, cfg.ShortenerAPIKey, cfg.ShortenerURL)

	appLog.Info("Service initialization complete, entering main loop")

	for {
		// Create a new context for each check cycle
		cycleCtx, _ := logger.NewRequestContext()
		cycleLog := log.WithRequestContext(cycleCtx).WithContext("component", "main_cycle")

		cycleLog.Info("Checking for new documents")

		// Check database connection before processing
		// This will wait until connection is established
		waitForDBConnection(cycleCtx, store)

		docs, err := sc.FetchLatestDocuments(cycleCtx, documentsToFetch)
		if err != nil {
			cycleLog.Error("Error fetching documents", "error", err)
			cycleLog.Info("Sleeping before retrying", "seconds", cfg.ScrapeInterval)
			time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
			continue
		}

		cycleLog.Info("Documents fetched", "Documents", docs)

		if len(docs) == 0 {
			cycleLog.Info("No documents found for the current Grand Prix")
			cycleLog.Info("Sleeping before retrying", "seconds", cfg.ScrapeInterval)
			time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
			continue
		}

		// Create a worker pool with limited concurrency
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, maxConcurrentProcessing)

		// Create a map to track processed documents and then pass it to log
		processedDocs := make(map[string]bool)

		for _, doc := range docs {
			// Check database connection before checking if document is processed
			waitForDBConnection(cycleCtx, store)

			// Skip already processed documents (moved this check earlier to handle all docs including recalled ones)
			if store.IsDocumentProcessed(cycleCtx, doc) {
				processedDocs[doc.Title] = true
				continue
			}

			// Check if this is a recalled document by its title
			if sc.IsRecalledDocument(*doc) {
				cycleLog.Info("Detected recalled document from title", "document", doc.Title)

				// Process recalled document specially
				cycleLog.Info("Posting recalled document notice")
				err := postRecalledDocumentNotice(cycleCtx, poster, doc)
				if err != nil {
					cycleLog.Error("Error posting recalled document notice", "error", err)
				}

				// Mark as processed to avoid repeated attempts
				cycleLog.Info("Marking recalled document as processed")
				err = store.AddProcessedDocument(cycleCtx, storage.ProcessedDocument{
					Title:     doc.Title,
					URL:       doc.URL,
					Timestamp: doc.Published,
				})
				if err != nil {
					cycleLog.Error("Error updating storage", "error", err)
				}

				// Add to the processed docs map to avoid multiple notices
				processedDocs[doc.Title] = true

				continue
			}

			// Limit concurrency using semaphore
			semaphore <- struct{}{}
			wg.Add(1)

			go func(document *scraper.Document) {
				defer wg.Done()
				defer func() { <-semaphore }()

				// Create a document processing context derived from the cycle context
				docCtx := cycleCtx
				docLog := log.WithRequestContext(docCtx).
					WithContext("component", "document_processor")

				docLog.Info(fmt.Sprintf("Processing new document: %s", document.Title))
				processDocument(docCtx, document, sc, summarizer, poster, store, cfg)
			}(doc)
		}

		// Log skipped documents after the loop (if any)
		if len(processedDocs) > 0 {
			cycleLog.Info("Skipping already processed document(s)", "Documents", processedDocs)
		}

		// Wait for all goroutines to finish
		wg.Wait()

		cycleLog.Info("Sleeping before next check", "seconds", cfg.ScrapeInterval)
		time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
	}
}

// processDocument handles all steps for a single document
func processDocument(ctx context.Context, doc *scraper.Document, scraper *scraper.Scraper, summarizer *summary.Summarizer, poster *poster.Poster, store storage.StorageInterface, cfg *config.Config) {
	// Get logger from context for this document
	docLog := log.WithRequestContext(ctx).
		WithContext("component", "document_processor")

	// Create a unique directory for this document
	docDir := filepath.Join(tempDir, fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(docDir, 0755); err != nil {
		docLog.Error("Error creating directory for document", "error", err)
		return
	}
	defer os.RemoveAll(docDir) // Clean up when done

	// Download the document
	docLog.Debug("Downloading document")
	pdfPath, err := scraper.DownloadDocument(ctx, *doc, docDir)
	if err != nil {
		// Check if this is a recalled document
		if strings.Contains(err.Error(), "document has been recalled") ||
			strings.Contains(err.Error(), "invalid PDF file (possibly recalled)") {
			docLog.Info("Detected recalled document")

			// Post a text-only message about the recalled document
			docLog.Info("Posting recalled document notice")
			err = postRecalledDocumentNotice(ctx, poster, doc)
			if err != nil {
				docLog.Error("Error posting recalled document notice", "error", err)
				return
			}

			// Check database connection before updating
			waitForDBConnection(ctx, store)

			// Mark as processed to avoid repeated attempts
			docLog.Info("Marking recalled document as processed")
			err = store.AddProcessedDocument(ctx, storage.ProcessedDocument{
				Title:     doc.Title,
				URL:       doc.URL,
				Timestamp: doc.Published,
			})
			if err != nil {
				docLog.Error("Error updating storage", "error", err)
			}

			return
		}

		docLog.Error("Error downloading document", "error", err)
		return
	}
	docLog.Info("Downloaded Document")

	// Generate AI summary of the document by calling Gemini
	docLog.Debug("Generating AI summary")
	aiSummary, err := summarizer.GenerateSummary(ctx, pdfPath)
	if err != nil {
		docLog.Error("Error generating summary", "error", err)
		// Continue with posting even if summary generation fails
	} else {
		docLog.Info("AI Summary generated successfully", "length", len(aiSummary))
	}

	// Convert the PDF to images
	docLog.Info("Converting PDF to images")
	images, err := utils.ConvertToImages(ctx, pdfPath)
	if err != nil {
		docLog.Error("Error processing document", "error", err)
		return
	}

	docLog.Info("Converted PDF to images", "pages", len(images))

	// Ensure that URL is properly encoded
	documentURL := utils.EncodeURL(doc.URL)

	// Attempt to post with the new format
	docLog.Info("Posting document to Threads")
	err = poster.Post(ctx, images, doc.Title, doc.Published, documentURL, aiSummary)
	if err != nil {
		docLog.Error("Error posting to Threads", "error", err)
		return
	}

	docLog.Info("Successfully posted to Threads")

	// Add explicit cleanup after using images
	for i := range images {
		images[i] = nil // Help GC by explicitly nulling references
	}
	images = nil

	// Check database connection before updating
	waitForDBConnection(ctx, store)

	// Update storage after successful posting
	docLog.Debug("Marking document as processed")
	err = store.AddProcessedDocument(ctx, storage.ProcessedDocument{
		Title:     doc.Title,
		URL:       doc.URL,
		Timestamp: doc.Published,
	})
	if err != nil {
		docLog.Error("Error updating storage", "error", err)
	}

	// Force garbage collection after processing large documents
	runtime.GC()
	docLog.Info("Document processing complete")
}

// postRecalledDocumentNotice posts a text-only message about a recalled document
func postRecalledDocumentNotice(ctx context.Context, poster *poster.Poster, doc *scraper.Document) error {
	// Create a message about the recalled document
	message := fmt.Sprintf("🚫 DOCUMENT RECALLED 🚫\n\nThe FIA has recalled the following document:\n\n%s\n\nPublished: %s\n\nThis document is no longer available.\n\n#F1Threads",
		doc.Title,
		doc.Published.Format("January 2, 2006 at 15:04 MST"))

	// Post a text-only message
	return poster.PostTextOnly(ctx, message)
}
