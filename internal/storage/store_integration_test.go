package storage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/storage"
	"github.com/egori/facebook-aggregator/migrations"
	"os"
	"testing"
	"time"
)

func TestPostgresDedupAndSearch(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, e := storage.Open(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = migrations.Up(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	g, e := s.AddGroup(ctx, "fb-"+suffix, "https://example.invalid/"+suffix, "test", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DeleteGroup(ctx, g.ID)
	rent := int64(5_000_000)
	beds := 2
	area := 50.0
	post := domain.FacebookPost{ID: "post-" + suffix, GroupID: g.FacebookID, URL: "https://example.invalid/post", Text: "2PN 50m2 5tr", PublishedAt: time.Now(), Raw: json.RawMessage(`{"ok":true}`)}
	listing := domain.Listing{GroupID: g.ID, FacebookPostID: post.ID, FacebookURL: post.URL, OriginalText: post.Text, PublishedAt: post.PublishedAt, RentMin: &rent, RentMax: &rent, Bedrooms: &beds, AreaM2: &area, District: "Sơn Trà", PropertyType: "apartment", Currency: "VND", Amenities: map[string]bool{}, Utilities: map[string]any{}, RawValues: map[string]any{}, Confidence: domain.Confidence{}, DealScore: 90, ScoreConfidence: .8}
	inserted, e := s.InsertListing(ctx, post, listing)
	if e != nil || !inserted {
		t.Fatalf("first insert=%v err=%v", inserted, e)
	}
	inserted, e = s.InsertListing(ctx, post, listing)
	if e != nil || inserted {
		t.Fatalf("duplicate insert=%v err=%v", inserted, e)
	}
	page, e := s.Search(ctx, 999, domain.SearchFilter{District: "Sơn Trà", RentMax: &rent, Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, x := range page.Items {
		if x.FacebookPostID == post.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("inserted listing absent from search: total=%d", page.Total)
	}
}
