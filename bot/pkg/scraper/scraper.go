package scraper

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gocolly/colly/v2"
)

type Document struct {
	Title     string
	URL       string
	Published time.Time
}

type Scraper struct {
	baseURL   string
	collector *colly.Collector
}

func New(baseURL string) *Scraper {
	c := colly.NewCollector()
	c.AllowURLRevisit = true // Allow revisiting the same URL

	return &Scraper{
		baseURL:   baseURL,
		collector: c,
	}
}

func (s *Scraper) FetchLatestDocument() (*Document, error) {
	var latestDoc *Document

	s.collector.OnHTML("ul.event-wrapper", func(e *colly.HTMLElement) {
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

					// Update latestDoc if this document is newer
					if latestDoc == nil || doc.Published.After(latestDoc.Published) {
						latestDoc = doc
					}
				})
				// Stop after processing the active Grand Prix
				return
			}
		})
	})

	err := s.collector.Visit(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("error visiting %s: %v", s.baseURL, err)
	}

	if latestDoc == nil {
		return nil, fmt.Errorf("no documents found for the current Grand Prix")
	}

	return latestDoc, nil
}

func (s *Scraper) DownloadDocument(doc Document) error {
	resp, err := http.Get(doc.URL)
	if err != nil {
		return fmt.Errorf("error downloading document: %v", err)
	}
	defer resp.Body.Close()

	// Create a file to save the PDF
	out, err := os.Create(doc.Title + ".pdf")
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}
