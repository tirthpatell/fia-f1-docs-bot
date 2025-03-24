package poster

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bot/pkg/logger"
	"bot/pkg/utils"
)

// Package logger
var log = logger.Package("poster")

const (
	maxCharacterLimit = 500
	ellipsis          = "..."
)

// Helper function for URL encoding
func encodeText(text string) string {
	return strings.ReplaceAll(url.QueryEscape(text), "+", "%20")
}

// Poster is a struct that holds the configuration for the poster
type Poster struct {
	AccessToken     string
	UserID          string
	PicsurClient    *utils.Client
	ShortenerClient *utils.ShortenerClient
}

// New creates a new Poster
func New(accessToken, userID, picsurAPI, picsurURL, shortenerAPIKey, shortenerURL string) *Poster {
	ctxLog := log.WithContext("method", "New")
	ctxLog.Info("Creating new poster client")

	return &Poster{
		AccessToken:     accessToken,
		UserID:          userID,
		PicsurClient:    utils.New(picsurAPI, picsurURL),
		ShortenerClient: utils.NewShortenerClient(shortenerAPIKey, shortenerURL),
	}
}

// Post posts the images to Threads
func (p *Poster) Post(ctx context.Context, images []image.Image, title string, publishTime time.Time, documentURL, aiSummary string) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "Post")

	// Limit to 20 images if there are more
	if len(images) > 20 {
		ctxLog.Warn("Limiting images due to Threads API limitations", "original", len(images), "limited", 20)
		images = images[:20]
	}

	// Upload images to Picsur
	ctxLog.Debug("Uploading images to Picsur", "count", len(images))
	imageURLs, err := p.uploadImages(ctx, images, title)
	if err != nil {
		ctxLog.Error("Failed to upload images", "error", err)
		return err
	}

	// Format the text for the post
	ctxLog.Debug("Formatting post text")
	postText, err := p.formatPostText(ctx, title, publishTime, documentURL, aiSummary)
	if err != nil {
		ctxLog.Error("Failed to format post text", "error", err)
		return err
	}

	ctxLog.Debug("Post character count", "chars", len(postText))

	// Determine whether to post a single image or a carousel based on the number of images
	if len(imageURLs) == 1 {
		// Single image post
		ctxLog.Info("Posting single image to Threads")
		return p.postSingleImage(ctx, imageURLs[0], postText)
	} else if len(imageURLs) >= 2 && len(imageURLs) <= 20 {
		// Carousel post
		ctxLog.Info("Posting carousel to Threads", "images", len(imageURLs))
		return p.postCarousel(ctx, imageURLs, postText)
	}

	ctxLog.Error("Invalid number of images", "count", len(imageURLs))
	return fmt.Errorf("invalid number of images: %d. Must be between 1 and 20", len(imageURLs))
}

// PostTextOnly posts a text-only message to Threads without any media
func (p *Poster) PostTextOnly(ctx context.Context, text string) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "PostTextOnly")

	// Truncate text if it exceeds the character limit
	if len(text) > maxCharacterLimit {
		ctxLog.Warn("Truncating text due to character limit", "original", len(text), "limit", maxCharacterLimit)
		text = truncateText(text, maxCharacterLimit)
	}

	ctxLog.Info("Posting text-only message to Threads")

	// Use the threads endpoint with text-only payload
	threadsURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)

	// URL encode the text for the payload
	encodedText := encodeText(text)

	// Create the payload for a text-only post
	payload := fmt.Sprintf("media_type=TEXT&text=%s&access_token=%s", encodedText, p.AccessToken)

	// Make the API request to create the text-only post
	mediaID, err := p.makePostRequest(ctx, threadsURL, payload)
	if err != nil {
		ctxLog.Error("Failed to create text-only post", "error", err)
		return fmt.Errorf("failed to create text-only post: %v", err)
	}

	ctxLog.Debug("Created text-only post", "mediaID", mediaID)

	// Publish the post
	if err := p.publishMedia(ctx, mediaID); err != nil {
		ctxLog.Error("Failed to publish text-only post", "error", err)
		return fmt.Errorf("failed to publish text-only post: %v", err)
	}

	ctxLog.Debug("Text-only message posted successfully")
	return nil
}

// uploadImages uploads images to Picsur and returns their URLs
func (p *Poster) uploadImages(ctx context.Context, images []image.Image, title string) ([]string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "uploadImages").
		WithContext("imageCount", len(images))

	var imageURLs []string

	for i, img := range images {
		imgTitle := fmt.Sprintf("%s - Page %d", title, i+1)
		imgDescription := fmt.Sprintf("Page %d of document: %s", i+1, title)

		// Add a small delay between uploads to prevent overwhelming the service
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		ctxLog.Debug("Uploading image", "index", i+1)
		imageURL, err := p.PicsurClient.UploadImage(ctx, img, imgTitle, imgDescription)
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

	ctxLog.Debug("Creating item container for single image", "url", imageURL)

	// Create item container for the single image
	itemID, err := p.createItemContainer(ctx, imageURL, false)
	if err != nil {
		ctxLog.Error("Failed to create item container", "error", err)
		return fmt.Errorf("failed to create item container: %v", err)
	}

	// Small delay before creating media container
	time.Sleep(1 * time.Second)

	// Create media container with the image and text
	ctxLog.Debug("Creating media container")
	mediaID, err := p.createMediaContainer(ctx, itemID, postText, "IMAGE", imageURL)
	if err != nil {
		ctxLog.Error("Failed to create media container", "error", err)
		return fmt.Errorf("failed to create media container: %v", err)
	}

	ctxLog.Info("Waiting before publishing...")
	time.Sleep(1 * time.Second)

	// Publish the media
	ctxLog.Debug("Publishing media", "mediaID", mediaID)
	return p.publishMedia(ctx, mediaID)
}

// postCarousel posts multiple images as a carousel to Threads
func (p *Poster) postCarousel(ctx context.Context, imageURLs []string, postText string) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "postCarousel").
		WithContext("imageCount", len(imageURLs))

	var itemIDs []string

	// Create item containers for each image in the carousel
	for i, url := range imageURLs {
		// Add a small delay between container creations
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		ctxLog.Debug("Creating item container for carousel image", "index", i+1)
		itemID, err := p.createItemContainer(ctx, url, true)
		if err != nil {
			ctxLog.Error("Failed to create item container", "index", i+1, "error", err)
			return fmt.Errorf("failed to create item container: %v", err)
		}
		itemIDs = append(itemIDs, itemID)
	}

	// Create carousel container
	ctxLog.Debug("Creating carousel container", "itemCount", len(itemIDs))
	carouselID, err := p.createCarouselContainer(ctx, itemIDs, postText)
	if err != nil {
		ctxLog.Error("Failed to create carousel container", "error", err)
		return fmt.Errorf("failed to create carousel container: %v", err)
	}

	ctxLog.Info("Waiting before publishing...")
	time.Sleep(3 * time.Second)

	// Publish the carousel
	ctxLog.Debug("Publishing carousel", "carouselID", carouselID)
	return p.publishCarousel(ctx, carouselID)
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

	// Calculate remaining characters for the AI summary
	suffix := "\n\n#F1Threads"
	remainingChars := maxCharacterLimit - len(baseText) - len(suffix)

	// Truncate AI summary if needed
	truncatedSummary := truncateText(aiSummary, remainingChars)

	// Combine all parts
	return baseText + truncatedSummary + suffix, nil
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

// createItemContainer creates an item container for the image
func (p *Poster) createItemContainer(ctx context.Context, imageURL string, isCarouselItem bool) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "createItemContainer")

	apiEndpoint := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)
	payload := fmt.Sprintf("media_type=IMAGE&image_url=%s&is_carousel_item=%t&access_token=%s",
		imageURL, isCarouselItem, p.AccessToken)

	ctxLog.Debug("Making container request", "isCarouselItem", isCarouselItem)
	return p.makePostRequest(ctx, apiEndpoint, payload)
}

// createMediaContainer creates a media container for a single image post
func (p *Poster) createMediaContainer(ctx context.Context, itemID, text, mediaType, imageURL string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "createMediaContainer")

	apiEndpoint := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)

	// URL encode the text
	encodedText := encodeText(text)

	payload := fmt.Sprintf("media_type=%s&text=%s&children=%s&image_url=%s&access_token=%s",
		mediaType, encodedText, itemID, imageURL, p.AccessToken)

	ctxLog.Debug("Creating media container", "mediaType", mediaType, "itemID", itemID)
	return p.makePostRequest(ctx, apiEndpoint, payload)
}

// createCarouselContainer creates a carousel container for the images
func (p *Poster) createCarouselContainer(ctx context.Context, itemIDs []string, text string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "createCarouselContainer")

	apiEndpoint := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)

	// URL encode the text
	encodedText := encodeText(text)

	payload := fmt.Sprintf("media_type=CAROUSEL&children=%s&text=%s&access_token=%s",
		strings.Join(itemIDs, ","), encodedText, p.AccessToken)

	ctxLog.Debug("Creating carousel container", "itemCount", len(itemIDs))
	return p.makePostRequest(ctx, apiEndpoint, payload)
}

// publishMedia publishes a single image post to Threads
func (p *Poster) publishMedia(ctx context.Context, mediaID string) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "publishMedia")

	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish", p.UserID)
	payload := fmt.Sprintf("creation_id=%s&access_token=%s", mediaID, p.AccessToken)

	ctxLog.Debug("Publishing media", "mediaID", mediaID)
	_, err := p.makePostRequest(ctx, url, payload)
	return err
}

// publishCarousel publishes the carousel to Threads
func (p *Poster) publishCarousel(ctx context.Context, carouselID string) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "publishCarousel")

	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish", p.UserID)
	payload := fmt.Sprintf("creation_id=%s&access_token=%s", carouselID, p.AccessToken)

	ctxLog.Debug("Publishing carousel", "carouselID", carouselID)
	_, err := p.makePostRequest(ctx, url, payload)
	return err
}

// makePostRequest is a helper function to make POST requests to the Threads API
func (p *Poster) makePostRequest(ctx context.Context, url, payload string) (string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "makePostRequest")

	ctxLog.Debug("Making POST request", "url", url)
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		ctxLog.Error("HTTP request failed", "error", err)
		return "", fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctxLog.Error("Failed to read response body", "error", err)
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		ctxLog.Error("Request failed", "status", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		ctxLog.Error("Failed to parse response JSON", "error", err, "body", string(body))
		return "", fmt.Errorf("failed to parse response JSON: %v - Body: %s", err, string(body))
	}

	if result.ID == "" {
		ctxLog.Error("Received empty ID in response", "body", string(body))
		return "", fmt.Errorf("received empty ID in response: %s", string(body))
	}

	ctxLog.Debug("Successfully received response", "id", result.ID)
	return result.ID, nil
}
