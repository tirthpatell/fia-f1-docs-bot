package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"bot/pkg/config"
	"bot/pkg/poster"
	"bot/pkg/scraper"
	"bot/pkg/storage"
	"bot/pkg/summary"
	"bot/pkg/utils"
)

const (
	maxConcurrentProcessing = 5      // Maximum number of documents to process concurrently
	documentsToFetch        = 8      // Number of recent documents to check
	tempDir                 = "temp" // Temporary directory for downloaded PDFs
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}

	// Automatically run this every 45 days to refresh the long-lived access token
	go func() {
		for {
			// Refresh the long-lived access token
			newToken, err := utils.RefreshToken(cfg.ThreadsAccessToken)
			if err != nil {
				log.Printf("Error refreshing token: %v", err)
			} else {
				log.Println("Successfully refreshed token")
				cfg.ThreadsAccessToken = newToken
			}

			// Sleep for 45 days
			time.Sleep(45 * 24 * time.Hour)
		}
	}()

	store, err := storage.New(cfg.Document)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Initialize the packages
	summarizer, err := summary.New(summary.Config{
		APIKey: cfg.GeminiAPIKey,
	})
	if err != nil {
		log.Printf("Failed to initialize summarizer: %v", err)
	}
	defer summarizer.Close()

	sc := scraper.New(cfg.FIAUrl)
	poster := poster.New(cfg.ThreadsAccessToken, cfg.ThreadsUserID, cfg.PicsurAPI, cfg.PicsurURL)

	for {
		log.Println("Checking for new documents...")
		docs, err := sc.FetchLatestDocuments(documentsToFetch)
		if err != nil {
			log.Printf("Error fetching documents: %v", err)
			log.Printf("Sleeping for %d seconds...", cfg.ScrapeInterval)
			time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
			continue
		}

		if len(docs) == 0 {
			log.Println("No documents found for the current Grand Prix")
			log.Printf("Sleeping for %d seconds...", cfg.ScrapeInterval)
			time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
			continue
		}

		// Create a worker pool with limited concurrency
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, maxConcurrentProcessing)

		for _, doc := range docs {
			// Skip already processed documents
			if store.IsDocumentProcessed(doc.URL) {
				log.Printf("Skipping already processed document: %s", doc.Title)
				continue
			}

			// Limit concurrency using semaphore
			semaphore <- struct{}{}
			wg.Add(1)

			go func(document *scraper.Document) {
				defer wg.Done()
				defer func() { <-semaphore }()

				log.Printf("Processing new document: %s", document.Title)
				processDocument(document, sc, summarizer, poster, store, cfg)
			}(doc)
		}

		// Wait for all goroutines to finish
		wg.Wait()

		log.Printf("Sleeping for %d seconds...", cfg.ScrapeInterval)
		time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
	}
}

// processDocument handles all steps for a single document
func processDocument(doc *scraper.Document, scraper *scraper.Scraper, summarizer *summary.Summarizer, poster *poster.Poster, store *storage.Storage, cfg *config.Config) {
	// Create a unique directory for this document
	docDir := filepath.Join(tempDir, fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(docDir, 0755); err != nil {
		log.Printf("Error creating directory for document: %v", err)
		return
	}
	defer os.RemoveAll(docDir) // Clean up when done

	// Download the document
	pdfPath, err := scraper.DownloadDocument(*doc, docDir)
	if err != nil {
		log.Printf("Error downloading document: %v", err)
		return
	}
	log.Printf("Downloaded PDF: %s", pdfPath)

	// Generate AI summary of the document by calling Gemini
	aiSummary, err := summarizer.GenerateSummary(context.Background(), pdfPath)
	if err != nil {
		log.Printf("Error generating summary: %v", err)
		// Continue with posting even if summary generation fails
	}

	// Convert the PDF to images
	images, err := utils.ConvertToImages(pdfPath)
	if err != nil {
		log.Printf("Error processing document: %v", err)
		return
	}

	log.Printf("Converted PDF to %d images", len(images))

	// Ensure that URL is properly encoded
	documentURL := utils.EncodeURL(doc.URL)

	// Attempt to post with the new format
	err = poster.Post(images, doc.Title, doc.Published, documentURL, aiSummary)
	if err != nil {
		log.Printf("Error posting to Threads: %v", err)
		return
	}

	log.Printf("Successfully posted to Threads: %s", doc.Title)

	// Add explicit cleanup after using images
	for i := range images {
		images[i] = nil // Help GC by explicitly nulling references
	}
	images = nil

	// Update storage after successful posting
	err = store.AddProcessedDocument(storage.ProcessedDocument{
		Title:     doc.Title,
		URL:       doc.URL,
		Timestamp: doc.Published,
	})
	if err != nil {
		log.Printf("Error updating storage: %v", err)
	}

	// Force garbage collection after processing large documents
	runtime.GC()
}
