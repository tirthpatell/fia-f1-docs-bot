package processor

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"image/png"
)

// Processor is a struct that holds the configuration for the processor
type Processor struct {
	ConversionServiceURL string
}

// New creates a new Processor
func New(conversionServiceURL string) *Processor {
	return &Processor{
		ConversionServiceURL: conversionServiceURL,
	}
}

// ConvertToImages converts a PDF document to a slice of images
func (p *Processor) ConvertToImages(pdfPath string) ([]image.Image, error) {
	// Open the PDF file
	file, err := os.Open(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF: %v", err)
	}
	defer file.Close()

	// Prepare the multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the target format
	err = writer.WriteField("targetFormat", "png")
	if err != nil {
		return nil, fmt.Errorf("failed to write target format: %v", err)
	}

	// Add the file
	part, err := writer.CreateFormFile("uploadFile", filepath.Base(pdfPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %v", err)
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return nil, fmt.Errorf("failed to copy file content: %v", err)
	}

	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %v", err)
	}

	// Send the request to the conversion service
	req, err := http.NewRequest("POST", p.ConversionServiceURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("conversion service returned non-OK status: %s", resp.Status)
	}

	// Read the response body (ZIP file)
	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	// Process the ZIP file
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to create zip reader: %v", err)
	}

	var images []image.Image
	for _, file := range zipReader.File {
		fileReader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to open file in zip: %v", err)
		}
		defer fileReader.Close()

		img, err := png.Decode(fileReader)
		if err != nil {
			return nil, fmt.Errorf("failed to decode PNG: %v", err)
		}

		images = append(images, img)
	}

	return images, nil
}
