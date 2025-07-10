package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ShortenerClient is a client for the URL shortener service
type ShortenerClient struct {
	APIKey  string
	BaseURL string
}

// NewShortenerClient creates a new ShortenerClient
func NewShortenerClient(apiKey, baseURL string) *ShortenerClient {
	ctxLog := log.WithContext("method", "NewShortenerClient")
	ctxLog.Info("Creating new URL shortener client", "baseURL", baseURL)

	return &ShortenerClient{
		APIKey:  apiKey,
		BaseURL: baseURL,
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
func (c *ShortenerClient) ShortenURL(ctx context.Context, longURL string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "ShortenURL").
		WithContext("longURL", longURL)

	// Create request body
	reqBody := ShortenRequest{
		URL: longURL,
	}

	ctxLog.Debug("Preparing request payload")
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		ctxLog.Error("Failed to marshal request", "error", err)
		return "", fmt.Errorf("failed to marshal request: %v", err)
	}

	// Create request
	endpoint := c.BaseURL + "/api/shorten"
	ctxLog.Debug("Creating request", "endpoint", endpoint)
	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		ctxLog.Error("Failed to create request", "error", err)
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.APIKey)

	// Send request
	ctxLog.Debug("Sending URL shortening request")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		ctxLog.Error("Failed to send request", "error", err)
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			ctxLog.Error("Failed to close response body", "error", err)
		}
	}(resp.Body)

	// Check response status - accept both 200 OK and 201 Created as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		ctxLog.Error("Shortener service returned error", "statusCode", resp.StatusCode)
		return "", fmt.Errorf("shortener service returned status code %d", resp.StatusCode)
	}

	// Parse response
	var shortenResp ShortenResponse
	if err := json.NewDecoder(resp.Body).Decode(&shortenResp); err != nil {
		ctxLog.Error("Failed to decode response", "error", err)
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	ctxLog.Info("URL shortened successfully",
		"originalURL", longURL,
		"shortURL", shortenResp.ShortURL)
	return shortenResp.ShortURL, nil
}
