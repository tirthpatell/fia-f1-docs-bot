package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	shortenerBaseURL = "https://sh.threadsutil.cc"
)

// ShortenerClient is a client for the URL shortener service
type ShortenerClient struct {
	APIKey string
}

// NewShortenerClient creates a new ShortenerClient
func NewShortenerClient(apiKey string) *ShortenerClient {
	return &ShortenerClient{
		APIKey: apiKey,
	}
}

// ShortenRequest represents a request to shorten a URL
type ShortenRequest struct {
	URL string `json:"url"`
}

// ShortenResponse represents a response from the URL shortener service
type ShortenResponse struct {
	ShortURL    string `json:"short_url"`
	OriginalURL string `json:"original_url"`
	CreatedAt   string `json:"created_at"`
}

// ShortenURL shortens a URL using the URL shortener service
func (c *ShortenerClient) ShortenURL(longURL string) (string, error) {
	// Create request body
	reqBody := ShortenRequest{
		URL: longURL,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %v", err)
	}

	// Create request
	req, err := http.NewRequest("POST", shortenerBaseURL+"/api/shorten", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.APIKey)

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// Check response status - accept both 200 OK and 201 Created as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("shortener service returned status code %d", resp.StatusCode)
	}

	// Parse response
	var shortenResp ShortenResponse
	if err := json.NewDecoder(resp.Body).Decode(&shortenResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	return shortenResp.ShortURL, nil
}
