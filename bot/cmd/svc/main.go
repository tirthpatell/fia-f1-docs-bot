package main

import (
	"context"
	"fmt"
	"maps"
	"net/http"
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
	tempDir                 = "temp"          // Temporary directory for downloaded PDFs
	shortRetryInterval      = 1 * time.Minute // Short retry interval for DB connection
	longRetryInterval       = 5 * time.Minute // Long retry interval for DB connection
	serviceName             = "f1-docs-bot"   // Service name for logging
)

// Global logger
var log *logger.Logger

// waitForDBConnection waits until the database is reachable, retrying with a
// short interval first and a long interval after that. sql.DB is a
// self-healing pool, so a successful ping is all that is needed to recover.
// Returns false if the context was cancelled (shutdown requested).
func waitForDBConnection(ctx context.Context, store storage.StorageInterface) bool {
	// Get context-aware logger
	dbLog := log.WithRequestContext(ctx).WithContext("component", "database")

	err := store.CheckConnection(ctx)
	if err == nil {
		return true
	}

	dbLog.Error("Database connection lost", "error", err)

	interval := shortRetryInterval
	for {
		dbLog.Info("Waiting before retrying", "interval", interval)

		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return false
		}

		if err := store.CheckConnection(ctx); err != nil {
			dbLog.Error("Database still unreachable", "error", err)
			interval = longRetryInterval
		} else {
			dbLog.Info("Database connection re-established")
			return true
		}
	}
}

// sleepOrShutdown sleeps for the given duration. Returns false if the context
// was cancelled (shutdown requested) before the duration elapsed.
func sleepOrShutdown(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
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

	// Create application context (startup logging only, not cancellable)
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
		Models: cfg.GeminiModels,
	})
	if err != nil {
		appLog.Error("Failed to initialize summarizer", "error", err)
		os.Exit(1)
	}
	defer summarizer.Close()

	appLog.Info("Initializing scraper and poster")
	sc := scraper.New(cfg.FIAUrl)
	appLog.Info("Scraper initialized successfully")

	pstr, err := poster.New(cfg.ThreadsAccessToken, cfg.ThreadsUserID, cfg.ThreadsClientID, cfg.ThreadsClientSecret, cfg.ThreadsRedirectURI, cfg.PicsurAPI, cfg.PicsurURL, cfg.PicsurUploadURL, cfg.ShortenerAPIKey, cfg.ShortenerURL)
	if err != nil {
		appLog.Error("Failed to initialize poster", "error", err)
		os.Exit(1)
	}
	appLog.Info("Poster initialized successfully")

	// Setup graceful shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	// Context for background goroutines, cancelled on shutdown
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	// Channel to coordinate shutdown
	done := make(chan bool, 1)

	appLog.Info("Service initialization complete, entering main loop")

	// Setup health check endpoint
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		healthLog := log.WithContext("component", "health_check")

		// Check database connection
		dbHealthy := store.CheckConnection(r.Context()) == nil

		uptime := time.Since(startTime)
		goroutines := runtime.NumGoroutine()

		healthLog.Debug("Health check requested",
			"db_connected", dbHealthy,
			"uptime_seconds", uptime.Seconds(),
			"goroutines", goroutines,
		)

		if dbHealthy {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "OK\nUptime: %s\nGoroutines: %d\n", uptime, goroutines)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "Database connection lost\nUptime: %s\nGoroutines: %d\n", uptime, goroutines)
			healthLog.Warn("Health check failed - database connection lost")
		}
	})

	// Start health check server with graceful shutdown support
	healthServer := &http.Server{
		Addr:    ":6060",
		Handler: mux,
	}
	go func() {
		healthLog := log.WithContext("component", "health_server")
		healthLog.Info("Starting health check server", "port", "6060")
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			healthLog.Error("Failed to start health check server", "error", err)
		}
	}()

	// Start a goroutine to periodically check and refresh token
	go func() {
		tokenCtx, _ := logger.NewRequestContextFrom(bgCtx)
		tokenLog := log.WithRequestContext(tokenCtx).WithContext("component", "token_refresher")

		// Initial delay to let the service start
		select {
		case <-time.After(5 * time.Second):
		case <-bgCtx.Done():
			return
		}

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

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

			select {
			case <-ticker.C:
			case <-bgCtx.Done():
				tokenLog.Info("Token refresher shutting down")
				return
			}
		}
	}()

	// Workers outlive the cycle that started them so a slow document doesn't
	// delay the next scrape. inFlight (DocKeys) stops double-starts.
	var (
		workers    sync.WaitGroup
		semaphore  = make(chan struct{}, maxConcurrentProcessing)
		inFlightMu sync.Mutex
		inFlight   = make(map[string]bool)
	)

	// Start main processing loop in a goroutine
	go func() {
		defer func() {
			workers.Wait()
			done <- true
		}()

		for {
			// bgCtx is cancelled by main() on shutdown. Watching it here
			// (rather than shutdownChan, whose single signal is consumed by
			// main's blocking receive) is what lets this loop actually exit.
			select {
			case <-bgCtx.Done():
				return
			default:
				// Continue with normal processing
			}

			// Create a session context for this cycle. sessionID ties together all
			// logs for one scrape cycle (cycle-level ops + all worker goroutines).
			cycleCtx, _ := logger.NewSessionContextFrom(bgCtx)
			cycleLog := log.WithRequestContext(cycleCtx).WithContext("component", "main_cycle")

			cycleLog.Info("Checking for new documents")

			// Check database connection before processing
			// This will wait until connection is established or shutdown
			if !waitForDBConnection(bgCtx, store) {
				return
			}

			docs, err := sc.FetchLatestDocuments(cycleCtx, cfg.DocumentsToFetch)
			if err != nil {
				cycleLog.Error("Error fetching documents", "error", err)
				cycleLog.Info("Sleeping before retrying", "seconds", cfg.ScrapeInterval)
				if !sleepOrShutdown(bgCtx, time.Duration(cfg.ScrapeInterval)*time.Second) {
					return
				}
				continue
			}

			if len(docs) == 0 {
				cycleLog.Info("No documents found for the current Grand Prix")
				cycleLog.Info("Sleeping before retrying", "seconds", cfg.ScrapeInterval)
				if !sleepOrShutdown(bgCtx, time.Duration(cfg.ScrapeInterval)*time.Second) {
					return
				}
				continue
			}

			cycleLog.Info("Documents fetched", "count", len(docs))

			// Snapshot before querying the DB: workers leave inFlight only after
			// recording their document, so anything missing from the snapshot
			// is either unstarted or already visible to FilterProcessed.
			inFlightMu.Lock()
			busy := maps.Clone(inFlight)
			inFlightMu.Unlock()

			// Check which documents are already processed in a single query.
			// On error, skip the cycle rather than assume "not processed" —
			// proceeding on a failed check could re-post documents.
			alreadyProcessed, err := store.FilterProcessed(cycleCtx, docs)
			if err != nil {
				cycleLog.Error("Error checking processed documents", "error", err)
				cycleLog.Info("Sleeping before retrying", "seconds", cfg.ScrapeInterval)
				if !sleepOrShutdown(bgCtx, time.Duration(cfg.ScrapeInterval)*time.Second) {
					return
				}
				continue
			}

			// Track skipped documents for a single summary log line. A slice
			// (not a title-keyed map) so same-title documents with different
			// URLs are each counted.
			var skippedDocs, inProgressDocs []string

			// The listing can repeat a document; handle each one once
			seenThisCycle := make(map[string]bool, len(docs))

			for _, doc := range docs {
				key := storage.DocKey(doc.Title, doc.URL)
				if seenThisCycle[key] {
					continue
				}
				seenThisCycle[key] = true

				// Skip already processed documents (checked before the recall
				// handling so it covers recalled documents too)
				if alreadyProcessed[key] {
					skippedDocs = append(skippedDocs, doc.Title)
					continue
				}

				if busy[key] {
					inProgressDocs = append(inProgressDocs, doc.Title)
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

					// Include in the skipped-documents summary log
					skippedDocs = append(skippedDocs, doc.Title)

					continue
				}

				inFlightMu.Lock()
				inFlight[key] = true
				inFlightMu.Unlock()
				workers.Add(1)

				go func(document *scraper.Document, key string) {
					defer workers.Done()
					// Must run after processDocument (see snapshot comment)
					defer func() {
						inFlightMu.Lock()
						delete(inFlight, key)
						inFlightMu.Unlock()
					}()

					// Limit concurrency using semaphore
					select {
					case semaphore <- struct{}{}:
					case <-bgCtx.Done():
						return
					}
					defer func() { <-semaphore }()

					// Each goroutine gets its own requestID so its logs can be
					// isolated from other concurrent workers. sessionID from
					// cycleCtx is inherited, linking this back to the cycle.
					docCtx, _ := logger.NewRequestContextFrom(cycleCtx)
					docLog := log.WithRequestContext(docCtx).
						WithContext("component", "document_processor")

					docLog.Info("Processing new document", "title", document.Title)
					processDocument(docCtx, document, sc, summarizer, pstr, store)
				}(doc, key)
			}

			// Log skipped documents after the loop (if any)
			if len(skippedDocs) > 0 {
				cycleLog.Info("Skipping already processed document(s)", "count", len(skippedDocs), "documents", skippedDocs)
			}

			if len(inProgressDocs) > 0 {
				cycleLog.Info("Document(s) still being processed from an earlier cycle", "count", len(inProgressDocs), "documents", inProgressDocs)
			}

			cycleLog.Info("Sleeping before next check", "seconds", cfg.ScrapeInterval)
			if !sleepOrShutdown(bgCtx, time.Duration(cfg.ScrapeInterval)*time.Second) {
				return
			}
		}
	}()

	// Wait for shutdown signal
	sig := <-shutdownChan
	uptime := time.Since(startTime)

	appLog.Info("Shutdown signal received",
		"signal", sig.String(),
		"uptime_seconds", uptime.Seconds(),
	)

	// Cancel background goroutines (token refresher, etc.)
	bgCancel()

	// Shutdown health check server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		appLog.Error("Error shutting down health server", "error", err)
	}

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
func processDocument(ctx context.Context, doc *scraper.Document, sc *scraper.Scraper, summarizer *summary.Summarizer, pstr *poster.Poster, store storage.StorageInterface) {
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
	pdfPath, err := sc.DownloadDocument(ctx, *doc, docDir)
	if err != nil {
		// Check if this is a recalled document
		if strings.Contains(err.Error(), "document has been recalled") ||
			strings.Contains(err.Error(), "invalid PDF file (possibly recalled)") {
			docLog.Info("Detected recalled document")

			// Post a text-only message about the recalled document
			docLog.Info("Posting recalled document notice")
			err = postRecalledDocumentNotice(ctx, pstr, doc)
			if err != nil {
				docLog.Error("Error posting recalled document notice", "error", err)
				return
			}

			// Check database connection before updating
			if !waitForDBConnection(ctx, store) {
				return
			}

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

	// Summary, short link and media pipeline are independent; run them
	// concurrently.
	sideCtx, cancelSide := context.WithCancel(ctx)
	defer cancelSide()

	var (
		sideWG    sync.WaitGroup
		aiSummary string
		shortURL  string
	)
	sideWG.Add(2)
	go func() {
		defer sideWG.Done()
		docLog.Debug("Generating AI summary")
		s, err := summarizer.GenerateSummary(sideCtx, pdfPath)
		if err != nil {
			docLog.Error("Error generating summary", "error", err)
			// Continue with posting even if summary generation fails
			return
		}
		aiSummary = s
	}()
	go func() {
		defer sideWG.Done()
		shortURL = pstr.ShortenURL(sideCtx, utils.EncodeURL(doc.URL))
	}()

	docLog.Info("Converting PDF to images")
	media, numPages, err := prepareMedia(ctx, pstr, pdfPath)
	if err != nil {
		docLog.Error("Error processing document", "error", err)
		// Wait for the side work before the deferred cleanup deletes the PDF
		cancelSide()
		sideWG.Wait()
		return
	}
	docLog.Info("Converted PDF to images and uploaded them", "pages", numPages)

	sideWG.Wait()

	docLog.Info("Posting document to Threads")
	err = pstr.Post(ctx, media, doc.Title, doc.Published, shortURL, aiSummary)
	if err != nil {
		docLog.Error("Error posting to Threads", "error", err)
		return
	}

	docLog.Info("Successfully posted to Threads")

	// Check database connection before updating
	if !waitForDBConnection(ctx, store) {
		docLog.Warn("Shutdown during DB write — document was posted but not recorded; may be re-posted on next start",
			"title", doc.Title, "url", doc.URL)
		return
	}

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

	docLog.Info("Document processing complete")
}

// prepareMedia renders the PDF's pages and stages them for posting. Returns
// the page count.
func prepareMedia(ctx context.Context, pstr *poster.Poster, pdfPath string) (*poster.Media, int, error) {
	numPages, err := utils.PDFPageCount(pdfPath)
	if err != nil {
		return nil, 0, err
	}
	media, err := pstr.PrepareMedia(ctx, numPages, func(ctx context.Context, emit func(int, []byte) error) error {
		return utils.RenderPages(ctx, pdfPath, numPages, emit)
	})
	return media, numPages, err
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
