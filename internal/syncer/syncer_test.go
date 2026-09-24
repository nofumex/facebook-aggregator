package syncer

import (
	"testing"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
)

func TestUniquePosts(t *testing.T) {
	in := []domain.FacebookPost{{ID: "2", Text: "new"}, {ID: "1"}, {ID: "2", Text: "old"}, {ID: ""}}
	out := uniquePosts(in)
	if len(out) != 2 || out[0].ID != "2" || out[0].Text != "new" {
		t.Fatalf("unexpected dedup: %+v", out)
	}
}

func TestRecoveryFetchCapsPostsAndKeepsIncrementalCursor(t *testing.T) {
	last := time.Now().Add(-72 * time.Hour)
	group := domain.Group{FacebookID: "fb-group", LastPostID: "last-post", LastPostAt: &last}
	recovery := fetchRequest(group, 50)
	if recovery.MaxPosts != 50 || recovery.StopPostID != "last-post" || !recovery.StopBefore.Equal(last) || recovery.Overlap != 6*time.Hour {
		t.Fatalf("recovery=%+v", recovery)
	}
	normal := fetchRequest(group, 0)
	if normal.MaxPosts != 0 || normal.StopPostID != recovery.StopPostID || normal.Overlap != recovery.Overlap {
		t.Fatalf("normal=%+v", normal)
	}
}
