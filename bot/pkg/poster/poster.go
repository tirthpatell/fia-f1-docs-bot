package poster

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bot/pkg/utils"
)

const (
	maxCharacterLimit = 500
	ellipsis          = "..."
)

// Poster is a struct that holds the configuration for the poster
type Poster struct {
	AccessToken     string
	UserID          string
	PicsurClient    *utils.Client
	ShortenerClient *utils.ShortenerClient
}

// New creates a new Poster
func New(accessToken, userID, picsurAPI, picsurURL, shortenerAPIKey, shortenerURL string) *Poster {
	return &Poster{
		AccessToken:     accessToken,
		UserID:          userID,
		PicsurClient:    utils.New(picsurAPI, picsurURL),
		ShortenerClient: utils.NewShortenerClient(shortenerAPIKey, shortenerURL),
	}
}

// Post posts the images to Threads
func (p *Poster) Post(images []image.Image, title string, publishTime time.Time, documentURL, aiSummary string) error {
	// Limit to 20 images if there are more
	if len(images) > 20 {
		log.Printf("Limiting from %d to 20 images as per Threads API limitations", len(images))
		images = images[:20]
	}

	// Upload images to Picsur
	imageURLs, err := p.uploadImages(images, title)
	if err != nil {
		return err
	}

	// Format the text for the post
	postText, err := p.formatPostText(title, publishTime, documentURL, aiSummary)
	if err != nil {
		return err
	}

	log.Printf("char count: %v", len(postText))

	// Determine whether to post a single image or a carousel based on the number of images
	if len(imageURLs) == 1 {
		// Single image post
		return p.postSingleImage(imageURLs[0], postText)
	} else if len(imageURLs) >= 2 && len(imageURLs) <= 20 {
		// Carousel post
		return p.postCarousel(imageURLs, postText)
	}

	return fmt.Errorf("invalid number of images: %d. Must be between 1 and 20", len(imageURLs))
}

// PostTextOnly posts a text-only message to Threads without any media
func (p *Poster) PostTextOnly(text string) error {
	// Truncate text if it exceeds the character limit
	if len(text) > maxCharacterLimit {
		text = truncateText(text, maxCharacterLimit)
	}

	// Use the threads endpoint with text-only payload
	threadsURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)

	// URL encode the text for the payload
	encodedText := strings.ReplaceAll(url.QueryEscape(text), "+", "%20")

	// Create the payload for a text-only post
	payload := fmt.Sprintf("text=%s&access_token=%s", encodedText, p.AccessToken)

	// Make the API request to create the text-only post
	mediaID, err := p.makePostRequest(threadsURL, payload)
	if err != nil {
		return fmt.Errorf("failed to create text-only post: %v", err)
	}

	log.Printf("Created text-only post with ID: %s", mediaID)

	// Publish the post
	if err := p.publishMedia(mediaID); err != nil {
		return fmt.Errorf("failed to publish text-only post: %v", err)
	}

	log.Printf("Successfully posted text-only message")
	return nil
}

// formatJSONString properly escapes a string for JSON
func formatJSONString(s string) string {
	bytes, err := json.Marshal(s)
	if err != nil {
		// If marshaling fails, do a basic escaping
		s = strings.ReplaceAll(s, "\"", "\\\"")
		s = strings.ReplaceAll(s, "\n", "\\n")
		return fmt.Sprintf("\"%s\"", s)
	}
	return string(bytes)
}

// uploadImages uploads the given images to Picsur and returns their URLs
func (p *Poster) uploadImages(images []image.Image, title string) ([]string, error) {
	var imageURLs []string
	for i, img := range images {
		imgTitle := fmt.Sprintf("%s - Page %d", title, i+1)
		imgDescription := fmt.Sprintf("Page %d of document: %s", i+1, title)

		// Add a small delay between uploads to prevent overwhelming the service
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		url, err := p.PicsurClient.UploadImage(img, imgTitle, imgDescription)
		if err != nil {
			return nil, fmt.Errorf("failed to upload image %d to Picsur: %v", i+1, err)
		}
		imageURLs = append(imageURLs, url)
		log.Printf("Uploaded image %d/%d", i+1, len(images))
	}

	// Small delay after all uploads to ensure they're processed
	time.Sleep(2 * time.Second)

	return imageURLs, nil
}

// postSingleImage posts a single image to Threads
func (p *Poster) postSingleImage(imageURL, postText string) error {
	log.Printf("Posting single image: %s", imageURL)

	// Create item container for the single image
	itemID, err := p.createItemContainer(imageURL, false)
	if err != nil {
		return fmt.Errorf("failed to create item container: %v", err)
	}

	// Small delay before creating media container
	time.Sleep(1 * time.Second)

	// Create media container with the image and text
	mediaID, err := p.createMediaContainer(itemID, postText, "IMAGE", imageURL)
	if err != nil {
		return fmt.Errorf("failed to create media container: %v", err)
	}

	log.Println("Waiting 5 seconds before publishing...")
	time.Sleep(5 * time.Second)

	// Publish the media
	return p.publishMedia(mediaID)
}

// postCarousel posts a carousel of images to Threads
func (p *Poster) postCarousel(imageURLs []string, postText string) error {
	log.Printf("Posting carousel with %d images", len(imageURLs))

	// Create item containers for each image
	var itemIDs []string
	for i, url := range imageURLs {
		// Add a small delay between container creations
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		itemID, err := p.createItemContainer(url, true)
		if err != nil {
			return fmt.Errorf("failed to create item container: %v", err)
		}
		itemIDs = append(itemIDs, itemID)
	}

	// Create carousel container
	carouselID, err := p.createCarouselContainer(itemIDs, postText)
	if err != nil {
		return fmt.Errorf("failed to create carousel container: %v", err)
	}

	log.Println("Waiting 5 seconds before publishing...")
	time.Sleep(5 * time.Second)

	// Publish the carousel
	return p.publishCarousel(carouselID)
}

// formatPostText formats the text for the post
func (p *Poster) formatPostText(title string, publishTime time.Time, documentURL, aiSummary string) (string, error) {
	// Shorten the document URL if provided
	shortenedURL := ""
	if documentURL != "" {
		var err error
		shortenedURL, err = p.ShortenerClient.ShortenURL(documentURL)
		if err != nil {
			log.Printf("Warning: Failed to shorten URL: %v", err)
			// Continue without the shortened URL
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

// truncateText truncates the text to fit within the given character limit
// and adds ellipsis if truncation occurred
func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}

	// Reserve space for ellipsis
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
func (p *Poster) createItemContainer(imageURL string, isCarouselItem bool) (string, error) {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)
	payload := fmt.Sprintf("media_type=IMAGE&image_url=%s&is_carousel_item=%t&access_token=%s", imageURL, isCarouselItem, p.AccessToken)

	return p.makePostRequest(url, payload)
}

// createMediaContainer creates a media container for a single image post
func (p *Poster) createMediaContainer(itemID, text, mediaType, imageURL string) (string, error) {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)
	payload := fmt.Sprintf("media_type=%s&text=%s&children=%s&image_url=%s&access_token=%s",
		mediaType, text, itemID, imageURL, p.AccessToken)

	return p.makePostRequest(url, payload)
}

// createCarouselContainer creates a carousel container for the images
func (p *Poster) createCarouselContainer(itemIDs []string, text string) (string, error) {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)
	payload := fmt.Sprintf("media_type=CAROUSEL&children=%s&text=%s&access_token=%s",
		strings.Join(itemIDs, ","), text, p.AccessToken)

	return p.makePostRequest(url, payload)
}

// publishMedia publishes a single image post to Threads
func (p *Poster) publishMedia(mediaID string) error {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish", p.UserID)
	payload := fmt.Sprintf("creation_id=%s&access_token=%s", mediaID, p.AccessToken)

	_, err := p.makePostRequest(url, payload)
	return err
}

// publishCarousel publishes the carousel to Threads
func (p *Poster) publishCarousel(carouselID string) error {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish", p.UserID)
	payload := fmt.Sprintf("creation_id=%s&access_token=%s", carouselID, p.AccessToken)

	_, err := p.makePostRequest(url, payload)
	return err
}

// makePostRequest is a helper function to make POST requests to the Threads API
func (p *Poster) makePostRequest(url, payload string) (string, error) {
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %v - Body: %s", err, string(body))
	}

	if result.ID == "" {
		return "", fmt.Errorf("received empty ID in response: %s", string(body))
	}

	return result.ID, nil
}
