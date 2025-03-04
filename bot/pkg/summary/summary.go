package summary

import (
	"context"
	"fmt"
	"os"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type Summarizer struct {
	client *genai.Client
	model  *genai.GenerativeModel
}

type Config struct {
	APIKey string
}

// New creates a new instance of Summarizer
func New(cfg Config) (*Summarizer, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(cfg.APIKey))
	if err != nil {
		return nil, fmt.Errorf("error creating Gemini client: %w", err)
	}

	model := client.GenerativeModel("gemini-2.0-flash-lite")
	configureModel(model)

	return &Summarizer{
		client: client,
		model:  model,
	}, nil
}

// Close closes the Gemini client
func (s *Summarizer) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

// GenerateSummary generates a summary for the given PDF file
func (s *Summarizer) GenerateSummary(ctx context.Context, pdfPath string) (string, error) {
	fileURI, err := s.uploadFile(ctx, pdfPath, "application/pdf")
	if err != nil {
		return "", fmt.Errorf("error uploading file: %w", err)
	}

	session := s.model.StartChat()
	session.History = []*genai.Content{
		{
			Role: "user",
			Parts: []genai.Part{
				genai.FileData{URI: fileURI},
			},
		},
	}

	resp, err := session.SendMessage(ctx, genai.Text("Please provide a summary of this document"))
	if err != nil {
		return "", fmt.Errorf("error generating summary: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no summary generated")
	}

	return fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0]), nil
}

// uploadFile uploads a file to Gemini and returns its URI
func (s *Summarizer) uploadFile(ctx context.Context, path, mimeType string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()

	options := genai.UploadFileOptions{
		DisplayName: path,
		MIMEType:    mimeType,
	}

	fileData, err := s.client.UploadFile(ctx, "", file, &options)
	if err != nil {
		return "", fmt.Errorf("error uploading file: %w", err)
	}

	return fileData.URI, nil
}

// configureModel configures the Gemini model with optimal settings
func configureModel(model *genai.GenerativeModel) {
	model.SetTemperature(0.7)
	model.SetTopK(64)
	model.SetTopP(0.95)
	model.SetMaxOutputTokens(8192)
	model.ResponseMIMEType = "text/plain"
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text("You are a concise and focused assistant. Based on the attached document, generate a 40-50 word summary emphasizing specific actions or decisions, such as penalties issued, rule changes, or instances where no action was taken, but only when there was some incident and it specifically mentions no penalty given. Prioritize clarity and relevance to highlight key outcomes. Provide only the summary without any additional explanation or commentary."),
		},
	}
}
