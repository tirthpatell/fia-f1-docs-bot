package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"bot/pkg/logger"

	"github.com/gen2brain/go-fitz"
)

// Package logger
var log = logger.Package("utils")

type Client struct {
	ApiKey  string
	BaseURL string
}

type picsurResponse struct {
	Success    bool `json:"success"`
	StatusCode int  `json:"statusCode"`
	TimeMs     int  `json:"timeMs"`
	Data       struct {
		ID        string `json:"id"`
		UserID    string `json:"user_id"`
		Created   string `json:"created"`
		FileName  string `json:"file_name"`
		ExpiresAt any    `json:"expires_at"`
		DeleteKey string `json:"delete_key"`
	} `json:"data"`
}

func New(apiKey, baseURL string) *Client {
	ctxLog := log.WithContext("method", "New")
	ctxLog.Debug("Creating new Picsur client", "baseURL", baseURL)

	return &Client{
		ApiKey:  apiKey,
		BaseURL: baseURL,
	}
}

func (c *Client) UploadImage(ctx context.Context, img image.Image, title, description string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "UploadImage")

	// Ensure we have a base URL
	if c.BaseURL == "" {
		ctxLog.Error("Picsur base URL not configured")
		return "", fmt.Errorf("picsur base URL not configured")
	}

	// Encode image to PNG
	var buf bytes.Buffer
	ctxLog.Debug("Encoding image to PNG")
	if err := png.Encode(&buf, img); err != nil {
		ctxLog.Error("Failed to encode image", "error", err)
		return "", fmt.Errorf("failed to encode image: %v", err)
	}

	// Prepare multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add image data
	ctxLog.Debug("Creating multipart form data")
	part, err := writer.CreateFormFile("image", "image.png")
	if err != nil {
		ctxLog.Error("Failed to create form file", "error", err)
		return "", fmt.Errorf("failed to create form file: %v", err)
	}
	if _, err := io.Copy(part, &buf); err != nil {
		ctxLog.Error("Failed to copy image data", "error", err)
		return "", fmt.Errorf("failed to copy image data: %v", err)
	}

	if err := writer.Close(); err != nil {
		ctxLog.Error("Failed to close multipart writer", "error", err)
		return "", fmt.Errorf("failed to close multipart writer: %v", err)
	}

	// Create request
	uploadURL := fmt.Sprintf("%s/api/image/upload", c.BaseURL)
	ctxLog.Debug("Creating upload request", "url", uploadURL)
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		ctxLog.Error("Failed to create request", "error", err)
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Api-Key "+c.ApiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	ctxLog.Debug("Uploading image to Picsur")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		ctxLog.Error("Failed to send request", "error", err)
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// Parse response
	var picsurResp picsurResponse
	if err := json.NewDecoder(resp.Body).Decode(&picsurResp); err != nil {
		ctxLog.Error("Failed to decode response", "error", err)
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	if !picsurResp.Success {
		ctxLog.Error("Picsur API error", "status", picsurResp.StatusCode)
		return "", fmt.Errorf("picsur API error: status %d", picsurResp.StatusCode)
	}

	// Construct the image URL from the response ID
	imageURL := fmt.Sprintf("%s/i/%s.png", c.BaseURL, picsurResp.Data.ID)
	ctxLog.Debug("Image uploaded successfully", "url", imageURL)
	return imageURL, nil
}

// Custom function to encode spaces in URL
func EncodeURL(input string) string {
	return strings.ReplaceAll(input, " ", "%20")
}

// Refresh the long-lived access token for the Threads API
func RefreshToken(ctx context.Context, refreshToken string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "RefreshToken")

	// Prepare request
	url := "https://graph.threads.net/refresh_access_token"
	ctxLog.Info("Refreshing Threads access token")
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		ctxLog.Error("Failed to create request", "error", err)
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("grant_type", "th_refresh_token")
	q.Add("access_token", refreshToken)
	req.URL.RawQuery = q.Encode()

	// Send request
	ctxLog.Debug("Sending token refresh request")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		ctxLog.Error("Failed to send request", "error", err)
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// Parse response
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		ctxLog.Error("Failed to decode response", "error", err)
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	expiresInDays := tokenResp.ExpiresIn / (24 * 3600)
	ctxLog.Info("Access token refreshed successfully", "expires_in_days", expiresInDays)
	return tokenResp.AccessToken, nil
}

// ConvertToImages converts a PDF document to a slice of images
func ConvertToImages(ctx context.Context, pdfPath string) ([]image.Image, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "ConvertToImages").
		WithContext("pdfPath", pdfPath)

	ctxLog.Debug("Opening PDF document")
	doc, err := fitz.New(pdfPath)
	if err != nil {
		ctxLog.Error("Failed to open PDF", "error", err)
		return nil, fmt.Errorf("failed to open PDF: %v", err)
	}
	defer doc.Close()

	numPages := doc.NumPage()
	ctxLog.Debug("Converting PDF to images", "pages", numPages)

	var images []image.Image

	for i := 0; i < numPages; i++ {
		ctxLog.Debug("Converting page to image", "page", i+1)
		img, err := doc.Image(i)
		if err != nil {
			ctxLog.Error("Failed to convert page to image", "page", i+1, "error", err)
			return nil, fmt.Errorf("failed to convert page %d to image: %v", i, err)
		}
		images = append(images, img)
	}

	ctxLog.Debug("PDF conversion completed", "images", len(images))
	return images, nil
}
