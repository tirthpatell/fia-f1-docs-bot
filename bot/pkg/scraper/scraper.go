package scraper

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

type Document struct {
	Title     string
	URL       string
	Published time.Time
}

type Scraper struct {
	baseURL string
}

func New(baseURL string) *Scraper {
	return &Scraper{
		baseURL: baseURL,
	}
}

// FetchLatestDocuments retrieves the specified number of most recent documents
func (s *Scraper) FetchLatestDocuments(limit int) ([]*Document, error) {
	var documents []*Document

	// Create a fresh collector for each request
	c := colly.NewCollector()
	c.AllowURLRevisit = true

	c.OnHTML("ul.event-wrapper", func(e *colly.HTMLElement) {
		// Find the active (current) Grand Prix
		e.ForEach("li", func(_ int, el *colly.HTMLElement) {
			if el.ChildText(".event-title.active") != "" {
				// Process only the documents under the active Grand Prix
				el.ForEach("li.document-row", func(_ int, docEl *colly.HTMLElement) {
					title := docEl.ChildText(".title")
					relativeURL := docEl.ChildAttr("a", "href")
					publishedStr := docEl.ChildText(".published .date-display-single")

					fullURL := "https://www.fia.com" + relativeURL

					published, err := time.Parse("02.01.06 15:04", publishedStr)
					if err != nil {
						fmt.Printf("Error parsing date %s: %v\n", publishedStr, err)
						return
					}

					doc := &Document{
						Title:     title,
						URL:       fullURL,
						Published: published,
					}

					documents = append(documents, doc)
				})
				// Stop after processing the active Grand Prix
				return
			}
		})
	})

	err := c.Visit(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("error visiting %s: %v", s.baseURL, err)
	}

	if len(documents) == 0 {
		return nil, fmt.Errorf("no documents found for the current Grand Prix")
	}

	// Sort documents by publish date (most recent first)
	// Since most recent should be first, we sort in reverse chronological order
	sortDocumentsByDate(documents)

	// Limit the number of documents if needed
	if len(documents) > limit {
		documents = documents[:limit]
	}

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
func (s *Scraper) FetchLatestDocument() (*Document, error) {
	docs, err := s.FetchLatestDocuments(1)
	if err != nil {
		return nil, err
	}

	return docs[0], nil
}

// DownloadDocument downloads a document to the specified directory and returns the file path
func (s *Scraper) DownloadDocument(doc Document, directory string) (string, error) {
	// Check if the document is recalled based on its title
	if s.IsRecalledDocument(doc) {
		return "", fmt.Errorf("document has been recalled: %s", doc.Title)
	}

	resp, err := http.Get(doc.URL)
	if err != nil {
		return "", fmt.Errorf("error downloading document: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Create a sanitized filename from the document title
	filename := fmt.Sprintf("%s.pdf", sanitizeFilename(doc.Title))
	filePath := filepath.Join(directory, filename)

	// Create a file to save the PDF
	out, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("error creating file: %v", err)
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("error writing to file: %v", err)
	}

	// Verify the downloaded file is a valid PDF
	if err := s.verifyPDF(filePath); err != nil {
		// If verification fails, it might be a recalled document that wasn't properly marked
		os.Remove(filePath) // Clean up the invalid file
		return "", fmt.Errorf("invalid PDF file (possibly recalled): %v", err)
	}

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
	defer file.Close()

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

	// Check file size - extremely small PDFs are suspicious
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
