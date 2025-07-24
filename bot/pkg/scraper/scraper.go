package scraper

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bot/pkg/logger"

	"github.com/gocolly/colly/v2"
)

// Package logger
var log = logger.Package("scraper")

type Document struct {
	Title     string
	URL       string
	Published time.Time
}

type Scraper struct {
	baseURL string
}

func New(baseURL string) *Scraper {
	// No need to seed the random number generator in Go 1.20+
	return &Scraper{
		baseURL: baseURL,
	}
}

// List of common user agents to rotate through
var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:123.0) Gecko/20100101 Firefox/123.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
}

// getRandomUserAgent returns a random user agent from the list
func getRandomUserAgent() string {
	return userAgents[rand.Intn(len(userAgents))]
}

// FetchLatestDocuments retrieves the specified number of most recent documents
func (s *Scraper) FetchLatestDocuments(ctx context.Context, limit int) ([]*Document, error) {
	// Get a context-aware logger
	ctxLog := log.WithRequestContext(ctx).WithContext("method", "FetchLatestDocuments")

	var documents []*Document

	// Create a fresh collector for each request
	c := colly.NewCollector(
		colly.UserAgent(getRandomUserAgent()),
	)

	// Set AllowURLRevisit to true
	c.AllowURLRevisit = true

	// Configure transport to disable caching
	c.WithTransport(&http.Transport{
		DisableKeepAlives: true,
	})

	// Add cache-busting query parameter
	cacheBuster := fmt.Sprintf("?_cb=%d", time.Now().UnixNano())
	targetURL := s.baseURL + cacheBuster

	// Set custom headers to prevent caching
	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		r.Headers.Set("Pragma", "no-cache")
		r.Headers.Set("Expires", "0")
	})

	c.OnHTML("ul.event-wrapper", func(e *colly.HTMLElement) {
		// Find the active (current) Grand Prix
		e.ForEach("li", func(_ int, el *colly.HTMLElement) {
			if el.ChildText(".event-title.active") != "" {
				activeGP := el.ChildText(".event-title.active")
				ctxLog.Info(fmt.Sprintf("Found active Grand Prix: %s", activeGP))

				// Process only the documents under the active Grand Prix
				el.ForEach("li.document-row", func(_ int, docEl *colly.HTMLElement) {
					title := docEl.ChildText(".title")
					relativeURL := docEl.ChildAttr("a", "href")
					publishedStr := docEl.ChildText(".published .date-display-single")

					fullURL := "https://www.fia.com" + relativeURL

					// Load the Europe/Paris timezone
					parisTZ, err := time.LoadLocation("Europe/Paris")
					if err != nil {
						ctxLog.Error("Failed to load Europe/Paris timezone", "error", err)
						parisTZ = time.UTC // Fallback to UTC if loading fails
					}

					// Parse the time assuming it's in the Paris timezone
					published, err := time.ParseInLocation("02.01.06 15:04", publishedStr, parisTZ)
					if err != nil {
						ctxLog.Error("Error parsing date", "date", publishedStr, "error", err)
						published, _ = time.Parse("02.01.06 15:04", publishedStr) // Fallback to UTC if parsing fails
					}

					// Convert to UTC for consistency
					publishedUTC := published.UTC()

					doc := &Document{
						Title:     title,
						URL:       fullURL,
						Published: publishedUTC, // Store as UTC
					}

					documents = append(documents, doc)
					ctxLog.Debug("Found document", "title", title, "publishedUTC", publishedUTC)
				})
				// Stop after processing the active Grand Prix
				return
			}
		})
	})

	// Add error handling
	c.OnError(func(r *colly.Response, err error) {
		ctxLog.Error("Request failed", "url", r.Request.URL, "status", r.StatusCode, "error", err)
	})

	// Log the URL being visited
	ctxLog.Info(fmt.Sprintf("Visiting URL: %s", targetURL))

	err := c.Visit(targetURL)
	if err != nil {
		ctxLog.Error("Error visiting URL", "url", targetURL, "error", err)
		return nil, fmt.Errorf("error visiting %s: %v", targetURL, err)
	}

	if len(documents) == 0 {
		ctxLog.Info("No documents found for current Grand Prix")
		return nil, fmt.Errorf("no documents found for the current Grand Prix")
	}

	// Sort documents by publish date (most recent first)
	// Since most recent should be first, we sort in reverse chronological order
	sortDocumentsByDate(documents)

	// Limit the number of documents if needed
	if len(documents) > limit {
		documents = documents[:limit]
	}

	ctxLog.Debug("Documents fetched successfully", "count", len(documents))
	return documents, nil
}

// Helper function to sort documents by date (most recent first)
func sortDocumentsByDate(docs []*Document) {
	// Use bubble sort for simplicity
	n := len(docs)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			// If the current document is older than the next one, swap them
			if docs[j].Published.Before(docs[j+1].Published) {
				docs[j], docs[j+1] = docs[j+1], docs[j]
			}
		}
	}
}

// FetchLatestDocument returns only the most recent document
func (s *Scraper) FetchLatestDocument(ctx context.Context) (*Document, error) {
	ctxLog := log.WithRequestContext(ctx).WithContext("method", "FetchLatestDocument")

	docs, err := s.FetchLatestDocuments(ctx, 1)
	if err != nil {
		ctxLog.Error("Error fetching latest document", "error", err)
		return nil, err
	}

	ctxLog.Info("Latest document fetched", "title", docs[0].Title)
	return docs[0], nil
}

// DownloadDocument downloads a document to the specified directory and returns the file path
func (s *Scraper) DownloadDocument(ctx context.Context, doc Document, directory string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "DownloadDocument")

	// Check if the document is recalled based on its title
	if s.IsRecalledDocument(doc) {
		ctxLog.Info("Document has been recalled", "title", doc.Title)
		return "", fmt.Errorf("document has been recalled: %s", doc.Title)
	}

	// Create a custom HTTP client with cache-busting headers
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	// Create a new request with cache-busting headers
	req, err := http.NewRequest("GET", doc.URL, nil)
	if err != nil {
		ctxLog.Error("Error creating request", "error", err)
		return "", fmt.Errorf("error creating request: %v", err)
	}

	// Add cache-busting headers
	req.Header.Set("User-Agent", getRandomUserAgent())
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Expires", "0")

	// Add a random query parameter to bypass cache
	q := req.URL.Query()
	q.Add("_cb", fmt.Sprintf("%d", time.Now().UnixNano()))
	req.URL.RawQuery = q.Encode()

	// Execute the request
	ctxLog.Debug("Downloading document", "url", req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		ctxLog.Error("Error downloading document", "error", err)
		return "", fmt.Errorf("error downloading document: %v", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			ctxLog.Error("Error closing response body", "error", err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		ctxLog.Error("Unexpected status code", "status", resp.StatusCode)
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Create a sanitized filename from the document title
	filename := fmt.Sprintf("%s.pdf", sanitizeFilename(doc.Title))
	filePath := filepath.Join(directory, filename)

	// Create a file to save the PDF
	out, err := os.Create(filePath)
	if err != nil {
		ctxLog.Error("Error creating file", "path", filePath, "error", err)
		return "", fmt.Errorf("error creating file: %v", err)
	}
	defer func(out *os.File) {
		err := out.Close()
		if err != nil {
			ctxLog.Error("Error closing file writer", "path", filePath, "error", err)
		}
	}(out)

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		ctxLog.Error("Error writing to file", "path", filePath, "error", err)
		return "", fmt.Errorf("error writing to file: %v", err)
	}

	// Verify the downloaded file is a valid PDF
	if err := s.verifyPDF(filePath); err != nil {
		// If verification fails, it might be a recalled document that wasn't properly marked
		err := os.Remove(filePath)
		if err != nil {
			return "", fmt.Errorf("error removing file: %v", err)
		} // Clean up the invalid file
		ctxLog.Warn("Invalid PDF file detected, possibly recalled", "error", err)
		return "", fmt.Errorf("invalid PDF file (possibly recalled): %v", err)
	}

	ctxLog.Debug("Document downloaded successfully", "path", filePath)
	return filePath, nil
}

// IsRecalledDocument checks if a document has been recalled based on its title
func (s *Scraper) IsRecalledDocument(doc Document) bool {
	// Check if the title contains "Recalled" or similar indicators
	return strings.HasPrefix(strings.ToLower(doc.Title), "recalled") ||
		strings.Contains(strings.ToLower(doc.Title), "recalled -")
}

// verifyPDF checks if a file is a valid PDF
func (s *Scraper) verifyPDF(filePath string) error {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			fmt.Printf("Error closing file: %v", err)
		}
	}(file)

	// Read the first few bytes to check for PDF signature
	header := make([]byte, 5)
	_, err = file.Read(header)
	if err != nil {
		return err
	}

	// Check if the file starts with the PDF signature (%PDF-)
	if string(header) != "%PDF-" {
		return fmt.Errorf("file does not have a valid PDF signature")
	}

	// Check file size - tiny PDFs are suspicious
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	if fileInfo.Size() < 1000 { // Less than 1KB is suspicious for a F1 document
		return fmt.Errorf("file is too small to be a valid F1 document PDF")
	}

	return nil
}

// Helper function to sanitize filenames
func sanitizeFilename(name string) string {
	// Simple implementation - replace problematic characters
	for _, char := range []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"} {
		name = strings.ReplaceAll(name, char, "_")
	}
	return filepath.Clean(name)
}
