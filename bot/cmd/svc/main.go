package main

import (
	"fmt"
	"log"
	"time"

	"bot/pkg/config"
	"bot/pkg/poster"
	"bot/pkg/processor"
	"bot/pkg/scraper"
	"bot/pkg/storage"
	"bot/pkg/utils"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
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

	scraper := scraper.New(cfg.FIAUrl)
	processor := processor.New(cfg.ConversionServiceURL)
	poster := poster.New(cfg.ThreadsAccessToken, cfg.ThreadsUserID, cfg.ImgurClientID)

	for {
		log.Println("Checking for new documents...")
		latestDoc, err := scraper.FetchLatestDocument()
		if err != nil {
			log.Printf("Error fetching latest document: %v", err)
			time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
			continue
		}

		if latestDoc == nil {
			log.Println("No documents found for the current Grand Prix")
			time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
			continue
		}

		storedDoc := store.GetLatest()
		if latestDoc.Title != storedDoc.Title || latestDoc.Published.After(storedDoc.Timestamp) {
			log.Printf("New document found: %s", latestDoc.Title)

			// Download the document
			pdfPath := latestDoc.Title + ".pdf"
			err := scraper.DownloadDocument(*latestDoc)
			if err != nil {
				log.Printf("Error downloading document: %v", err)
				time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
				continue
			}
			log.Printf("Downloaded PDF: %s", pdfPath)

			// Process the new document
			images, err := processor.ConvertToImages(pdfPath)
			if err != nil {
				log.Printf("Error processing document: %v", err)
				time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
				continue
			}

			log.Printf("Converted PDF to %d images", len(images))

			// Update storage after successful download and processing
			err = store.UpdateLatest(latestDoc.Title, latestDoc.Published)
			if err != nil {
				log.Printf("Error updating storage: %v", err)
			}

			// Ensure we have the full document URL and properly encode it
			documentURL := latestDoc.URL
			if documentURL == "" {
				// If URL is not available in latestDoc, construct it
				baseURL := "https://www.fia.com/sites/default/files/decision-document/"
				encodedTitle := utils.EncodeURL(latestDoc.Title + ".pdf")
				documentURL = baseURL + encodedTitle
			} else {
				// If we already have a URL, ensure spaces are encoded
				documentURL = utils.EncodeURL(documentURL)
			}

			fmt.Println("Document URL:", documentURL)

			// Attempt to post with the new format
			err = poster.Post(images, latestDoc.Title, latestDoc.Published, documentURL)
			if err != nil {
				log.Printf("Error posting to Threads: %v", err)
			} else {
				log.Println("Successfully posted to Threads")
			}
		} else {
			log.Println("No new documents found")
		}

		log.Printf("Sleeping for %d seconds...", cfg.ScrapeInterval)
		time.Sleep(time.Duration(cfg.ScrapeInterval) * time.Second)
	}
}
