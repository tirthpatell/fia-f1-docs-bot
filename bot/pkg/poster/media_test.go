package poster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bot/pkg/utils"

	"github.com/tirthpatell/threads-go"
)

// fakeThreads is a minimal Threads Graph API: every container is immediately
// FINISHED, and it records carousel item containers and published posts.
type fakeThreads struct {
	mu          sync.Mutex
	nextID      atomic.Int64
	itemImages  map[string]string // carousel item container ID -> image URL
	carousels   [][]string        // children of each carousel container, in creation order
	publishes   int
	replyTo     []string        // reply_to_id of each created parent container
	singleImage []string        // image_url of each single-image (non-carousel-item) container
	failOnce    map[string]bool // carousel item image URLs whose first create fails
}

func (f *fakeThreads) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := func(prefix string) string { return fmt.Sprintf("%s%d", prefix, f.nextID.Add(1)) }
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/threads_publish"):
		f.publishes++
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id("post")})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/threads"):
		cid := id("c")
		switch {
		case r.Form.Get("is_carousel_item") == "true":
			if img := r.Form.Get("image_url"); f.failOnce[img] {
				delete(f.failOnce, img)
				http.Error(w, `{"error":{"message":"boom","code":1}}`, http.StatusBadRequest)
				return
			}
			f.itemImages[cid] = r.Form.Get("image_url")
		case r.Form.Get("media_type") == "CAROUSEL":
			f.carousels = append(f.carousels, strings.Split(r.Form.Get("children"), ","))
			f.replyTo = append(f.replyTo, r.Form.Get("reply_to_id"))
		default:
			f.singleImage = append(f.singleImage, r.Form.Get("image_url"))
			f.replyTo = append(f.replyTo, r.Form.Get("reply_to_id"))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": cid})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/c"):
		_ = json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(r.URL.Path, "/"), "status": "FINISHED"})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/post"):
		_ = json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(r.URL.Path, "/")})
	default:
		http.Error(w, `{"error":{"message":"unexpected `+r.Method+" "+r.URL.Path+`"}}`, http.StatusNotFound)
	}
}

// fakePicsur uses the uploaded bytes as the image ID.
func fakePicsur(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("image")
		if err != nil {
			t.Errorf("picsur: %v", err)
			return
		}
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"id": string(buf[:n])}})
	}))
}

func newTestPoster(t *testing.T) (*Poster, *fakeThreads) {
	ft := &fakeThreads{itemImages: map[string]string{}, failOnce: map[string]bool{}}
	threadsSrv := httptest.NewServer(ft)
	t.Cleanup(threadsSrv.Close)
	picsurSrv := fakePicsur(t)
	t.Cleanup(picsurSrv.Close)

	client, err := threads.NewClient(&threads.Config{
		ClientID: "id", ClientSecret: "secret", RedirectURI: "https://example.com/cb",
		BaseURL: threadsSrv.URL, HTTPTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetTokenInfo(&threads.TokenInfo{
		AccessToken: "tok", TokenType: "Bearer", UserID: "user",
		ExpiresAt: time.Now().Add(24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return &Poster{ThreadsClient: client, PicsurClient: utils.New("key", "https://picsur.example.com", picsurSrv.URL)}, ft
}

// emitAll emits images concurrently and in reverse order.
func emitAll(n int) func(context.Context, func(int, []byte) error) error {
	return func(_ context.Context, emit func(int, []byte) error) error {
		var wg sync.WaitGroup
		for i := n - 1; i >= 0; i-- {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = emit(i, []byte(fmt.Sprintf("img%d", i)))
			}()
		}
		wg.Wait()
		return nil
	}
}

func TestPrepareMediaAndPost(t *testing.T) {
	// 21 images: a 20-image carousel root plus a single-image reply, which
	// must not get a carousel item container.
	const n = 21
	p, ft := newTestPoster(t)
	ctx := context.Background()

	media, err := p.PrepareMedia(ctx, n, emitAll(n))
	if err != nil {
		t.Fatalf("PrepareMedia: %v", err)
	}
	for i := range n {
		if want := fmt.Sprintf("https://picsur.example.com/i/img%d.png", i); media.urls[i] != want {
			t.Errorf("urls[%d] = %q, want %q", i, media.urls[i], want)
		}
		if got := ft.itemImages[media.containers[i]]; (i < 20) != (got == media.urls[i]) {
			t.Errorf("image %d: container %q has image %q", i, media.containers[i], got)
		}
	}
	if len(ft.itemImages) != 20 {
		t.Errorf("created %d carousel item containers, want 20", len(ft.itemImages))
	}

	if err := p.Post(ctx, media, "Doc 1", time.Now(), "https://sho.rt/x", "summary"); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(ft.carousels) != 1 || strings.Join(ft.carousels[0], ",") != strings.Join(media.containers[:20], ",") {
		t.Errorf("carousel children = %v, want %v", ft.carousels, media.containers[:20])
	}
	if len(ft.singleImage) != 1 || ft.singleImage[0] != media.urls[20] {
		t.Errorf("single image posts = %v, want [%s]", ft.singleImage, media.urls[20])
	}
	if ft.publishes != 2 || ft.replyTo[0] != "" || ft.replyTo[1] == "" {
		t.Errorf("publishes = %d, reply_to = %q; want root then a reply", ft.publishes, ft.replyTo)
	}
}

func TestPrepareMediaMissingImage(t *testing.T) {
	p, _ := newTestPoster(t)
	_, err := p.PrepareMedia(context.Background(), 3, func(_ context.Context, emit func(int, []byte) error) error {
		_ = emit(0, []byte("a"))
		return emit(2, []byte("c"))
	})
	if err == nil {
		t.Fatal("expected an error when an image is never emitted")
	}
}

// A container that fails to create while staging must not fail the document;
// it is created when its chunk is posted.
func TestContainerFailureRetriedAtPost(t *testing.T) {
	const n = 41 // chunks of 20, 20, 1
	p, ft := newTestPoster(t)
	ctx := context.Background()
	ft.failOnce["https://picsur.example.com/i/img25.png"] = true

	media, err := p.PrepareMedia(ctx, n, emitAll(n))
	if err != nil {
		t.Fatalf("PrepareMedia: %v", err)
	}
	if media.containers[25] != "" {
		t.Fatalf("container 25 = %q, want none after failed create", media.containers[25])
	}
	if err := p.Post(ctx, media, "Doc 1", time.Now(), "", ""); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(ft.carousels) != 2 || ft.itemImages[ft.carousels[1][5]] != media.urls[25] {
		t.Errorf("second carousel child 6 = %q, want a container for %s", ft.carousels, media.urls[25])
	}
	if ft.publishes != 3 {
		t.Errorf("publishes = %d, want 3", ft.publishes)
	}
}
