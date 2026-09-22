package syncer

import (
	"github.com/egori/facebook-aggregator/internal/domain"
	"testing"
)

func TestUniquePosts(t *testing.T) {
	in := []domain.FacebookPost{{ID: "2", Text: "new"}, {ID: "1"}, {ID: "2", Text: "old"}, {ID: ""}}
	out := uniquePosts(in)
	if len(out) != 2 || out[0].ID != "2" || out[0].Text != "new" {
		t.Fatalf("unexpected dedup: %+v", out)
	}
}
