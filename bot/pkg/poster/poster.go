package poster

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
}

// New creates a new Poster
func New(accessToken, userID, clientID, clientSecret, redirectURI, picsurAPI, picsurURL, picsurUploadURL, shortenerAPIKey, shortenerURL string) (*Poster, error) {
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
		PicsurClient:    utils.New(picsurAPI, picsurURL, picsurUploadURL),
		ShortenerClient: utils.NewShortenerClient(shortenerAPIKey, shortenerURL),
	}, nil
}

// Media is a document's uploaded page images, with Threads carousel item
// containers already created for images that go into a carousel.
type Media struct {
	urls       []string
	containers []string // carousel item container per image; "" for single-image chunks
}

// PrepareMedia uploads numImages images and creates their carousel item
// containers. render must call emit once per index (0..numImages-1), from any
// goroutine in any order; each image is staged as soon as it is emitted.
// Creating containers early lets Threads process them while the summary is
// still generating, so publishing rarely has to wait on them. The first
// failure cancels the context passed to render.
func (p *Poster) PrepareMedia(ctx context.Context, numImages int, render func(ctx context.Context, emit func(i int, png []byte) error) error) (*Media, error) {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "PrepareMedia").
		WithContext("imageCount", numImages)

	if numImages <= 0 {
		return &Media{}, nil // Post treats empty media as nothing to do
	}

	m := &Media{
		urls:       make([]string, numImages),
		containers: make([]string, numImages),
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentUploads)

	renderErr := render(gctx, func(i int, png []byte) error {
		if i < 0 || i >= numImages {
			return fmt.Errorf("image index %d out of range [0, %d)", i, numImages)
		}
		// Blocks at the limit, throttling rendering to the upload rate
		g.Go(func() error { return p.stageImage(gctx, m, i, png) })
		return nil
	})
	stageErr := g.Wait()

	// A staging failure cancels rendering, so it is the root cause
	err := stageErr
	if err == nil {
		err = renderErr
	}
	if err == nil {
		for i, url := range m.urls {
			if url == "" {
				err = fmt.Errorf("image %d was never rendered", i+1)
				break
			}
		}
	}
	if err != nil {
		ctxLog.ErrorWithType("Failed to prepare media", err,
			"duration_ms", time.Since(start).Milliseconds())
		return nil, err
	}

	ctxLog.Info("Media prepared",
		"count", numImages,
		"duration_ms", time.Since(start).Milliseconds())
	return m, nil
}

// stageImage uploads image i and, if it goes into a carousel, creates its
// carousel item container.
func (p *Poster) stageImage(ctx context.Context, m *Media, i int, png []byte) error {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "stageImage").
		WithContext("index", i+1)

	url, err := p.PicsurClient.UploadImage(ctx, png)
	if err != nil {
		ctxLog.Error("Failed to upload image", "error", err)
		return fmt.Errorf("failed to upload image %d: %v", i+1, err)
	}
	m.urls[i] = url
	ctxLog.Debug("Uploaded image", "total", len(m.urls))

	if !inCarousel(i, len(m.urls), maxImagesPerPost) {
		return nil
	}
	containerID, err := p.ThreadsClient.CreateMediaContainer(ctx, threads.MediaTypeImage, url, "")
	if err != nil {
		ctxLog.Error("Failed to create media container", "error", err)
		return fmt.Errorf("failed to create media container for image %d: %v", i+1, err)
	}
	m.containers[i] = string(containerID)
	return nil
}

// ShortenURL returns the shortened url, or "" if url is empty or shortening
// fails.
func (p *Poster) ShortenURL(ctx context.Context, url string) string {
	if url == "" {
		return ""
	}
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "ShortenURL")

	ctxLog.Debug("Shortening document URL")
	shortenedURL, err := p.ShortenerClient.ShortenURL(ctx, url)
	if err != nil {
		ctxLog.Error("Failed to shorten URL", "error", err)
		ctxLog.Warn("Continuing without shortened URL")
		return ""
	}
	return shortenedURL
}

// Post publishes prepared media to Threads. When there are more than
// maxImagesPerPost images, the post is split into a chain: the first chunk
// becomes the root post (with the AI summary text); each subsequent chunk is
// posted as an image-only reply to the previous post in the chain. An empty
// shortURL omits the link.
//
// Failure policy:
//   - Root post failure: returns the error; caller skips marking the document
//     as processed and will retry on the next scrape cycle.
//   - Reply chunk failure: logs the failure with the root post ID and chunk
//     index, then returns nil. The root post and any earlier replies remain
//     published; the document is marked processed so we don't re-publish the
//     root on the next cycle. Some tail images may be lost.
func (p *Poster) Post(ctx context.Context, media *Media, title string, publishTime time.Time, shortURL, aiSummary string) error {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "Post")

	if media == nil || len(media.urls) == 0 {
		ctxLog.Warn("Post called with zero images; nothing to do")
		return nil
	}

	// Format the text for the root post
	postText := formatPostText(title, publishTime, shortURL, aiSummary)
	ctxLog.Debug("Post character count", "chars", utf8.RuneCountInString(postText))

	// Partition images into chunks of ≤ maxImagesPerPost
	chunks := chunkURLs(media.urls, maxImagesPerPost)
	containerChunks := chunkURLs(media.containers, maxImagesPerPost)
	ctxLog.Info("Posting to Threads",
		"image_count", len(media.urls),
		"chunk_count", len(chunks))

	// Post the root chunk
	rootPost, err := p.postChunk(ctx, chunks[0], containerChunks[0], postText, "")
	if err != nil {
		ctxLog.ErrorWithType("Failed to post root chunk to Threads", err,
			"chunk_size", len(chunks[0]),
			"total_duration_ms", time.Since(start).Milliseconds())
		return err
	}
	ctxLog.Info("Root post published", "post_id", rootPost.ID, "images", len(chunks[0]))

	// Chain replies for the remaining chunks. chunk_index in logs is 1-based;
	// the root post is chunk 1, the first reply is chunk 2, etc.
	prevID := rootPost.ID
	for i := 1; i < len(chunks); i++ {
		replyPost, replyErr := p.postChunk(ctx, chunks[i], containerChunks[i], "", prevID)
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

	ctxLog.Info("Post to Threads completed",
		"chunks_total", len(chunks),
		"posting_duration_ms", time.Since(start).Milliseconds())

	return nil
}

// PostTextOnly posts a text-only message to Threads without any media
func (p *Poster) PostTextOnly(ctx context.Context, text string) error {
	start := time.Now()
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "PostTextOnly")

	// Truncate text if it exceeds the character limit
	if utf8.RuneCountInString(text) > maxCharacterLimit {
		ctxLog.Warn("Truncating text due to character limit", "original", utf8.RuneCountInString(text), "limit", maxCharacterLimit)
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
		TopicTag: topicTagForReply(replyToID),
	})
	if err != nil {
		ctxLog.Error("Failed to create image post", "error", err)
		return nil, fmt.Errorf("failed to create image post: %v", err)
	}

	ctxLog.Debug("Successfully posted single image", "post_id", post.ID)
	return post, nil
}

// postCarousel posts a carousel of carousel item containers to Threads. If
// replyToID is non-empty, the carousel is posted as a reply to that post.
func (p *Poster) postCarousel(ctx context.Context, containerIDs []string, postText, replyToID string) (*threads.Post, error) {
	ctxLog := log.WithRequestContext(ctx).
		WithContext("method", "postCarousel").
		WithContext("imageCount", len(containerIDs))

	ctxLog.Debug("Creating carousel post", "itemCount", len(containerIDs), "reply_to", replyToID)
	post, err := p.ThreadsClient.CreateCarouselPost(ctx, &threads.CarouselPostContent{
		Text:     postText,
		Children: containerIDs,
		ReplyTo:  replyToID,
		TopicTag: topicTagForReply(replyToID),
	})
	if err != nil {
		ctxLog.Error("Failed to create carousel post", "error", err)
		return nil, fmt.Errorf("failed to create carousel post: %v", err)
	}

	ctxLog.Debug("Successfully posted carousel", "post_id", post.ID)
	return post, nil
}

// postChunk posts a single chunk of 1..maxImagesPerPost images (URLs plus
// matching carousel item containers) and returns the resulting post. text is
// attached to the post (use "" for image-only replies). replyToID, when
// non-empty, makes this a reply to that post.
func (p *Poster) postChunk(ctx context.Context, imageURLs, containerIDs []string, text, replyToID string) (*threads.Post, error) {
	switch n := len(imageURLs); {
	case n == 1:
		return p.postSingleImage(ctx, imageURLs[0], text, replyToID)
	case n >= 2 && n <= maxImagesPerPost:
		return p.postCarousel(ctx, containerIDs, text, replyToID)
	default:
		// Unreachable from Post (chunkURLs guarantees 1..maxImagesPerPost);
		// retained as defense for any future direct caller.
		return nil, fmt.Errorf("invalid chunk size: %d (must be 1..%d)", n, maxImagesPerPost)
	}
}

// formatPostText formats the text for a post. An empty shortenedURL omits
// the link.
func formatPostText(title string, publishTime time.Time, shortenedURL, aiSummary string) string {
	// Create the base text with or without the shortened URL
	var baseText string
	if shortenedURL != "" {
		baseText = fmt.Sprintf("New document: %s\nPublished on: %s\nLink: %s",
			title, publishTime.Format("02-01-2006 15:04 MST"), shortenedURL)
	} else {
		baseText = fmt.Sprintf("New document: %s\nPublished on: %s",
			title, publishTime.Format("02-01-2006 15:04 MST"))
	}

	// Only attach the summary section when there is a summary (a failed
	// generation should not leave a dangling "AI Summary:" label) and there
	// is room for it — the final truncation below would otherwise cut the
	// text mid-label.
	const summaryLabel = "\n\nAI Summary: "
	remainingChars := maxCharacterLimit - utf8.RuneCountInString(baseText) - utf8.RuneCountInString(summaryLabel)

	text := baseText
	if aiSummary != "" && remainingChars > 0 {
		text += summaryLabel + truncateText(aiSummary, remainingChars)
	}

	// Final guard: an unusually long title can push baseText itself past the
	// limit, so truncate the assembled text as a whole.
	return truncateText(text, maxCharacterLimit)
}

// truncateText truncates text to the specified limit (counted in runes, since
// the Threads limit is characters, not bytes), adding an ellipsis.
func truncateText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	// Reserve space for the ellipsis
	limit -= len(ellipsis)
	if limit <= 0 {
		return ""
	}

	// Find the last space before the limit to avoid cutting words in the middle
	truncated := string(runes[:limit])
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace == -1 {
		// If no space found, just cut at the limit
		return truncated + ellipsis
	}

	return truncated[:lastSpace] + ellipsis
}

// topicTagForReply returns the topic tag to apply to a post: TopicTag for the
// root post (empty replyToID), and "" for replies — the Threads API only
// allows topic tags on root posts, not on replies.
func topicTagForReply(replyToID string) string {
	if replyToID == "" {
		return TopicTag
	}
	return ""
}

// inCarousel reports whether image i of n lands in a chunk of two or more
// images when split by chunkURLs.
func inCarousel(i, n, size int) bool {
	chunkStart := i / size * size
	return min(chunkStart+size, n)-chunkStart >= 2
}

// chunkURLs partitions urls into consecutive slices of length ≤ size.
// Returns nil for an empty input or non-positive size. The last chunk may be
// shorter than size.
func chunkURLs(urls []string, size int) [][]string {
	if len(urls) == 0 || size <= 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(urls)+size-1)/size)
	for i := 0; i < len(urls); i += size {
		end := min(i+size, len(urls))
		chunks = append(chunks, urls[i:end])
	}
	return chunks
}
