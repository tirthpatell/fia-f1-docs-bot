package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"bot/pkg/logger"

	"github.com/gen2brain/go-fitz"
)

// Package logger
var log = logger.Package("utils")

type Client struct {
	ApiKey string
	// BaseURL is the public URL used in image links (Threads fetches these)
	BaseURL string
	// UploadURL is where uploads are sent, e.g. a LAN address; defaults to BaseURL
	UploadURL string
}

// NewHTTPClient returns an HTTP client that keeps enough idle connections per
// host for concurrent requests (the default transport keeps only 2).
func NewHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 16
	return &http.Client{Timeout: timeout, Transport: transport}
}

var picsurHTTPClient = NewHTTPClient(60 * time.Second)

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

func New(apiKey, baseURL, uploadURL string) *Client {
	if uploadURL == "" {
		uploadURL = baseURL
	}
	ctxLog := log.WithContext("method", "New")
	ctxLog.Info("Creating new Picsur client", "baseURL", baseURL, "uploadURL", uploadURL)

	return &Client{
		ApiKey:    apiKey,
		BaseURL:   baseURL,
		UploadURL: strings.TrimSuffix(uploadURL, "/"),
	}
}

// UploadImage uploads an already PNG-encoded image to Picsur and returns its
// public URL.
func (c *Client) UploadImage(ctx context.Context, pngData []byte) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "UploadImage")

	// Ensure we have a base URL
	if c.BaseURL == "" {
		ctxLog.Error("Picsur base URL not configured")
		return "", fmt.Errorf("picsur base URL not configured")
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
	if _, err := part.Write(pngData); err != nil {
		ctxLog.Error("Failed to copy image data", "error", err)
		return "", fmt.Errorf("failed to copy image data: %v", err)
	}

	if err := writer.Close(); err != nil {
		ctxLog.Error("Failed to close multipart writer", "error", err)
		return "", fmt.Errorf("failed to close multipart writer: %v", err)
	}

	// Create request
	uploadURL := fmt.Sprintf("%s/api/image/upload", c.UploadURL)
	ctxLog.Debug("Creating upload request", "url", uploadURL)
	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, body)
	if err != nil {
		ctxLog.Error("Failed to create request", "error", err)
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Api-Key "+c.ApiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request with timeout
	ctxLog.Debug("Uploading image to Picsur")
	resp, err := picsurHTTPClient.Do(req)
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
	imageURL := fmt.Sprintf("%s/i/%s.png", c.BaseURL, picsurResp.Data.ID)
	ctxLog.Debug("Image uploaded successfully", "url", imageURL)
	return imageURL, nil
}

// EncodeURL encodes spaces in URL
func EncodeURL(input string) string {
	return strings.ReplaceAll(input, " ", "%20")
}

// renderDPI matches go-fitz's Image() default so output quality is unchanged.
const renderDPI = 300

// renderSlots caps concurrent page renders across all documents at the CPU
// count; more would only add memory (~35 MB per 300 DPI page).
var renderSlots = make(chan struct{}, runtime.NumCPU())

// PDFPageCount returns the number of pages in a PDF.
func PDFPageCount(pdfPath string) (int, error) {
	doc, err := fitz.New(pdfPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open PDF: %v", err)
	}
	defer doc.Close()
	return doc.NumPage(), nil
}

// RenderPages renders every page of a PDF to PNG in parallel, calling fn with
// each page (0-based index) as soon as it is ready. fn is called concurrently
// and in no particular order.
//
// go-fitz serializes rendering per document behind a mutex, so each page is
// rendered on its own handle.
func RenderPages(ctx context.Context, pdfPath string, numPages int, fn func(page int, png []byte) error) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "RenderPages").
		WithContext("pdfPath", pdfPath)

	workers := min(numPages, cap(renderSlots))
	ctxLog.Debug("Rendering PDF pages", "pages", numPages, "workers", workers)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pages := make(chan int)
	go func() {
		defer close(pages)
		for i := range numPages {
			select {
			case pages <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	fail := func(err error) {
		errOnce.Do(func() { firstErr = err })
		cancel()
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := range pages {
				select {
				case renderSlots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				img, err := renderPage(pdfPath, i)
				<-renderSlots
				if err != nil {
					ctxLog.Error("Failed to convert page to image", "page", i+1, "error", err)
					fail(err)
					return
				}
				ctxLog.Debug("Rendered page", "page", i+1)
				if err := fn(i, img); err != nil {
					fail(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	// Non-nil if the caller cancelled and the page feeder stopped early
	return ctx.Err()
}

// renderPage renders one page on a fresh handle. Handles only live while a
// render slot is held, so open handles are capped by renderSlots too; opening
// is cheap next to rendering at 300 DPI.
func renderPage(pdfPath string, page int) ([]byte, error) {
	doc, err := fitz.New(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF: %v", err)
	}
	defer doc.Close()
	img, err := doc.ImagePNG(page, renderDPI)
	if err != nil {
		return nil, fmt.Errorf("failed to convert page %d to image: %v", page, err)
	}
	return img, nil
}
