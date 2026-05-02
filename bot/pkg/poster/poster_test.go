package poster

import (
	"reflect"
	"strconv"
	"testing"
)

func TestChunkURLs(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		size int
		want [][]string
	}{
		{
			name: "empty",
			urls: []string{},
			size: 20,
			want: nil,
		},
		{
			name: "non-positive size returns nil",
			urls: []string{"a", "b", "c"},
			size: 0,
			want: nil,
		},
		{
			name: "single",
			urls: []string{"a"},
			size: 20,
			want: [][]string{{"a"}},
		},
		{
			name: "exactly one chunk",
			urls: makeURLs(20),
			size: 20,
			want: [][]string{makeURLs(20)},
		},
		{
			name: "one over chunk size",
			urls: makeURLs(21),
			size: 20,
			want: [][]string{makeURLs(20), {"url20"}},
		},
		{
			name: "exactly two chunks",
			urls: makeURLs(40),
			size: 20,
			want: [][]string{makeURLs(20), makeURLsFrom(20, 40)},
		},
		{
			name: "two and a half chunks",
			urls: makeURLs(45),
			size: 20,
			want: [][]string{makeURLs(20), makeURLsFrom(20, 40), makeURLsFrom(40, 45)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chunkURLs(tt.urls, tt.size)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chunkURLs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func makeURLs(n int) []string {
	return makeURLsFrom(0, n)
}

func makeURLsFrom(start, end int) []string {
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, "url"+strconv.Itoa(i))
	}
	return out
}
