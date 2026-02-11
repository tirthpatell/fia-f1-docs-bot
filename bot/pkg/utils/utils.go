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
	"time"

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
	ctxLog.Info("Creating new Picsur client", "baseURL", baseURL)

	return &Client{
		ApiKey:  apiKey,
		BaseURL: baseURL,
	}
}

func (c *Client) UploadImage(ctx context.Context, img image.Image) (string, error) {
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

	// Send request with timeout
	ctxLog.Debug("Uploading image to Picsur")
	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Do(req)
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
	imageURL := fmt.Sprintf("%s/i/%s.jpg", c.BaseURL, picsurResp.Data.ID)
	ctxLog.Debug("Image uploaded successfully", "url", imageURL)
	return imageURL, nil
}

// EncodeURL encodes spaces in URL
func EncodeURL(input string) string {
	return strings.ReplaceAll(input, " ", "%20")
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
	defer func(doc *fitz.Document) {
		err := doc.Close()
		if err != nil {
			ctxLog.Error("Failed to close document", "error", err)
		}
	}(doc)

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
