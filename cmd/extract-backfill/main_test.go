package main

import "testing"

func TestNextBatchSizeHonorsTotalLimit(t *testing.T) {
	tests := []struct {
		name                    string
		batch, limit, processed int
		want                    int
	}{
		{name: "unlimited", batch: 100, limit: 0, processed: 500, want: 100},
		{name: "limit below batch", batch: 100, limit: 5, processed: 0, want: 5},
		{name: "remaining", batch: 3, limit: 5, processed: 3, want: 2},
		{name: "limit reached", batch: 100, limit: 5, processed: 5, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextBatchSize(tc.batch, tc.limit, tc.processed); got != tc.want {
				t.Fatalf("got=%d want=%d", got, tc.want)
			}
		})
	}
}
