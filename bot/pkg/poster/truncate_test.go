package poster

import (
	"strings"
	"testing"
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
