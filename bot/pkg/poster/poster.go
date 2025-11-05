package poster

import (
	"context"
	"fmt"
	"image"
	"strings"
	"time"

	"bot/pkg/logger"
	"bot/pkg/utils"

	"github.com/tirthpatell/threads-go"
)

// Package logger
var log = logger.Package("poster")

const (
	maxCharacterLimit = 500
	ellipsis          = "..."
	TopicTag          = "F1Threads"
)

// Poster is a struct that holds the configuration for the poster
type Poster struct {
	ThreadsClient   *threads.Client
	PicsurClient    *utils.Client
	ShortenerClient *utils.ShortenerClient
}

// New creates a new Poster
func New(accessToken, userID, clientID, clientSecret, redirectURI, picsurAPI, picsurURL, shortenerAPIKey, shortenerURL string) (*Poster, error) {
	ctxLog := log.WithContext("method", "New")
	ctxLog.Info("Creating new poster client")

	// Create threads client with existing token
	threadsClient, err := threads.NewClientWithToken(accessToken, &threads.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
	})
	if err != nil {
		ctxLog.Error("Failed to create threads client", "error", err)
		return nil, fmt.Errorf("failed to create threads client: %w", err)
	}
	ctxLog.Info("Threads client initialized successfully")

	return &Poster{
		ThreadsClient:   threadsClient,
		PicsurClient:    utils.New(picsurAPI, picsurURL),
		ShortenerClient: utils.NewShortenerClient(shortenerAPIKey, shortenerURL),
	}, nil
}

// Post posts the images to Threads
func (p *Poster) Post(ctx context.Context, images []image.Image, title string, publishTime time.Time, documentURL, aiSummary string) error {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "Post")

	// Limit to 20 images if there are more
	if len(images) > 20 {
		ctxLog.Warn("Limiting images due to Threads API limitations", "original", len(images), "limited", 20)
		images = images[:20]
	}

	// Upload images to Picsur
	ctxLog.Debug("Uploading images to Picsur", "count", len(images))
	uploadStart := time.Now()
	imageURLs, err := p.uploadImages(ctx, images)
	uploadDuration := time.Since(uploadStart)

	if err != nil {
		ctxLog.ErrorWithType("Failed to upload images", err,
			"upload_duration_ms", uploadDuration.Milliseconds())
		return err
	}

	ctxLog.Info("Images uploaded successfully",
		"count", len(imageURLs),
		"upload_duration_ms", uploadDuration.Milliseconds())

	// Format the text for the post
	ctxLog.Debug("Formatting post text")
	postText, err := p.formatPostText(ctx, title, publishTime, documentURL, aiSummary)
	if err != nil {
		ctxLog.ErrorWithType("Failed to format post text", err)
		return err
	}

	ctxLog.Debug("Post character count", "chars", len(postText))

	// Determine whether to post a single image or a carousel based on the number of images
	var postErr error
	postStart := time.Now()

	if len(imageURLs) == 1 {
		// Single image post
		ctxLog.Info("Posting single image to Threads")
		postErr = p.postSingleImage(ctx, imageURLs[0], postText)
	} else if len(imageURLs) >= 2 && len(imageURLs) <= 20 {
		// Carousel post
		ctxLog.Info("Posting carousel to Threads", "images", len(imageURLs))
		postErr = p.postCarousel(ctx, imageURLs, postText)
	} else {
		ctxLog.Error("Invalid number of images", "count", len(imageURLs))
		return fmt.Errorf("invalid number of images: %d. Must be between 1 and 20", len(imageURLs))
	}

	postDuration := time.Since(postStart)
	totalDuration := time.Since(start)

	if postErr != nil {
		ctxLog.ErrorWithType("Failed to post to Threads", postErr,
			"post_duration_ms", postDuration.Milliseconds(),
			"total_duration_ms", totalDuration.Milliseconds())
		return postErr
	}

	ctxLog.Info("Post to Threads completed successfully",
		"post_duration_ms", postDuration.Milliseconds(),
		"upload_duration_ms", uploadDuration.Milliseconds(),
		"total_duration_ms", totalDuration.Milliseconds())

	return nil
}

// PostTextOnly posts a text-only message to Threads without any media
func (p *Poster) PostTextOnly(ctx context.Context, text string) error {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "PostTextOnly")

	// Truncate text if it exceeds the character limit
	if len(text) > maxCharacterLimit {
		ctxLog.Warn("Truncating text due to character limit", "original", len(text), "limit", maxCharacterLimit)
		text = truncateText(text, maxCharacterLimit)
	}

	ctxLog.Info("Posting text-only message to Threads")

	// Use the threads-go client to create text post
	_, err := p.ThreadsClient.CreateTextPost(ctx, &threads.TextPostContent{
		Text:     text,
		TopicTag: TopicTag,
	})
	duration := time.Since(start)

	if err != nil {
		ctxLog.ErrorWithType("Failed to create text-only post", err,
			"duration_ms", duration.Milliseconds())
		return fmt.Errorf("failed to create text-only post: %v", err)
	}

	ctxLog.Info("Text-only message posted successfully",
		"duration_ms", duration.Milliseconds())
	return nil
}

// uploadImages uploads images to Picsur and returns their URLs
func (p *Poster) uploadImages(ctx context.Context, images []image.Image) ([]string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "uploadImages").
		WithContext("imageCount", len(images))

	var imageURLs []string

	for i, img := range images {
		// Add a small delay between uploads to prevent overwhelming the service
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		ctxLog.Debug("Uploading image", "index", i+1)
		imageURL, err := p.PicsurClient.UploadImage(ctx, img)
		if err != nil {
			ctxLog.Error("Failed to upload image", "index", i+1, "error", err)
			return nil, fmt.Errorf("failed to upload image %d: %v", i+1, err)
		}
		imageURLs = append(imageURLs, imageURL)
		ctxLog.Debug("Uploaded image", "index", i+1, "total", len(images))
	}

	// Small delay after all uploads to ensure they're processed
	time.Sleep(2 * time.Second)

	ctxLog.Info("All images uploaded successfully", "count", len(imageURLs))
	return imageURLs, nil
}

// postSingleImage posts a single image to Threads
func (p *Poster) postSingleImage(ctx context.Context, imageURL, postText string) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "postSingleImage")

	ctxLog.Debug("Creating single image post", "url", imageURL)

	// Use the threads-go client to create image post
	_, err := p.ThreadsClient.CreateImagePost(ctx, &threads.ImagePostContent{
		Text:     postText,
		ImageURL: imageURL,
		TopicTag: TopicTag,
	})
	if err != nil {
		ctxLog.Error("Failed to create image post", "error", err)
		return fmt.Errorf("failed to create image post: %v", err)
	}

	ctxLog.Debug("Successfully posted single image")
	return nil
}

// postCarousel posts multiple images as a carousel to Threads
func (p *Poster) postCarousel(ctx context.Context, imageURLs []string, postText string) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "postCarousel").
		WithContext("imageCount", len(imageURLs))

	var containerIDs []string

	// Create media containers for each image in the carousel
	for i, imageURL := range imageURLs {
		// Add a small delay between container creations
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		ctxLog.Debug("Creating media container for carousel image", "index", i+1)
		containerID, err := p.ThreadsClient.CreateMediaContainer(ctx, threads.MediaTypeImage, imageURL, "")
		if err != nil {
			ctxLog.Error("Failed to create media container", "index", i+1, "error", err)
			return fmt.Errorf("failed to create media container: %v", err)
		}
		containerIDs = append(containerIDs, string(containerID))
	}

	// Create carousel post
	ctxLog.Debug("Creating carousel post", "itemCount", len(containerIDs))
	_, err := p.ThreadsClient.CreateCarouselPost(ctx, &threads.CarouselPostContent{
		Text:     postText,
		Children: containerIDs,
		TopicTag: TopicTag,
	})
	if err != nil {
		ctxLog.Error("Failed to create carousel post", "error", err)
		return fmt.Errorf("failed to create carousel post: %v", err)
	}

	ctxLog.Debug("Successfully posted carousel")
	return nil
}

// formatPostText formats the text for a post
func (p *Poster) formatPostText(ctx context.Context, title string, publishTime time.Time, documentURL, aiSummary string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "formatPostText")

	// Shorten the document URL if provided
	var shortenedURL string
	var err error

	if documentURL != "" {
		ctxLog.Debug("Shortening document URL")
		shortenedURL, err = p.ShortenerClient.ShortenURL(ctx, documentURL)
		if err != nil {
			ctxLog.Error("Failed to shorten URL", "error", err)
			// Continue without the shortened URL
			ctxLog.Warn("Continuing without shortened URL")
		}
	}

	// Create the base text with or without the shortened URL
	var baseText string
	if shortenedURL != "" {
		baseText = fmt.Sprintf("New document: %s\nPublished on: %s\nLink: %s\n\nAI Summary: ",
			title, publishTime.Format("02-01-2006 15:04 MST"), shortenedURL)
	} else {
		baseText = fmt.Sprintf("New document: %s\nPublished on: %s\n\nAI Summary: ",
			title, publishTime.Format("02-01-2006 15:04 MST"))
	}

	remainingChars := maxCharacterLimit - len(baseText)

	// Truncate AI summary if needed
	truncatedSummary := truncateText(aiSummary, remainingChars)

	// Combine all parts
	return baseText + truncatedSummary, nil
}

// truncateText truncates text to the specified limit, adding an ellipsis
func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}

	// Reserve space for the ellipsis
	limit -= len(ellipsis)
	if limit <= 0 {
		return ""
	}

	// Find the last space before the limit to avoid cutting words in the middle
	lastSpace := strings.LastIndex(text[:limit], " ")
	if lastSpace == -1 {
		// If no space found, just cut at the limit
		return text[:limit] + ellipsis
	}

	return text[:lastSpace] + ellipsis
}
