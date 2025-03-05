package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gen2brain/go-fitz"
)

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
	return &Client{
		ApiKey:  apiKey,
		BaseURL: baseURL,
	}
}

func (c *Client) UploadImage(img image.Image, title, description string) (string, error) {
	// Ensure we have a base URL
	if c.BaseURL == "" {
		return "", fmt.Errorf("picsur base URL not configured")
	}

	// Encode image to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("failed to encode image: %v", err)
	}

	// Prepare multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add image data
	part, err := writer.CreateFormFile("image", "image.png")
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %v", err)
	}
	if _, err := io.Copy(part, &buf); err != nil {
		return "", fmt.Errorf("failed to copy image data: %v", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %v", err)
	}

	// Create request
	uploadURL := fmt.Sprintf("%s/api/image/upload", c.BaseURL)
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Api-Key "+c.ApiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// Parse response
	var picsurResp picsurResponse
	if err := json.NewDecoder(resp.Body).Decode(&picsurResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	if !picsurResp.Success {
		return "", fmt.Errorf("picsur API error: status %d", picsurResp.StatusCode)
	}

	// Construct the image URL from the response ID
	imageURL := fmt.Sprintf("%s/i/%s.png", c.BaseURL, picsurResp.Data.ID)
	return imageURL, nil
}

// Custom function to encode spaces in URL
func EncodeURL(input string) string {
	return strings.ReplaceAll(input, " ", "%20")
}

// Refresh the long-lived access token for the Threads API
func RefreshToken(refreshToken string) (string, error) {
	// Prepare request
	url := "https://graph.threads.net/refresh_access_token"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("grant_type", "th_refresh_token")
	q.Add("access_token", refreshToken)
	req.URL.RawQuery = q.Encode()

	// Send request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
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
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	return tokenResp.AccessToken, nil
}

// ConvertToImages converts a PDF document to a slice of images
func ConvertToImages(pdfPath string) ([]image.Image, error) {
	doc, err := fitz.New(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF: %v", err)
	}
	defer doc.Close()

	var images []image.Image

	for i := 0; i < doc.NumPage(); i++ {
		img, err := doc.Image(i)
		if err != nil {
			return nil, fmt.Errorf("failed to convert page %d to image: %v", i, err)
		}
		images = append(images, img)
	}

	return images, nil
}
