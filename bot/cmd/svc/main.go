package main

import (
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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
	// Record start time for uptime tracking
	startTime := time.Now()

	// Load configuration first (needed for logger configuration)
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Parse log level from config
	logLevel, err := logger.ParseLevel(cfg.LogLevel)
	if err != nil {
		fmt.Printf("Invalid log level '%s', using 'info': %v\n", cfg.LogLevel, err)
		logLevel = logger.LevelInfo
	}

	// Initialize structured logger with config-based settings
	log = logger.New(logger.Config{
		Level:          logLevel,
		AddSource:      cfg.LogAddSource,
		ServiceName:    serviceName,
		Environment:    cfg.Environment,
		Version:        cfg.Version,
		SanitizeFields: true, // Enable sensitive data sanitization
	})

	// Set as the default logger for the entire application
	logger.SetDefaultLogger(log)

	// Create application context
	appCtx, _ := logger.NewRequestContext()
	appLog := log.WithRequestContext(appCtx).WithContext("component", "main")

	// Get hostname for lifecycle logging
	hostname, _ := os.Hostname()

	// Log application startup with detailed metadata
	appLog.Info("Application starting",
		"version", cfg.Version,
		"environment", cfg.Environment,
		"go_version", runtime.Version(),
		"pid", os.Getpid(),
		"hostname", hostname,
		"log_level", logLevel,
		"num_cpu", runtime.NumCPU(),
	)

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		appLog.Error("Failed to create temp directory", "error", err)
		os.Exit(1)
	}

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
	appLog.Info("Scraper initialized successfully")

	pstr, err := poster.New(cfg.ThreadsAccessToken, cfg.ThreadsUserID, cfg.ThreadsClientID, cfg.ThreadsClientSecret, cfg.ThreadsRedirectURI, cfg.PicsurAPI, cfg.PicsurURL, cfg.ShortenerAPIKey, cfg.ShortenerURL)
	if err != nil {
		appLog.Error("Failed to initialize poster", "error", err)
		os.Exit(1)
	}
	appLog.Info("Poster initialized successfully")

	// Setup graceful shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	// Channel to coordinate shutdown
	done := make(chan bool, 1)

	appLog.Info("Service initialization complete, entering main loop")

	// Setup health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		healthLog := log.WithContext("component", "health_check")

		// Check database connection
		dbHealthy := store.CheckConnection() == nil

		uptime := time.Since(startTime)
		goroutines := runtime.NumGoroutine()

		healthLog.Debug("Health check requested",
			"db_connected", dbHealthy,
			"uptime_seconds", uptime.Seconds(),
			"goroutines", goroutines,
		)

		if dbHealthy {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "OK\nUptime: %s\nGoroutines: %d\n", uptime, goroutines)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "Database connection lost\nUptime: %s\nGoroutines: %d\n", uptime, goroutines)
			healthLog.Warn("Health check failed - database connection lost")
		}
	})

	// Start pprof server for profiling if enabled
	if cfg.PprofEnabled {
		go func() {
			pprofLog := log.WithContext("component", "pprof")
			pprofPort := cfg.PprofPort
			if pprofPort == "" {
				pprofPort = "6060" // Default pprof port
			}

			pprofLog.Info("Starting pprof and health check server", "port", pprofPort)
			pprofLog.Info("Available endpoints:", "endpoints", []string{
				"http://localhost:" + pprofPort + "/health",
				"http://localhost:" + pprofPort + "/debug/pprof/",
				"http://localhost:" + pprofPort + "/debug/pprof/heap",
				"http://localhost:" + pprofPort + "/debug/pprof/goroutine",
				"http://localhost:" + pprofPort + "/debug/pprof/threadcreate",
				"http://localhost:" + pprofPort + "/debug/pprof/block",
				"http://localhost:" + pprofPort + "/debug/pprof/mutex",
			})
			if err := http.ListenAndServe(":"+pprofPort, nil); err != nil {
				pprofLog.Error("Failed to start pprof server", "error", err)
			}
		}()
	} else {
		// Even if pprof is disabled, start a minimal health check server
		go func() {
			healthLog := log.WithContext("component", "health_server")
			healthPort := "6060" // Use same port as pprof

			healthLog.Info("Starting health check server", "port", healthPort)
			if err := http.ListenAndServe(":"+healthPort, nil); err != nil {
				healthLog.Error("Failed to start health check server", "error", err)
			}
		}()
	}

	// Start a goroutine to periodically check and refresh token
	go func() {
		tokenCtx, _ := logger.NewRequestContext()
		tokenLog := log.WithRequestContext(tokenCtx).WithContext("component", "token_refresher")

		// Initial delay to let the service start
		time.Sleep(5 * time.Second)

		for {
			tokenLog.Debug("Checking token status")

			// Check if token needs refresh
			if pstr.ThreadsClient.IsTokenExpired() {
				tokenLog.Info("Token is expired, attempting to refresh")
				if err := pstr.ThreadsClient.RefreshToken(tokenCtx); err != nil {
					tokenLog.Error("Failed to refresh expired token", "error", err)
				} else {
					tokenLog.Info("Token refreshed successfully")
				}
			} else if pstr.ThreadsClient.IsTokenExpiringSoon(240 * time.Hour) {
				tokenLog.Info("Token is expiring soon, refreshing proactively")
				if err := pstr.ThreadsClient.RefreshToken(tokenCtx); err != nil {
					tokenLog.Warn("Failed to proactively refresh token", "error", err)
				} else {
					tokenLog.Info("Token refreshed successfully")
				}
			} else {
				tokenLog.Debug("Token is still valid")
			}

			// Check every 24 hours
			time.Sleep(24 * time.Hour)
		}
	}()

	// Start main processing loop in a goroutine
	go func() {
		defer func() {
			done <- true
		}()

		for {
			select {
			case <-shutdownChan:
				// Shutdown signal received, exit the loop
				return
			default:
				// Continue with normal processing
			}

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
					err := postRecalledDocumentNotice(cycleCtx, pstr, doc)
					if err != nil {
						cycleLog.Error("Error posting recalled document notice", "error", err)
						// Skip marking as processed if posting the notice failed, allow retry next cycle
						continue
					}

					// Mark as processed only if the notice was successfully posted
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
					processDocument(docCtx, document, sc, summarizer, pstr, store)
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
	}()

	// Wait for shutdown signal
	sig := <-shutdownChan
	uptime := time.Since(startTime)

	appLog.Info("Shutdown signal received",
		"signal", sig.String(),
		"uptime_seconds", uptime.Seconds(),
	)

	appLog.Info("Draining connections and cleaning up...")

	// Wait for main loop to finish (with timeout)
	select {
	case <-done:
		appLog.Info("Main processing loop stopped gracefully")
	case <-time.After(30 * time.Second):
		appLog.Warn("Shutdown timeout reached, forcing exit")
	}

	appLog.Info("Application shutdown complete",
		"uptime", uptime.String(),
		"final_goroutines", runtime.NumGoroutine(),
	)
}

// processDocument handles all steps for a single document
func processDocument(ctx context.Context, doc *scraper.Document, scraper *scraper.Scraper, summarizer *summary.Summarizer, poster *poster.Poster, store storage.StorageInterface) {
	// Get logger from context for this document
	docLog := log.WithRequestContext(ctx).
		WithContext("component", "document_processor")

	// Create a unique directory for this document
	docDir := filepath.Join(tempDir, fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(docDir, 0755); err != nil {
		docLog.Error("Error creating directory for document", "error", err)
		return
	}
	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			docLog.Error("Error removing directory for document", "error", err)
		}
	}(docDir) // Clean up when done

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
	message := fmt.Sprintf("🚫 DOCUMENT RECALLED 🚫\n\nThe FIA has recalled the following document:\n\n%s\n\nPublished: %s\n\nThis document is no longer available.",
		doc.Title,
		doc.Published.Format("02-01-2006 15:04 MST"))

	// Post a text-only message
	return poster.PostTextOnly(ctx, message)
}
