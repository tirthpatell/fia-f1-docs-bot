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
	"golang.org/x/sync/errgroup"
)

// Package logger
var log = logger.Package("poster")

const (
	maxCharacterLimit    = 500
	maxImagesPerPost     = 20
	maxConcurrentUploads = 5
	ellipsis             = "..."
	TopicTag             = "F1Threads"
)

// Poster is a struct that holds the configuration for the poster
type Poster struct {
	ThreadsClient   *threads.Client
	PicsurClient    *utils.Client
	ShortenerClient *utils.ShortenerClient
	AccessToken     string
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
		AccessToken:     accessToken,
	}, nil
}

// Post posts the images to Threads. When more than maxImagesPerPost images are
// provided, the post is split into a chain: the first chunk becomes the root
// post (with the AI summary text); each subsequent chunk is posted as an
// image-only reply to the previous post in the chain.
//
// Failure policy:
//   - Root post failure: returns the error; caller skips marking the document
//     as processed and will retry on the next scrape cycle.
//   - Reply chunk failure: logs the failure with the root post ID and chunk
//     index, then returns nil. The root post and any earlier replies remain
//     published; the document is marked processed so we don't re-publish the
//     root on the next cycle. Some tail images may be lost.
func (p *Poster) Post(ctx context.Context, images []image.Image, title string, publishTime time.Time, documentURL, aiSummary string) error {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "Post")

	if len(images) == 0 {
		ctxLog.Warn("Post called with zero images; nothing to do")
		return nil
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

	// Format the text for the root post
	ctxLog.Debug("Formatting post text")
	postText, err := p.formatPostText(ctx, title, publishTime, documentURL, aiSummary)
	if err != nil {
		ctxLog.ErrorWithType("Failed to format post text", err)
		return err
	}
	ctxLog.Debug("Post character count", "chars", len(postText))

	// Partition images into chunks of ≤ maxImagesPerPost
	chunks := chunkURLs(imageURLs, maxImagesPerPost)
	ctxLog.Info("Posting to Threads",
		"image_count", len(imageURLs),
		"chunk_count", len(chunks))

	// Post the root chunk
	postStart := time.Now()
	rootPost, err := p.postChunk(ctx, chunks[0], postText, "")
	if err != nil {
		ctxLog.ErrorWithType("Failed to post root chunk to Threads", err,
			"chunk_size", len(chunks[0]),
			"upload_duration_ms", uploadDuration.Milliseconds(),
			"total_duration_ms", time.Since(start).Milliseconds())
		return err
	}
	ctxLog.Info("Root post published", "post_id", rootPost.ID, "images", len(chunks[0]))

	// Chain replies for the remaining chunks. chunk_index in logs is 1-based;
	// the root post is chunk 1, the first reply is chunk 2, etc.
	prevID := rootPost.ID
	for i := 1; i < len(chunks); i++ {
		replyPost, replyErr := p.postChunk(ctx, chunks[i], "", prevID)
		if replyErr != nil {
			// Loss-tolerant: log loudly, stop the chain, but do not fail the
			// whole Post() call. Caller will mark the document as processed so
			// we don't re-publish the root on the next cycle.
			ctxLog.ErrorWithType("Failed to post reply chunk; remaining images dropped", replyErr,
				"root_post_id", rootPost.ID,
				"chunk_index", i+1,
				"total_chunks", len(chunks),
				"chunk_size", len(chunks[i]),
				"dropped_chunks", len(chunks)-i)
			break
		}
		ctxLog.Info("Reply chunk published",
			"post_id", replyPost.ID,
			"reply_to", prevID,
			"chunk_index", i+1,
			"total_chunks", len(chunks),
			"images", len(chunks[i]))
		prevID = replyPost.ID
	}

	totalDuration := time.Since(start)
	ctxLog.Info("Post to Threads completed",
		"chunks_total", len(chunks),
		"posting_duration_ms", time.Since(postStart).Milliseconds(),
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

// uploadImages uploads images to Picsur in parallel (bounded by
// maxConcurrentUploads) and returns their URLs in the original order. The
// first upload error cancels the remaining uploads via the errgroup context.
func (p *Poster) uploadImages(ctx context.Context, images []image.Image) ([]string, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "uploadImages").
		WithContext("imageCount", len(images))

	imageURLs := make([]string, len(images))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentUploads)

	for i, img := range images {
		g.Go(func() error {
			ctxLog.Debug("Uploading image", "index", i+1)
			url, err := p.PicsurClient.UploadImage(gctx, img)
			if err != nil {
				ctxLog.Error("Failed to upload image", "index", i+1, "error", err)
				return fmt.Errorf("failed to upload image %d: %v", i+1, err)
			}
			imageURLs[i] = url
			ctxLog.Debug("Uploaded image", "index", i+1, "total", len(images))
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	ctxLog.Info("All images uploaded successfully", "count", len(imageURLs))
	return imageURLs, nil
}

// postSingleImage posts a single image to Threads. If replyToID is non-empty,
// the post is created as a reply to that post.
func (p *Poster) postSingleImage(ctx context.Context, imageURL, postText, replyToID string) (*threads.Post, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "postSingleImage")

	ctxLog.Debug("Creating single image post", "url", imageURL, "reply_to", replyToID)

	post, err := p.ThreadsClient.CreateImagePost(ctx, &threads.ImagePostContent{
		Text:     postText,
		ImageURL: imageURL,
		ReplyTo:  replyToID,
		TopicTag: TopicTag,
	})
	if err != nil {
		ctxLog.Error("Failed to create image post", "error", err)
		return nil, fmt.Errorf("failed to create image post: %v", err)
	}

	ctxLog.Debug("Successfully posted single image", "post_id", post.ID)
	return post, nil
}

// postCarousel posts multiple images as a carousel to Threads. If replyToID
// is non-empty, the carousel is posted as a reply to that post.
func (p *Poster) postCarousel(ctx context.Context, imageURLs []string, postText, replyToID string) (*threads.Post, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "postCarousel").
		WithContext("imageCount", len(imageURLs))

	var containerIDs []string

	for i, imageURL := range imageURLs {
		ctxLog.Debug("Creating media container for carousel image", "index", i+1)
		containerID, err := p.ThreadsClient.CreateMediaContainer(ctx, threads.MediaTypeImage, imageURL, "")
		if err != nil {
			ctxLog.Error("Failed to create media container", "index", i+1, "error", err)
			return nil, fmt.Errorf("failed to create media container: %v", err)
		}
		containerIDs = append(containerIDs, string(containerID))
	}

	ctxLog.Debug("Creating carousel post", "itemCount", len(containerIDs), "reply_to", replyToID)
	post, err := p.ThreadsClient.CreateCarouselPost(ctx, &threads.CarouselPostContent{
		Text:     postText,
		Children: containerIDs,
		ReplyTo:  replyToID,
		TopicTag: TopicTag,
	})
	if err != nil {
		ctxLog.Error("Failed to create carousel post", "error", err)
		return nil, fmt.Errorf("failed to create carousel post: %v", err)
	}

	ctxLog.Debug("Successfully posted carousel", "post_id", post.ID)
	return post, nil
}

// postChunk posts a single chunk of 1..maxImagesPerPost image URLs and returns
// the resulting post. text is attached to the post (use "" for image-only
// replies). replyToID, when non-empty, makes this a reply to that post.
func (p *Poster) postChunk(ctx context.Context, imageURLs []string, text, replyToID string) (*threads.Post, error) {
	switch n := len(imageURLs); {
	case n == 1:
		return p.postSingleImage(ctx, imageURLs[0], text, replyToID)
	case n >= 2 && n <= maxImagesPerPost:
		return p.postCarousel(ctx, imageURLs, text, replyToID)
	default:
		// Unreachable from Post (chunkURLs guarantees 1..maxImagesPerPost);
		// retained as defense for any future direct caller.
		return nil, fmt.Errorf("invalid chunk size: %d (must be 1..%d)", n, maxImagesPerPost)
	}
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

// chunkURLs partitions urls into consecutive slices of length ≤ size.
// Returns nil for an empty input. The last chunk may be shorter than size.
func chunkURLs(urls []string, size int) [][]string {
	if len(urls) == 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(urls)+size-1)/size)
	for i := 0; i < len(urls); i += size {
		end := i + size
		if end > len(urls) {
			end = len(urls)
		}
		chunks = append(chunks, urls[i:end])
	}
	return chunks
}
