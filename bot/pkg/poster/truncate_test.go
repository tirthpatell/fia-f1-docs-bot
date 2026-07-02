package poster

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncateText(t *testing.T) {
	// Multi-byte input: must count runes and never split one.
	text := strings.Repeat("é", 600)
	got := truncateText(text, 500)
	if !utf8.ValidString(got) {
		t.Error("truncation split a rune")
	}
	if utf8.RuneCountInString(got) > 500 {
		t.Errorf("got %d runes, want <= 500", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, ellipsis) {
		t.Error("missing ellipsis")
	}

	// Short text passes through untouched.
	if got := truncateText("short", 500); got != "short" {
		t.Errorf("short text changed: %q", got)
	}

	// Word-boundary truncation.
	got = truncateText("hello world this is a long sentence", 15)
	if utf8.RuneCountInString(got) > 15 {
		t.Errorf("got %d runes, want <= 15", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, ellipsis) {
		t.Error("missing ellipsis")
	}
}

// formatPostText must never produce text over the Threads character limit,
// even when a very long title leaves no room for the summary section.
func TestFormatPostTextRespectsCharacterLimit(t *testing.T) {
	p := &Poster{} // documentURL is empty in all cases, so no clients are used
	publishTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		title   string
		summary string
	}{
		{"normal", "Doc 12 - Car 44 - Alleged breach", "Steward summary text."},
		{"empty summary", "Doc 12 - Car 44", ""},
		{"long summary", "Doc 12", strings.Repeat("word ", 200)},
		{"title fills the limit", strings.Repeat("x", 480), "Some summary."},
		{"title leaves no room for label", strings.Repeat("x", 460), "Some summary."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.formatPostText(context.Background(), tt.title, publishTime, "", tt.summary)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n := utf8.RuneCountInString(got); n > maxCharacterLimit {
				t.Errorf("post text is %d runes, want <= %d", n, maxCharacterLimit)
			}
			if strings.HasSuffix(got, "AI Summary: ") {
				t.Error("dangling AI Summary label")
			}
		})
	}
}
