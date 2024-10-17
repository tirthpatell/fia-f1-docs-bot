package poster

import (
	"encoding/json"
	"fmt"
	"image"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bot/pkg/utils"
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
func (p *Poster) Post(images []image.Image, title string, publishTime time.Time, documentURL string) error {
	// Upload images to Imgur
	var imageURLs []string
	for i, img := range images {
		imgTitle := fmt.Sprintf("%s - Page %d", title, i+1)
		imgDescription := fmt.Sprintf("Page %d of document: %s", i+1, title)
		url, err := p.ImgurClient.UploadImage(img, imgTitle, imgDescription)
		if err != nil {
			return fmt.Errorf("failed to upload image %d to Imgur: %v", i+1, err)
		}
		imageURLs = append(imageURLs, url)
	}

	// Format the text for the post
	postText := formatPostText(title, publishTime, documentURL)

	// Determine whether to post a single image or a carousel based on the number of images
	if len(imageURLs) == 1 {
		// Single image post
		log.Println("single image returned")
		return p.postSingleImage(imageURLs[0], postText)
	} else if len(imageURLs) >= 2 && len(imageURLs) <= 20 {
		// Carousel post
		log.Println("multiple images retutned")
		return p.postCarousel(imageURLs, postText)
	} else {
		return fmt.Errorf("invalid number of images: %d. Must be between 1 and 20", len(imageURLs))
	}
}

// postSingleImage posts a single image to Threads
func (p *Poster) postSingleImage(imageURL, postText string) error {
	// Create item container for the single image
	itemID, err := p.createItemContainer(imageURL, false)
	if err != nil {
		return fmt.Errorf("failed to create item container: %v", err)
	}

	// Create media container with the image and text
	mediaID, err := p.createMediaContainer(itemID, postText, "IMAGE")
	if err != nil {
		return fmt.Errorf("failed to create media container: %v", err)
	}

	// Wait for 30 seconds as recommended by the API documentation
	time.Sleep(30 * time.Second)

	// Publish the media
	err = p.publishMedia(mediaID)
	if err != nil {
		return fmt.Errorf("failed to publish media: %v", err)
	}

	return nil
}

// postCarousel posts a carousel of images to Threads
func (p *Poster) postCarousel(imageURLs []string, postText string) error {
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

	// Wait for 30 seconds as recommended by the API documentation
	time.Sleep(30 * time.Second)

	// Publish the carousel
	err = p.publishCarousel(carouselID)
	if err != nil {
		return fmt.Errorf("failed to publish carousel: %v", err)
	}

	return nil
}

// formatPostText formats the text for the post
func formatPostText(title string, publishTime time.Time, documentURL string) string {
	// Ensure the URL starts with "https://"
	if !strings.HasPrefix(documentURL, "https://") && !strings.HasPrefix(documentURL, "http://") {
		documentURL = "https://" + documentURL
	}

	// Escape the URL
	escapedURL := url.QueryEscape(documentURL)

	return fmt.Sprintf("New document: %s\nPublished on: %s\n\nLink to document: %s\n\n#F1Threads",
		title,
		publishTime.Format("02-01-2006 15:04 MST"),
		escapedURL)
}

// createItemContainer creates an item container for the image
func (p *Poster) createItemContainer(imageURL string, isCarouselItem bool) (string, error) {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)
	payload := fmt.Sprintf("media_type=IMAGE&image_url=%s&is_carousel_item=%t&access_token=%s", imageURL, isCarouselItem, p.AccessToken)

	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return "", err
	}

	return result.ID, nil
}

// createMediaContainer creates a media container for a single image post
func (p *Poster) createMediaContainer(itemID, text, mediaType string) (string, error) {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)
	payload := fmt.Sprintf("media_type=%s&text=%s&children=%s&access_token=%s",
		mediaType, text, itemID, p.AccessToken)

	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return "", err
	}

	return result.ID, nil
}

// createCarouselContainer creates a carousel container for the images
func (p *Poster) createCarouselContainer(itemIDs []string, text string) (string, error) {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", p.UserID)
	payload := fmt.Sprintf("media_type=CAROUSEL&children=%s&text=%s&access_token=%s",
		strings.Join(itemIDs, ","), text, p.AccessToken)

	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return "", err
	}

	return result.ID, nil
}

// publishMedia publishes a single image post to Threads
func (p *Poster) publishMedia(mediaID string) error {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish", p.UserID)
	payload := fmt.Sprintf("creation_id=%s&access_token=%s", mediaID, p.AccessToken)

	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to publish single media post: %s", resp.Status)
	}

	return nil
}

// publishCarousel publishes the carousel to Threads
func (p *Poster) publishCarousel(carouselID string) error {
	url := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish", p.UserID)
	payload := fmt.Sprintf("creation_id=%s&access_token=%s", carouselID, p.AccessToken)

	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to publish carousel media post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to publish carousel media post: %s", resp.Status)
	}

	return nil
}
