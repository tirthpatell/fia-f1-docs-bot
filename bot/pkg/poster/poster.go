package poster

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
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
	AccessToken string
	UserID      string
	ImgurClient *utils.Client
}

// New creates a new Poster
func New(accessToken, userID, imgurClientID string) *Poster {
	return &Poster{
		AccessToken: accessToken,
		UserID:      userID,
		ImgurClient: utils.New(imgurClientID),
	}
}

// Post posts the images to Threads
func (p *Poster) Post(images []image.Image, title string, publishTime time.Time, documentURL, aiSummary string) error {
	// Upload images to Imgur
	imageURLs, err := p.uploadImages(images, title)
	if err != nil {
		return err
	}

	// Format the text for the post
	postText := formatPostText(title, publishTime, documentURL, aiSummary)

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

// uploadImages uploads the given images to Imgur and returns their URLs
func (p *Poster) uploadImages(images []image.Image, title string) ([]string, error) {
	var imageURLs []string
	for i, img := range images {
		imgTitle := fmt.Sprintf("%s - Page %d", title, i+1)
		imgDescription := fmt.Sprintf("Page %d of document: %s", i+1, title)
		url, err := p.ImgurClient.UploadImage(img, imgTitle, imgDescription)
		if err != nil {
			return nil, fmt.Errorf("failed to upload image %d to Imgur: %v", i+1, err)
		}
		imageURLs = append(imageURLs, url)
	}
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
	for _, url := range imageURLs {
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
func formatPostText(title string, publishTime time.Time, documentURL, aiSummary string) string {
	_ = documentURL

	// Create the base text without the AI summary
	baseText := fmt.Sprintf("New document: %s\nPublished on: %s\n\nAI Summary: ",
		title, publishTime.Format("02-01-2006 15:04 MST"))

	// Calculate remaining characters for the AI summary
	suffix := "\n\n#F1Threads"
	remainingChars := maxCharacterLimit - len(baseText) - len(suffix)

	// Truncate AI summary if needed
	truncatedSummary := truncateText(aiSummary, remainingChars)

	// Combine all parts
	return baseText + truncatedSummary + suffix
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
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	return result.ID, nil
}
