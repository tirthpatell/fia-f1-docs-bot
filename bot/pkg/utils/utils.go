package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
)

const imgurAPIURL = "https://api.imgur.com/3/image"

type Client struct {
	ClientID string
}

type imgurResponse struct {
	Data struct {
		Link string `json:"link"`
	} `json:"data"`
	Success bool `json:"success"`
	Status  int  `json:"status"`
}

func New(clientID string) *Client {
	return &Client{ClientID: clientID}
}

func (c *Client) UploadImage(img image.Image, title, description string) (string, error) {
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

	// Add other fields
	if err := writer.WriteField("type", "file"); err != nil {
		log.Printf("Error writing field 'type': %v", err)
	}

	if err := writer.WriteField("title", title); err != nil {
		log.Printf("Error writing field 'title': %v", err)
	}

	if err := writer.WriteField("description", description); err != nil {
		log.Printf("Error writing field 'description': %v", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %v", err)
	}

	// Create request
	req, err := http.NewRequest("POST", imgurAPIURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Client-ID "+c.ClientID)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// Parse response
	var imgurResp imgurResponse
	if err := json.NewDecoder(resp.Body).Decode(&imgurResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	if !imgurResp.Success {
		return "", fmt.Errorf("imgur API error: status %d", imgurResp.Status)
	}

	return imgurResp.Data.Link, nil
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
